//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
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
	"github.com/chibuike-kt/argus/internal/tracking"
)

// Phase 4.1a, real failures for the new-symbol path: the real Hyperliquid
// adapter, the real supervisor and symbol pipeline, real Postgres, and a
// local stand-in exchange that (1) kills the TCP connection in the middle
// of a new symbol's history backfill, twice, then serves it, and (2)
// rejects another symbol outright. The watchlist row must show what is
// happening -- retrying with the reason, then live; or retrying with the
// provider's own rejection -- never a silent stall.
//
// The symbols enter the tracked set through this test's own unique floor,
// so no other package's rows can change it.

type flakyExchange struct {
	killFirst  map[string]int // coin -> how many candleSnapshot requests to cut
	reject     map[string]bool
	mu         sync.Mutex
	calls      map[string]int
	connection atomic.Int64
}

func (x *flakyExchange) rest(w http.ResponseWriter, r *http.Request) {
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
	coin := body.Req.Coin
	x.mu.Lock()
	x.calls[coin]++
	n := x.calls[coin]
	x.mu.Unlock()
	if x.reject[coin] {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`"Unknown coin ` + coin + `"`))
		return
	}
	if n <= x.killFirst[coin] {
		// The provider connection dies mid-request.
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
				return
			}
		}
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
	end := time.UnixMilli(body.Req.EndTime).UTC()
	if now := time.Now().UTC(); end.After(now) {
		end = now
	}
	out := []wire{}
	for m := time.UnixMilli(body.Req.StartTime).UTC().Truncate(time.Minute); !m.After(end); m = m.Add(time.Minute) {
		if m.Before(time.UnixMilli(body.Req.StartTime)) {
			continue
		}
		out = append(out, wire{T: m.UnixMilli(), C: m.Add(time.Minute).UnixMilli() - 1, S: coin, I: "1m", O: "10.00", H: "10.00", L: "10.00", Cl: "10.00", V: "1", N: 1})
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (x *flakyExchange) socket(w http.ResponseWriter, r *http.Request) {
	conn, err := ws.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	x.connection.Add(1)
	for {
		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
	}
}

func TestIntegration_Supervisor_NewSymbol_ProviderFailuresShowOnTheRow(t *testing.T) {
	for _, zone := range []string{"Etc/UTC", "America/New_York"} {
		t.Run(zone, func(t *testing.T) {
			db := dbInSessionZone(t, zone)
			clk := clock.Real{}
			stamp := time.Now().UnixNano()
			newSym, badSym := fmt.Sprintf("SUPNEW%d", stamp), fmt.Sprintf("SUPBAD%d", stamp)

			x := &flakyExchange{killFirst: map[string]int{newSym: 2}, reject: map[string]bool{badSym: true}, calls: map[string]int{}}
			restSrv := httptest.NewServer(http.HandlerFunc(x.rest))
			defer restSrv.Close()
			wsSrv := httptest.NewServer(http.HandlerFunc(x.socket))
			defer wsSrv.Close()

			instrumentStore := instruments.NewStore(db)
			newID := seedHyperliquidTestInstrument(t, instrumentStore, newSym)
			badID := seedHyperliquidTestInstrument(t, instrumentStore, badSym)

			provider := hyperliquid.NewProvider(clk, nil,
				hyperliquid.WithBaseURL(restSrv.URL),
				hyperliquid.WithWSURL("ws"+strings.TrimPrefix(wsSrv.URL, "http")),
				hyperliquid.WithHeartbeat(100*time.Millisecond, 5*time.Second))
			marketdataStore := marketdata.NewStore(db, clk)
			cache := ingestion.NewInstrumentCache(instrumentStore)
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 4}))
			svc := ingestion.NewService(hyperliquid.ProviderName, provider, ingestion.NewNormalizer(cache, clk), cache, marketdataStore, marketdata.ContinuousCalendar{}, logger)
			cfg := config.Config{
				Live:        config.LiveFeed{ReconnectBackoffBase: 20 * time.Millisecond, ReconnectBackoffCap: 100 * time.Millisecond},
				Hyperliquid: config.Hyperliquid{BackfillDays: 1},
			}
			state := storeBackfillState{instruments: instrumentStore, candles: marketdataStore}
			ts := tracking.NewStore(db)

			// Record every state the rows go through, in order.
			var mu sync.Mutex
			seen := map[int64][]tracking.Status{}
			rec := recordingStates{Store: ts, onSet: func(s tracking.Status) {
				mu.Lock()
				seen[s.InstrumentID] = append(seen[s.InstrumentID], s)
				mu.Unlock()
			}}
			sup := newSupervisor([]string{newSym, badSym}, 8, time.Minute, 2, rec, func(ctx context.Context, symbol string, hooks symbolHooks) {
				runHyperliquidSymbolWith(ctx, symbol, svc, state, cfg, logger, clk, hooks)
			}, clk, logger)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); sup.run(ctx, 100*time.Millisecond) }()

			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				st, _ := ts.States(context.Background(), []int64{newID, badID})
				if st[newID].State == tracking.Live && st[badID].State == tracking.Retrying && st[badID].Attempt >= 3 {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			cancel()
			<-done

			mu.Lock()
			defer mu.Unlock()
			// The new symbol: queued, loading, retrying with the cut
			// connection's error, then live -- and its history is stored.
			var sawRetry, sawLive bool
			for _, s := range seen[newID] {
				if s.State == tracking.Retrying && strings.Contains(s.Reason, "history backfill failed") {
					sawRetry = true
				}
				if s.State == tracking.Live {
					sawLive = true
				}
				if s.UpdatedAt.Location() != time.UTC {
					t.Fatalf("state time %s is not UTC", s.UpdatedAt)
				}
			}
			if !sawRetry || !sawLive {
				t.Fatalf("new symbol states %+v, want retrying (the killed connection) and then live", seen[newID])
			}
			var stored int
			_ = db.QueryRow(`SELECT count(*) FROM candles WHERE instrument_id = $1 AND is_closed`, newID).Scan(&stored)
			if stored < 60 {
				t.Fatalf("new symbol has %d closed candles after its backfill, want its history", stored)
			}

			// The rejected symbol: retrying with the provider's own words,
			// several attempts, never live, nothing stored.
			last := seen[badID][len(seen[badID])-1]
			if last.State != tracking.Retrying || !strings.Contains(last.Reason, "Unknown coin") || last.Attempt < 3 {
				t.Fatalf("rejected symbol last state %+v, want retrying with the provider's rejection, attempt >= 3", last)
			}
			for _, s := range seen[badID] {
				if s.State == tracking.Live {
					t.Fatal("the rejected symbol was reported live")
				}
			}
			var badStored int
			_ = db.QueryRow(`SELECT count(*) FROM candles WHERE instrument_id = $1`, badID).Scan(&badStored)
			if badStored != 0 {
				t.Fatalf("rejected symbol stored %d candles", badStored)
			}
		})
	}
}

