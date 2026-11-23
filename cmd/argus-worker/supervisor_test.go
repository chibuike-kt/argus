package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/store"
	"github.com/chibuike-kt/argus/internal/tracking"
)

type fakeTracking struct {
	mu      sync.Mutex
	tracked []tracking.Tracked
	states  map[int64]tracking.Status
	writes  []tracking.Status
}

func (f *fakeTracking) Tracked(context.Context, []string) ([]tracking.Tracked, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tracking.Tracked(nil), f.tracked...), nil
}

func (f *fakeTracking) States(_ context.Context, ids []int64) (map[int64]tracking.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[int64]tracking.Status{}
	for _, id := range ids {
		if s, ok := f.states[id]; ok {
			out[id] = s
		}
	}
	return out, nil
}

func (f *fakeTracking) SetState(_ context.Context, id int64, st tracking.State, reason string, attempt int, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.states == nil {
		f.states = map[int64]tracking.Status{}
	}
	if cur, ok := f.states[id]; ok && cur.State == tracking.NeedsAttention && st != tracking.NeedsAttention {
		return nil // mirrors the upsert's guard
	}
	s := tracking.Status{InstrumentID: id, State: st, Reason: reason, Attempt: attempt, UpdatedAt: at}
	f.states[id] = s
	f.writes = append(f.writes, s)
	return nil
}

func (f *fakeTracking) set(tr ...tracking.Tracked) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tracked = tr
}

func (f *fakeTracking) state(id int64) tracking.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.states[id]
}

func supervisorTestLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// blockingStart is a pipeline that runs until stopped and counts starts.
type blockingStart struct {
	mu     sync.Mutex
	starts map[string]int
}

func (b *blockingStart) run(ctx context.Context, symbol string, _ symbolHooks) {
	b.mu.Lock()
	if b.starts == nil {
		b.starts = map[string]int{}
	}
	b.starts[symbol]++
	b.mu.Unlock()
	<-ctx.Done()
}

