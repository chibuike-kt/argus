//go:build integration

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ws "github.com/coder/websocket"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/ingestion/hyperliquid"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

// Gap recording (fix/gap-recording). The real-run finding: candle_gaps
// held about 2,500 open rows, one per instrument per minute, each exactly
// 5 minutes long with sub-minute edges ([01:43:23.92244, 01:48:23.92244)),
// written 0.44 s (median) after its own end. The periodic backfill fetched
// [now-5m, now) with an unaligned now, and the gap walker stepped from the
// unaligned start, so no slot ever matched a candle and the whole window
// was recorded as a gap on every tick. The reconnect backfill (end = now)
// also walked the minute still forming.
//
// This runs the real Hyperliquid adapter against a local fake exchange
// whose WebSocket drops every connection (a real reconnect each time) and
// whose REST serves every minute through the forming one except one real
// hole, with the periodic backfill on. Run with the Postgres session time
// zone UTC and America/New_York (CLAUDE.md, check 1).

func dbInSessionZone(t *testing.T, zone string) *sql.DB {
	t.Helper()
	dsn := os.Getenv("ARGUS_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://argus:argus@localhost:5432/argus?sslmode=disable"
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	dsn += sep + "options=-c%20timezone%3D" + strings.ReplaceAll(zone, "/", "%2F")
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("integration database unreachable: %v", err)
	}
	var got string
	if err := db.QueryRowContext(ctx, "SHOW TimeZone").Scan(&got); err != nil || got != zone {
		t.Fatalf("session time zone = %q (%v), want %q", got, err, zone)
	}
	return db
}

// fakeExchange is a local Hyperliquid: candleSnapshot over REST, and a
// WebSocket that accepts, reads the subscription, stays quiet for hold,
// then drops the connection.
type fakeExchange struct {
	hole        time.Time // the one minute the exchange does not have
	hold        time.Duration
	connections atomic.Int64
	snapshots   atomic.Int64
}

