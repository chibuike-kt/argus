//go:build integration

package main

import (
	"context"
	"database/sql"
	"errors"
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
)

// outageProvider is a fake ingestion.Provider for the outage tests.
// HistoricalCandles fails its first failFirst calls (the network is
// still down), then returns every minute in the requested window --
// the exchange's real history -- and records each window it was asked
// for. StreamCandles fails its first failStreams calls, then stays
// connected (blocks) until ctx ends.
type outageProvider struct {
	mu          sync.Mutex
	failFirst   int
	failStreams int
	histCalls   int
	streamCalls int
	windows     [][2]time.Time
}

func (p *outageProvider) Instruments(context.Context) ([]ingestion.ProviderInstrument, error) {
	return nil, nil
}

func (p *outageProvider) HistoricalCandles(_ context.Context, symbol string, start, end time.Time) ([]ingestion.ProviderCandle, error) {
	p.mu.Lock()
	p.histCalls++
	n := p.histCalls
	p.windows = append(p.windows, [2]time.Time{start, end})
	p.mu.Unlock()
	if n <= p.failFirst {
		return nil, fmt.Errorf("network unreachable (scripted failure %d)", n)
	}
	var out []ingestion.ProviderCandle
	for t := start.Truncate(time.Minute); t.Before(end); t = t.Add(time.Minute) {
		if t.Before(start) {
			continue
		}
		out = append(out, ingestion.ProviderCandle{
			Symbol: symbol, OpenTime: t, Open: "100", High: "100", Low: "100", Close: "100", Volume: "1", IsFinal: true,
		})
	}
	if len(out) > 0 {
		out[len(out)-1].IsFinal = false // the newest minute may still be forming
	}
	return out, nil
}

func (p *outageProvider) StreamCandles(ctx context.Context, _ string, _ func(context.Context, ingestion.ProviderCandle) error) error {
	p.mu.Lock()
	p.streamCalls++
	n := p.streamCalls
	p.mu.Unlock()
	if n <= p.failStreams {
		return fmt.Errorf("dial: network unreachable (scripted stream failure %d)", n)
	}
	<-ctx.Done()
	return nil
}

func (p *outageProvider) StreamQuotes(ctx context.Context, _ string, _ func(context.Context, ingestion.ProviderQuote) error) error {
	<-ctx.Done()
	return nil
}

func (p *outageProvider) snapshot() (hist int, windows [][2]time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.histCalls, append([][2]time.Time(nil), p.windows...)
}