func (b *blockingStart) count(symbol string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.starts[symbol]
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 2s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func tr(id int64, sym string) tracking.Tracked { return tracking.Tracked{ID: id, Symbol: sym} }

func TestSupervisor_StartsTheTrackedSetWithinTheCapAndMarksTheRestWaiting(t *testing.T) {
	store := &fakeTracking{}
	store.set(tr(1, "BTC"), tr(2, "ETH"), tr(3, "SOL"), tr(4, "DOGE"))
	clk := clock.NewMutable(time.Date(2026, 11, 22, 4, 0, 0, 0, time.UTC))
	b := &blockingStart{}
	s := newSupervisor(nil, 3, 10*time.Minute, 2, store, b.run, clk, supervisorTestLogger())
	defer s.stopAll()

	if err := s.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := s.runningSymbols()
	if len(got) != 3 || !got["BTC"] || !got["ETH"] || !got["SOL"] {
		t.Fatalf("running %v, want the first 3 in cap order", got)
	}
	if st := store.state(4); st.State != tracking.Waiting || !strings.Contains(st.Reason, "cap of 3") {
		t.Fatalf("DOGE state = %+v, want waiting over the cap", st)
	}
	if cap(s.gate) != 2 {
		t.Fatalf("backfill gate holds %d, want the concurrency 2", cap(s.gate))
	}
}

func TestSupervisor_RemovalGraceKeepsThePipelineAndAnUndoDoesNotRestartIt(t *testing.T) {
	store := &fakeTracking{}
	store.set(tr(1, "BTC"), tr(5, "AVAX"))
	clk := clock.NewMutable(time.Date(2026, 11, 22, 4, 0, 0, 0, time.UTC))
	b := &blockingStart{}
	s := newSupervisor(nil, 8, 10*time.Minute, 2, store, b.run, clk, supervisorTestLogger())
	defer s.stopAll()
	ctx := context.Background()
	_ = s.reconcile(ctx)

	// AVAX leaves every watchlist.
	store.set(tr(1, "BTC"))
	_ = s.reconcile(ctx)
	clk.Advance(9 * time.Minute)
	_ = s.reconcile(ctx)
	if !s.runningSymbols()["AVAX"] {
		t.Fatal("AVAX stopped inside the 10 minute grace")
	}
	// Undo: back within the grace -- the same pipeline, no restart.
	store.set(tr(1, "BTC"), tr(5, "AVAX"))
	_ = s.reconcile(ctx)
	waitFor(t, func() bool { return b.count("AVAX") >= 1 }) // pipelines start on their own goroutine
	time.Sleep(50 * time.Millisecond)                       // room for a wrong second start to show
	if n := b.count("AVAX"); n != 1 {
		t.Fatalf("AVAX started %d times, want 1 (an undo inside the grace keeps it)", n)
	}
	// Removed again, and the grace runs out.
	store.set(tr(1, "BTC"))
	_ = s.reconcile(ctx)
	clk.Advance(10 * time.Minute)
	_ = s.reconcile(ctx)
	if s.runningSymbols()["AVAX"] {
		t.Fatal("AVAX still running after the grace")
	}
	if st := store.state(5); st.State != tracking.Stopped {
		t.Fatalf("AVAX state = %+v, want stopped", st)
	}
	if !s.runningSymbols()["BTC"] {
		t.Fatal("BTC stopped too")
	}
}

func TestSupervisor_NeedsAttentionIsNeverStartedAndStopsARunningSymbolAtOnce(t *testing.T) {
	store := &fakeTracking{}
	store.set(tr(1, "BTC"), tr(6, "LINK"))
	clk := clock.NewMutable(time.Date(2026, 11, 22, 4, 0, 0, 0, time.UTC))
	b := &blockingStart{}
	s := newSupervisor(nil, 8, 10*time.Minute, 2, store, b.run, clk, supervisorTestLogger())
	defer s.stopAll()
	ctx := context.Background()
	_ = s.reconcile(ctx)
	if !s.runningSymbols()["LINK"] {
		t.Fatal("LINK not started")
	}
	_ = store.SetState(ctx, 6, tracking.NeedsAttention, "price precision changed", 0, clk.Now())
	_ = s.reconcile(ctx) // no grace for a precision change
	if s.runningSymbols()["LINK"] {
		t.Fatal("LINK still running after its precision changed")
	}
	if st := store.state(6); st.State != tracking.NeedsAttention {
		t.Fatalf("LINK state = %+v, want needs_attention kept", st)
	}
	// And it is not restarted on later passes.
	_ = s.reconcile(ctx)
	time.Sleep(50 * time.Millisecond)
	if b.count("LINK") != 1 {
		t.Fatalf("LINK started %d times, want 1", b.count("LINK"))
	}
}

// The backfill gate: at most 2 new symbols load history at once; the rest
// are reported waiting until a slot frees.
func TestSymbolHooks_GateQueuesHistoryBackfills(t *testing.T) {
	gate := make(chan struct{}, 2)
	var mu sync.Mutex
	states := map[string]tracking.State{}
	hooksFor := func(sym string) symbolHooks {
		return symbolHooks{gate: gate, report: func(st tracking.State, _ string, _ int) {
			mu.Lock()
			states[sym] = st
			mu.Unlock()
		}}
	}
	release := make(chan struct{})
	inBackfill := make(chan string, 3)
	// The gated part of runHyperliquidSymbolWith, with a backfill that
	// blocks until released.
	run := func(ctx context.Context, sym string, h symbolHooks) {
		h.set(tracking.Waiting, "queued", 0)
		select {
		case h.gate <- struct{}{}:
		case <-ctx.Done():
			return
		}
		h.set(tracking.LoadingHistory, "", 0)
		inBackfill <- sym
		<-release
		<-h.gate
		h.set(tracking.Live, "", 0)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, sym := range []string{"A", "B", "C"} {
		go run(ctx, sym, hooksFor(sym))
	}
	<-inBackfill
	<-inBackfill
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	loading, waiting := 0, 0
	for _, st := range states {
		switch st {
		case tracking.LoadingHistory:
			loading++
		case tracking.Waiting:
			waiting++
		}
	}
	mu.Unlock()
	if loading != 2 || waiting != 1 {
		t.Fatalf("%d loading and %d waiting, want 2 and 1", loading, waiting)
	}
	close(release)
	select {
	case <-inBackfill:
	case <-time.After(2 * time.Second):
		t.Fatal("the third symbol never got a backfill slot")
	}
}

// ---- catalog refresh ----

type fakeInserter struct {
	inserted []instruments.NewInstrumentParams
}

func (f *fakeInserter) Insert(_ context.Context, p instruments.NewInstrumentParams) (instruments.Instrument, error) {
	f.inserted = append(f.inserted, p)
	return instruments.Instrument{NativeSymbol: p.NativeSymbol}, nil
}

type fakeCatalogStore struct {
	fakeTracking
	stored []store.Instrument
}

func (f *fakeCatalogStore) HyperliquidInstruments(context.Context) ([]store.Instrument, error) {
	return f.stored, nil
}

func perp(sym string, sz int32) ingestion.ProviderInstrument {
	return ingestion.ProviderInstrument{
		Symbol: sym, AssetClass: "crypto_perp", BaseAsset: sym, QuoteAsset: "USD", ContractType: "perpetual",
		PriceDecimalPlaces: 6 - sz, QtyDecimalPlaces: sz, TickSize: decimalOne(6 - sz), QtyStepSize: decimalOne(sz),
		SupportedIntervals: []string{"1m"},
	}
}

func decimalOne(d int32) string {
	if d <= 0 {
		return "1"
	}
	return "0." + strings.Repeat("0", int(d-1)) + "1"
}

func TestRefreshCatalog_CreatesDisabledRowsAndFlagsPrecisionChangesAndDelistings(t *testing.T) {
	cs := &fakeCatalogStore{stored: []store.Instrument{
		{ID: 1, NativeSymbol: "BTC", PriceScale: 1, QtyScale: 5, Enabled: true},  // unchanged
		{ID: 2, NativeSymbol: "LINK", PriceScale: 5, QtyScale: 1, Enabled: true}, // Hyperliquid now says szDecimals 2
		{ID: 3, NativeSymbol: "GONE", PriceScale: 4, QtyScale: 2, Enabled: true}, // no longer listed
		{ID: 4, NativeSymbol: "OLD", PriceScale: 4, QtyScale: 2, Enabled: false}, // unlisted but never enabled: left alone
	}}
	ins := &fakeInserter{}
	clk := clock.NewMutable(time.Date(2026, 11, 22, 4, 0, 0, 0, time.UTC))
	res, err := refreshCatalog(context.Background(), []ingestion.ProviderInstrument{perp("BTC", 5), perp("LINK", 2), perp("AVAX", 2)}, ins, cs, clk, supervisorTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 1 || len(ins.inserted) != 1 || ins.inserted[0].NativeSymbol != "AVAX" || *ins.inserted[0].PriceScale != 4 || *ins.inserted[0].QtyScale != 2 {
		t.Fatalf("created %+v, want only AVAX at price scale 4, qty scale 2", ins.inserted)
	}
	if st := cs.state(2); st.State != tracking.NeedsAttention || !strings.Contains(st.Reason, "nothing was rescaled") {
		t.Fatalf("LINK = %+v, want needs_attention for the precision change", st)
	}
	if st := cs.state(3); st.State != tracking.NeedsAttention || !strings.Contains(st.Reason, "no longer listed") {
		t.Fatalf("GONE = %+v, want needs_attention for the delisting", st)
	}
	if _, ok := cs.states[1]; ok {
		t.Fatal("BTC unchanged but a state was written")
	}
	if _, ok := cs.states[4]; ok {
		t.Fatal("an unlisted, never enabled row was flagged")
	}
}

// countingStart runs until stopped, counting starts and the most
// pipelines ever running at once. A symbol in failOnce returns at once on
// its first start (a pipeline that reported Failed).
type countingStart struct {
	mu       sync.Mutex
	starts   map[string]int
	current  int
	peak     int
	failOnce map[string]bool
}

func (c *countingStart) run(ctx context.Context, symbol string, _ symbolHooks) {
	c.mu.Lock()
	if c.starts == nil {
		c.starts = map[string]int{}
	}
	c.starts[symbol]++
	if c.failOnce[symbol] && c.starts[symbol] == 1 {
		c.mu.Unlock()
		return
	}
	c.current++
	c.peak = max(c.peak, c.current)
	c.mu.Unlock()
	<-ctx.Done()
	c.mu.Lock()
	c.current--
	c.mu.Unlock()
}

func (c *countingStart) count(symbol string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.starts[symbol]
}

func (c *countingStart) peakRunning() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peak
}

// Architect review of 4.1a, item 2: pipelines in their removal grace hold
// a connection, so they count against the cap. A wanted symbol that needs
// a slot stops the grace pipeline that left first.
func TestSupervisor_GracePipelinesCountAgainstTheCapAndTheOldestIsStoppedForANewSymbol(t *testing.T) {
	store := &fakeTracking{}
	store.set(tr(1, "AAA"), tr(2, "BBB"), tr(3, "CCC"))
	clk := clock.NewMutable(time.Date(2026, 11, 22, 4, 0, 0, 0, time.UTC))
	c := &countingStart{}
	s := newSupervisor(nil, 3, 10*time.Minute, 2, store, c.run, clk, supervisorTestLogger())
	defer s.stopAll()
	ctx := context.Background()
	step := func(after time.Duration, want map[string]bool, tracked ...tracking.Tracked) {
		t.Helper()
		clk.Advance(after)
		store.set(tracked...)
		if err := s.reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		got := s.runningSymbols()
		if len(got) > 3 {
			t.Fatalf("running %v: more than the cap of 3", got)
		}
		if len(got) != len(want) {
			t.Fatalf("running %v, want %v", got, want)
		}
		for sym := range want {
			if !got[sym] {
				t.Fatalf("running %v, want %v", got, want)
			}
		}
	}
	step(0, map[string]bool{"AAA": true, "BBB": true, "CCC": true}, tr(1, "AAA"), tr(2, "BBB"), tr(3, "CCC"))
	// Remove the three, one minute apart: all three stay in their grace.
	step(time.Minute, map[string]bool{"AAA": true, "BBB": true, "CCC": true}, tr(2, "BBB"), tr(3, "CCC"))
	step(time.Minute, map[string]bool{"AAA": true, "BBB": true, "CCC": true}, tr(3, "CCC"))
	step(time.Minute, map[string]bool{"AAA": true, "BBB": true, "CCC": true})
	// Add three new symbols within the grace, one at a time: each stops
	// the grace pipeline that left first.
	step(time.Minute, map[string]bool{"DDD": true, "BBB": true, "CCC": true}, tr(4, "DDD"))
	if st := store.state(1); st.State != tracking.Stopped || !strings.Contains(st.Reason, "cut short") {
		t.Fatalf("AAA = %+v, want stopped with its grace cut short", st)
	}
	step(time.Minute, map[string]bool{"DDD": true, "EEE": true, "CCC": true}, tr(4, "DDD"), tr(5, "EEE"))
	// An undo of the evicted AAA: it starts again (in CCC's slot).
	step(time.Minute, map[string]bool{"DDD": true, "EEE": true, "AAA": true}, tr(4, "DDD"), tr(5, "EEE"), tr(1, "AAA"))
	waitFor(t, func() bool { return c.count("AAA") == 2 })
	waitFor(t, func() bool { return c.count("DDD") == 1 && c.count("EEE") == 1 })
	if p := c.peakRunning(); p > 3 {
		t.Fatalf("at most %d pipelines ran at once, want never more than the cap of 3", p)
	}
}

// Architect review of 4.1a, item 4: a pipeline whose start function
// returned (it reported Failed) is not left in running forever; the next
// pass starts it again while its symbol is still wanted.
func TestSupervisor_APipelineThatEndedOnItsOwnIsStartedAgain(t *testing.T) {
	store := &fakeTracking{}
	store.set(tr(1, "BTC"), tr(2, "NEW"))
	clk := clock.NewMutable(time.Date(2026, 11, 22, 4, 0, 0, 0, time.UTC))
	c := &countingStart{failOnce: map[string]bool{"NEW": true}}
	s := newSupervisor(nil, 8, 10*time.Minute, 2, store, c.run, clk, supervisorTestLogger())
	defer s.stopAll()
	ctx := context.Background()
	if err := s.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return c.count("NEW") == 1 })
	// Let the failed pipeline's goroutine finish closing its done channel.
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		p, ok := s.running[2]
		if !ok {
			return true
		}
		select {
		case <-p.done:
			return true
		default:
			return false
		}
	})
	if err := s.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return c.count("NEW") == 2 })
	if !s.runningSymbols()["NEW"] || c.count("BTC") != 1 {
		t.Fatalf("running %v, BTC starts %d: want NEW running again and BTC untouched", s.runningSymbols(), c.count("BTC"))
	}
}