func (x *fakeExchange) rest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type string `json:"type"`
		Req  struct {
			Coin      string `json:"coin"`
			StartTime int64  `json:"startTime"`
			EndTime   int64  `json:"endTime"`
		} `json:"req"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Type != "candleSnapshot" {
		_, _ = w.Write([]byte("[]"))
		return
	}
	x.snapshots.Add(1)
	// Every minute from start through the one still forming, like the real
	// exchange, except the hole.
	end := time.UnixMilli(body.Req.EndTime).UTC()
	if now := time.Now().UTC(); end.After(now) {
		end = now
	}
	type wire struct {
		T  int64  `json:"t"`
		C  int64  `json:"T"`
		S  string `json:"s"`
		I  string `json:"i"`
		O  string `json:"o"`
		H  string `json:"h"`
		L  string `json:"l"`
		Cl string `json:"c"`
		V  string `json:"v"`
		N  int64  `json:"n"`
	}
	out := []wire{}
	for m := time.UnixMilli(body.Req.StartTime).UTC().Truncate(time.Minute); !m.After(end); m = m.Add(time.Minute) {
		if m.Before(time.UnixMilli(body.Req.StartTime)) || m.Equal(x.hole) {
			continue
		}
		out = append(out, wire{T: m.UnixMilli(), C: m.Add(time.Minute).UnixMilli() - 1, S: body.Req.Coin, I: "1m",
			O: "100.0", H: "100.0", L: "100.0", Cl: "100.0", V: "1", N: 1})
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (x *fakeExchange) socket(w http.ResponseWriter, r *http.Request) {
	conn, err := ws.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	x.connections.Add(1)
	ctx, cancel := context.WithTimeout(r.Context(), x.hold)
	defer cancel()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return // the hold ran out (or the client left): drop the connection
		}
	}
}

type gapRow struct {
	start, end time.Time
}

func gapsOf(t *testing.T, db *sql.DB, instrumentID int64) []gapRow {
	t.Helper()
	rows, err := db.Query(`SELECT gap_start, gap_end FROM candle_gaps WHERE instrument_id = $1 ORDER BY gap_start`, instrumentID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []gapRow
	for rows.Next() {
		var g gapRow
		if err := rows.Scan(&g.start, &g.end); err != nil {
			t.Fatal(err)
		}
		out = append(out, g)
	}
	return out
}

func TestIntegration_GapRecording_RealReconnect_OnlyTheRealHoleIsRecorded(t *testing.T) {
	for _, zone := range []string{"Etc/UTC", "America/New_York"} {
		t.Run(zone, func(t *testing.T) {
			db := dbInSessionZone(t, zone)
			clk := clock.Real{}
			now := clk.Now()
			// Keep the run inside one wall-clock minute region far enough from
			// the hole that the hole stays in the periodic window throughout.
			hole := now.Truncate(time.Minute).Add(-3 * time.Minute)

			x := &fakeExchange{hole: hole, hold: 900 * time.Millisecond}
			restSrv := httptest.NewServer(http.HandlerFunc(x.rest))
			defer restSrv.Close()
			wsSrv := httptest.NewServer(http.HandlerFunc(x.socket))
			defer wsSrv.Close()

			symbol := fmt.Sprintf("HLGAPREC-%d", time.Now().UnixNano())
			instrumentStore := instruments.NewStore(db)
			instrumentID := seedHyperliquidTestInstrument(t, instrumentStore, symbol)
			// Stored history up to the minute before the hole.
			insertClosedCandles(t, db, instrumentID, hole.Add(-time.Minute), 30)

			provider := hyperliquid.NewProvider(clk, nil,
				hyperliquid.WithBaseURL(restSrv.URL),
				hyperliquid.WithWSURL("ws"+strings.TrimPrefix(wsSrv.URL, "http")),
				hyperliquid.WithHeartbeat(100*time.Millisecond, 5*time.Second))
			marketdataStore := marketdata.NewStore(db, clk)
			cache := ingestion.NewInstrumentCache(instrumentStore)
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
			svc := ingestion.NewService(hyperliquid.ProviderName, provider, ingestion.NewNormalizer(cache, clk), cache, marketdataStore, marketdata.ContinuousCalendar{}, logger)
			cfg := config.Config{
				Live: config.LiveFeed{ReconnectBackoffBase: 10 * time.Millisecond, ReconnectBackoffCap: 50 * time.Millisecond},
				Hyperliquid: config.Hyperliquid{
					BackfillDays:             1,
					PeriodicBackfillInterval: 200 * time.Millisecond,
					PeriodicBackfillWindow:   5 * time.Minute,
				},
			}

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				defer close(done)
				runHyperliquidSymbol(ctx, symbol, svc, storeBackfillState{instruments: instrumentStore, candles: marketdataStore}, cfg, logger, clk)
			}()
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) && (x.connections.Load() < 3 || x.snapshots.Load() < 8) {
				time.Sleep(50 * time.Millisecond)
			}
			cancel()
			<-done
			endedAt := clk.Now()

			if c := x.connections.Load(); c < 3 {
				t.Fatalf("%d WebSocket connections, want at least 3 real reconnects", c)
			}
			if s := x.snapshots.Load(); s < 8 {
				t.Fatalf("%d candleSnapshot fetches, want the startup, reconnect and periodic backfills (>= 8)", s)
			}

			gaps := gapsOf(t, db, instrumentID)
			for _, g := range gaps {
				if !g.start.Equal(g.start.Truncate(time.Minute)) || !g.end.Equal(g.end.Truncate(time.Minute)) {
					t.Errorf("gap [%s, %s) is not on minute bounds", g.start.UTC(), g.end.UTC())
				}
				if g.end.After(endedAt.Truncate(time.Minute)) {
					t.Errorf("gap [%s, %s) reaches the minute still forming", g.start.UTC(), g.end.UTC())
				}
				var covered int
				if err := db.QueryRow(`SELECT count(*) FROM candles WHERE instrument_id = $1 AND superseded_at IS NULL AND is_closed
					AND open_time >= $2 AND open_time < $3`, instrumentID, g.start, g.end).Scan(&covered); err != nil {
					t.Fatal(err)
				}
				if covered > 0 {
					t.Errorf("gap [%s, %s) covers %d minute(s) that have a closed candle", g.start.UTC(), g.end.UTC(), covered)
				}
			}
			if len(gaps) != 1 || !gaps[0].start.Equal(hole) || !gaps[0].end.Equal(hole.Add(time.Minute)) {
				t.Fatalf("gaps = %+v, want exactly the real hole [%s, %s)", gaps, hole.UTC(), hole.Add(time.Minute).UTC())
			}
		})
	}
}