// insertClosedCandles stores n current, closed 1-minute candles for
// instrumentID ending with the one that opens at newest, in one
// statement (close 1000 = "100" at the test instrument's price scale 1).
func insertClosedCandles(t *testing.T, db *sql.DB, instrumentID int64, newest time.Time, n int) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO candles (instrument_id, open_time, close_time, open, high, low, close, volume,
		                     revision, is_closed, tick_aligned, provider, ingested_at)
		SELECT $1, $2::timestamptz - i * interval '1 minute', $2::timestamptz - (i - 1) * interval '1 minute',
		       1000, 1000, 1000, 1000, 1, 1, TRUE, TRUE, 'hyperliquid', now()
		FROM generate_series(0, $3::int - 1) AS i`, instrumentID, newest, n)
	if err != nil {
		t.Fatalf("insert closed candles: %v", err)
	}
}

// missingMinutes lists every minute in [from, to] with no current,
// closed candle -- computed in SQL over the real table.
func missingMinutes(t *testing.T, db *sql.DB, instrumentID int64, from, to time.Time) []time.Time {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT m FROM generate_series($2::timestamptz, $3::timestamptz, interval '1 minute') AS m
		WHERE NOT EXISTS (SELECT 1 FROM candles c WHERE c.instrument_id = $1 AND c.open_time = m
		                  AND c.superseded_at IS NULL AND c.is_closed)
		ORDER BY m`, instrumentID, from, to)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []time.Time
	for rows.Next() {
		var m time.Time
		if err := rows.Scan(&m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

type outageHarness struct {
	db           *sql.DB
	symbol       string
	instrumentID int64
	svc          *ingestion.Service
	state        storeBackfillState
	cfg          config.Config
	logger       *slog.Logger
}

func newOutageHarness(t *testing.T, provider ingestion.Provider) outageHarness {
	t.Helper()
	db := testWorkerDB(t)
	clk := clock.Real{}
	symbol := fmt.Sprintf("HLGAP-%d", time.Now().UnixNano())
	instrumentStore := instruments.NewStore(db)
	instrumentID := seedHyperliquidTestInstrument(t, instrumentStore, symbol)
	marketdataStore := marketdata.NewStore(db, clk)
	cache := ingestion.NewInstrumentCache(instrumentStore)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := ingestion.NewService(hyperliquid.ProviderName, provider, ingestion.NewNormalizer(cache, clk), cache, marketdataStore, marketdata.ContinuousCalendar{}, logger)
	cfg := config.Config{
		Live:        config.LiveFeed{ReconnectBackoffBase: 10 * time.Millisecond, ReconnectBackoffCap: 50 * time.Millisecond},
		Hyperliquid: config.Hyperliquid{BackfillDays: 3}, // periodic backfill off (interval 0)
	}
	return outageHarness{db: db, symbol: symbol, instrumentID: instrumentID, svc: svc,
		state: storeBackfillState{instruments: instrumentStore, candles: marketdataStore}, cfg: cfg, logger: logger}
}

// runUntil runs runHyperliquidSymbol (real clock) until cond holds or
// limit elapses, then stops it and reports whether cond held. Polling a
// condition, not sleeping a fixed time: applying a few hundred candles
// takes a variable few seconds on a loaded machine.
func (h outageHarness) runUntil(t *testing.T, limit time.Duration, cond func() bool) bool {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runHyperliquidSymbol(ctx, h.symbol, h.svc, h.state, h.cfg, h.logger, clock.Real{})
	}()
	deadline := time.Now().Add(limit)
	ok := false
	for time.Now().Before(deadline) {
		if cond() {
			ok = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	<-done
	return ok
}

// (1) After a long outage the first backfill attempts fail (the network
// is still coming back). The hole must still be filled once a request
// succeeds. The old code made one attempt from a time held in memory,
// then reset that time to "now": the hole stayed forever.
func TestIntegration_Outage_BackfillFailsTwiceThenSucceeds_HoleFilled(t *testing.T) {
	fake := &outageProvider{failFirst: 2, failStreams: 1}
	h := newOutageHarness(t, fake)
	now := time.Now().UTC().Truncate(time.Minute)
	newest := now.Add(-3 * time.Hour)
	insertClosedCandles(t, h.db, h.instrumentID, newest, 30)

	filled := func() bool {
		return len(missingMinutes(t, h.db, h.instrumentID, newest.Add(time.Minute), now.Add(-2*time.Minute))) == 0
	}
	h.runUntil(t, 30*time.Second, filled)

	hist, _ := fake.snapshot()
	if hist < 3 {
		t.Fatalf("historical calls = %d, want at least 3 (two failures, then a retry that succeeds)", hist)
	}
	// Every minute from just after the old newest to two minutes ago.
	if miss := missingMinutes(t, h.db, h.instrumentID, newest.Add(time.Minute), now.Add(-2*time.Minute)); len(miss) > 0 {
		t.Fatalf("%d minutes still missing after recovery, first %s last %s", len(miss), miss[0], miss[len(miss)-1])
	}
}

// (2) The process restarts with the newest stored candle 4 hours old,
// and the first request after the restart fails (the network is not up
// yet). Startup must keep retrying and fill the hole, and its window
// must come from the database (newest stored minus the overlap), not
// from a fixed look-back held in the process.
func TestIntegration_Restart_NewestStoredFourHoursOld_StartupFillsIt(t *testing.T) {
	fake := &outageProvider{failFirst: 1}
	h := newOutageHarness(t, fake)
	now := time.Now().UTC().Truncate(time.Minute)
	newest := now.Add(-4 * time.Hour)
	insertClosedCandles(t, h.db, h.instrumentID, newest, 30)

	h.runUntil(t, 30*time.Second, func() bool {
		return len(missingMinutes(t, h.db, h.instrumentID, newest.Add(time.Minute), now.Add(-2*time.Minute))) == 0
	})

	if miss := missingMinutes(t, h.db, h.instrumentID, newest.Add(time.Minute), now.Add(-2*time.Minute)); len(miss) > 0 {
		t.Fatalf("%d minutes still missing after restart, first %s", len(miss), miss[0])
	}
	_, windows := fake.snapshot()
	wantStart := newest.Add(-backfillOverlap)
	if len(windows) == 0 || !windows[0][0].Equal(wantStart) {
		t.Fatalf("first startup window = %v, want it to start at newest stored minus overlap (%s)", windows, wantStart)
	}
}

// (4) A hole older than the exchange's retention (newest stored 5 days
// ago; Hyperliquid keeps 5000 one-minute candles, about 3.5 days) can
// never be backfilled. It must be logged and recorded in candle_gaps,
// not silently skipped, and the fetch must start inside retention.
func TestIntegration_GapOlderThanRetention_IsRecordedNotIgnored(t *testing.T) {
	fake := &outageProvider{}
	h := newOutageHarness(t, fake)
	now := time.Now().UTC().Truncate(time.Minute)
	newest := now.Add(-5 * 24 * time.Hour)
	insertClosedCandles(t, h.db, h.instrumentID, newest, 30)

	store := marketdata.NewStore(h.db, clock.Real{})
	h.runUntil(t, 30*time.Second, func() bool {
		gaps, err := store.ListOpenGaps(context.Background(), h.instrumentID)
		if err != nil {
			return false
		}
		for _, g := range gaps {
			if g.Start.Time().Equal(newest.Add(time.Minute)) {
				return true
			}
		}
		return false
	})

	gaps, err := marketdata.NewStore(h.db, clock.Real{}).ListOpenGaps(context.Background(), h.instrumentID)
	if err != nil {
		t.Fatal(err)
	}
	retentionEdge := now.Add(-hyperliquidRetentionCandles * time.Minute).Add(retentionSafetyMargin)
	var lost *marketdata.Gap
	for i := range gaps {
		if gaps[i].Start.Time().Equal(newest.Add(time.Minute)) {
			lost = &gaps[i]
		}
	}
	if lost == nil {
		t.Fatalf("no open gap starting at %s (the minute after the newest stored candle); open gaps: %+v", newest.Add(time.Minute), gaps)
	}
	if d := lost.End.Time().Sub(retentionEdge); d < -2*time.Minute || d > 2*time.Minute {
		t.Fatalf("lost gap ends %s, want about the retention edge %s", lost.End.Time(), retentionEdge)
	}
	_, windows := fake.snapshot()
	if len(windows) == 0 || windows[0][0].Before(retentionEdge.Add(-2*time.Minute)) {
		t.Fatalf("first fetch window %v starts before the retention edge %s", windows, retentionEdge)
	}
}

// (3) A WebSocket that stays open but silent is detected and replaced:
// with the real Hyperliquid adapter against a local fake exchange whose
// socket never sends anything, the worker must close it and reconnect
// (more than one connection within a few stall timeouts). Before the
// watchdog the first connection was held for the 12-hour planned age.
func TestIntegration_StalledWebSocket_IsDetectedAndReplaced(t *testing.T) {
	var connections atomic.Int64
	wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := ws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		connections.Add(1)
		// Read and ignore everything (subscribe, pings) and never reply:
		// open but silent.
		for {
			if _, _, err := conn.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	defer wsSrv.Close()
	restSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	}))
	defer restSrv.Close()

	provider := hyperliquid.NewProvider(clock.Real{}, nil,
		hyperliquid.WithBaseURL(restSrv.URL),
		hyperliquid.WithWSURL("ws"+strings.TrimPrefix(wsSrv.URL, "http")),
		hyperliquid.WithHeartbeat(50*time.Millisecond, 250*time.Millisecond))
	h := newOutageHarness(t, provider)

	h.runUntil(t, 10*time.Second, func() bool { return connections.Load() >= 2 })

	if n := connections.Load(); n < 2 {
		t.Fatalf("connections = %d within 10s with a 250ms stall timeout, want the silent socket replaced (>= 2)", n)
	}
}

// A configured symbol with no instrument row is not a transient
// failure: the worker logs it once and stops that symbol, instead of
// retrying (and logging) forever.
func TestIntegration_SymbolWithoutInstrumentRow_StopsInsteadOfRetryingForever(t *testing.T) {
	h := newOutageHarness(t, &outageProvider{})
	h.symbol = fmt.Sprintf("NOT-REGISTERED-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	runHyperliquidSymbol(ctx, h.symbol, h.svc, h.state, h.cfg, h.logger, clock.Real{})
	if ctx.Err() != nil {
		t.Fatalf("runHyperliquidSymbol still retrying after %v for an unregistered symbol", time.Since(start))
	}
}

// Sanity for the error sentinel the worker logs on a stall.
func TestStallErrorIsDistinguishable(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", hyperliquid.ErrStreamStalled)
	if !errors.Is(err, hyperliquid.ErrStreamStalled) {
		t.Fatal("ErrStreamStalled not matched through wrapping")
	}
}