// Architect review of 4.1a, item 3: an empty listing, or one under half
// of the stored catalog, changes nothing and marks nothing.
func TestRefreshCatalog_AnEmptyOrUnderHalfListingChangesNothing(t *testing.T) {
	stored := []store.Instrument{
		{ID: 1, NativeSymbol: "BTC", PriceScale: 1, QtyScale: 5, Enabled: true},
		{ID: 2, NativeSymbol: "ETH", PriceScale: 2, QtyScale: 4, Enabled: true},
		{ID: 3, NativeSymbol: "SOL", PriceScale: 4, QtyScale: 2, Enabled: true},
		{ID: 4, NativeSymbol: "LINK", PriceScale: 5, QtyScale: 1, Enabled: true},
		{ID: 5, NativeSymbol: "AVAX", PriceScale: 4, QtyScale: 2, Enabled: true},
	}
	clk := clock.NewMutable(time.Date(2026, 11, 22, 4, 0, 0, 0, time.UTC))
	cases := map[string][]ingestion.ProviderInstrument{
		"empty": nil,
		// 2 of 5 stored is under half. LINK's precision differs and a new
		// symbol is listed: neither may be acted on.
		"under half": {perp("LINK", 2), perp("NEWCOIN", 2)},
	}
	for name, listing := range cases {
		t.Run(name, func(t *testing.T) {
			cs := &fakeCatalogStore{stored: stored}
			ins := &fakeInserter{}
			_, err := refreshCatalog(context.Background(), listing, ins, cs, clk, supervisorTestLogger())
			if !errors.Is(err, errCatalogListingTooSmall) {
				t.Fatalf("err = %v, want errCatalogListingTooSmall", err)
			}
			if len(cs.writes) != 0 || len(ins.inserted) != 0 {
				t.Fatalf("states written %+v, rows inserted %+v: want nothing changed", cs.writes, ins.inserted)
			}
		})
	}
	// Exactly half is accepted (the guard is "under half"): 3 of 5 listed,
	// the 2 unlisted enabled symbols are marked.
	cs := &fakeCatalogStore{stored: stored}
	if _, err := refreshCatalog(context.Background(), []ingestion.ProviderInstrument{perp("BTC", 5), perp("ETH", 4), perp("SOL", 2)}, &fakeInserter{}, cs, clk, supervisorTestLogger()); err != nil {
		t.Fatalf("a listing of 3 of 5 was refused: %v", err)
	}
	if cs.state(4).State != tracking.NeedsAttention || cs.state(5).State != tracking.NeedsAttention {
		t.Fatalf("states %+v, want LINK and AVAX marked when the listing is usable", cs.states)
	}
}