// Architect review of 4.1a, item 1: a backfill slot is held only while one
// attempt runs. Two symbols Hyperliquid keeps rejecting, with both slots
// (concurrency 2), must not starve a healthy third symbol: it reaches live.
func TestIntegration_Supervisor_RejectedSymbolsDoNotHoldEveryBackfillSlot(t *testing.T) {
	db := dbInSessionZone(t, "America/New_York")
	clk := clock.Real{}
	stamp := time.Now().UnixNano()
	bad1, bad2, good := fmt.Sprintf("SLOTBADA%d", stamp), fmt.Sprintf("SLOTBADB%d", stamp), fmt.Sprintf("SLOTGOOD%d", stamp)

	x := &flakyExchange{killFirst: map[string]int{}, reject: map[string]bool{bad1: true, bad2: true}, calls: map[string]int{}}
	restSrv := httptest.NewServer(http.HandlerFunc(x.rest))
	defer restSrv.Close()
	wsSrv := httptest.NewServer(http.HandlerFunc(x.socket))
	defer wsSrv.Close()

	instrumentStore := instruments.NewStore(db)
	bad1ID := seedHyperliquidTestInstrument(t, instrumentStore, bad1)
	bad2ID := seedHyperliquidTestInstrument(t, instrumentStore, bad2)
	goodID := seedHyperliquidTestInstrument(t, instrumentStore, good)

	provider := hyperliquid.NewProvider(clk, nil,
		hyperliquid.WithBaseURL(restSrv.URL),
		hyperliquid.WithWSURL("ws"+strings.TrimPrefix(wsSrv.URL, "http")),
		hyperliquid.WithHeartbeat(100*time.Millisecond, 5*time.Second))
	marketdataStore := marketdata.NewStore(db, clk)
	cache := ingestion.NewInstrumentCache(instrumentStore)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 4}))
	svc := ingestion.NewService(hyperliquid.ProviderName, provider, ingestion.NewNormalizer(cache, clk), cache, marketdataStore, marketdata.ContinuousCalendar{}, logger)
	cfg := config.Config{
		Live:        config.LiveFeed{ReconnectBackoffBase: 20 * time.Millisecond, ReconnectBackoffCap: 100 * time.Millisecond},
		Hyperliquid: config.Hyperliquid{BackfillDays: 1, NewSymbolBackfillRetryCap: 200 * time.Millisecond},
	}
	state := storeBackfillState{instruments: instrumentStore, candles: marketdataStore}
	ts := tracking.NewStore(db)

	// The healthy symbol is hidden from the tracked set until both
	// rejected ones have taken a slot and failed an attempt, so the test
	// does not depend on which goroutine reaches the gate first.
	hidden := &hidingTracked{Store: ts, hide: good}
	hidden.hiding.Store(true)
	sup := newSupervisor([]string{bad1, bad2, good}, 8, time.Minute, 2, hidden, func(ctx context.Context, symbol string, hooks symbolHooks) {
		runHyperliquidSymbolWith(ctx, symbol, svc, state, cfg, logger, clk, hooks)
	}, clk, logger)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); sup.run(ctx, 100*time.Millisecond) }()
	defer func() { cancel(); <-done }()

	var st map[int64]tracking.Status
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		st, _ = ts.States(context.Background(), []int64{bad1ID, bad2ID})
		if st[bad1ID].State == tracking.Retrying && st[bad2ID].State == tracking.Retrying {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	hidden.hiding.Store(false)

	deadline = time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		st, _ = ts.States(context.Background(), []int64{bad1ID, bad2ID, goodID})
		if st[goodID].State == tracking.Live && st[bad1ID].Attempt >= 3 && st[bad2ID].Attempt >= 3 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("states %+v: want the healthy symbol live while the two rejected ones keep retrying (attempt >= 3)", st)
}

// hidingTracked leaves hide out of the tracked set while hiding is set.
type hidingTracked struct {
	*tracking.Store
	hide   string
	hiding atomic.Bool
}

func (h *hidingTracked) Tracked(ctx context.Context, floor []string) ([]tracking.Tracked, error) {
	all, err := h.Store.Tracked(ctx, floor)
	if err != nil || !h.hiding.Load() {
		return all, err
	}
	out := all[:0:0]
	for _, t := range all {
		if t.Symbol != h.hide {
			out = append(out, t)
		}
	}
	return out, nil
}

// recordingStates wraps tracking.Store, reporting every write.
type recordingStates struct {
	*tracking.Store
	onSet func(tracking.Status)
}

func (r recordingStates) SetState(ctx context.Context, id int64, st tracking.State, reason string, attempt int, at time.Time) error {
	r.onSet(tracking.Status{InstrumentID: id, State: st, Reason: reason, Attempt: attempt, UpdatedAt: at})
	return r.Store.SetState(ctx, id, st, reason, attempt, at)
}