// Architect review of 4.1a, item 1: a supervised symbol's startup backfill
// retries up to NewSymbolBackfillRetryCap (about 5 minutes); the shared
// live feed cap (30 s) is used everywhere else.
func TestStartupBackfillOptions_RetryCapAndSlot(t *testing.T) {
	live := config.LiveFeed{ReconnectBackoffBase: time.Second, ReconnectBackoffCap: 30 * time.Second}
	cfg := config.Config{Live: live, Hyperliquid: config.Hyperliquid{NewSymbolBackfillRetryCap: 5 * time.Minute}}
	gate := make(chan struct{}, 2)
	supervised := startupBackfillOptions(cfg, symbolHooks{gate: gate})
	if supervised.slot != gate || supervised.retryCap != 5*time.Minute {
		t.Fatalf("supervised options: slot %v, cap %s; want the gate and 5m", supervised.slot, supervised.retryCap)
	}
	if plain := startupBackfillOptions(cfg, symbolHooks{}); plain.slot != nil || plain.retryCap != 0 {
		t.Fatalf("unsupervised options: slot %v, cap %s; want none (the shared cap)", plain.slot, plain.retryCap)
	}
	var longest, longestShared time.Duration
	for i := 0; i < 400; i++ {
		longest = max(longest, supervised.retryDelay(20, live))
		longestShared = max(longestShared, backfillOptions{}.retryDelay(20, live))
	}
	if longest <= 30*time.Second || longest > 5*time.Minute {
		t.Fatalf("longest supervised delay %s, want past 30s and at most 5m", longest)
	}
	if longestShared > 30*time.Second {
		t.Fatalf("longest shared delay %s, want the shared 30s cap", longestShared)
	}
}

// The slot is given back after a failed attempt (not held into the retry
// delay) and taken again for the next one.
func TestBackfillOptions_SlotIsHeldPerAttemptOnly(t *testing.T) {
	gate := make(chan struct{}, 1)
	opt := backfillOptions{slot: gate}
	if !opt.acquire(context.Background()) || len(gate) != 1 {
		t.Fatal("acquire did not take the slot")
	}
	opt.release()
	if len(gate) != 0 {
		t.Fatal("release did not give the slot back")
	}
	gate <- struct{}{} // someone else holds it
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if opt.acquire(ctx) {
		t.Fatal("acquire succeeded with the slot taken and ctx done")
	}
}
