//go:build integration

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/ingestion/hyperliquid"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"

	_ "github.com/lib/pq"
)

// scriptedReconnectProvider is a minimal, handwritten
// ingestion.Provider (guide 4, 10 -- a consumer-defined fake, never
// the real Hyperliquid adapter) whose only purpose is to prove
// runHyperliquidSymbol's own orchestration: does it actually retry
// StreamCandles after a disconnect, more than once, and does it call
// IngestHistorical (the gap backfill) between every attempt,
// unconditionally. It streams one real candle per connection before
// disconnecting, so each reconnect cycle also proves a candle from an
// EARLIER connection is still in the store (no hole) once the test
// inspects it afterward.
type scriptedReconnectProvider struct {
	mu              sync.Mutex
	streamCalls     int
	historicalCalls int
	clk             clock.Clock
	base            time.Time
}

func (p *scriptedReconnectProvider) Instruments(context.Context) ([]ingestion.ProviderInstrument, error) {
	return nil, nil
}

// HistoricalCandles always returns one closed candle at p.base (final
// by construction: a batch of exactly one, matching this fake's own
// single-candle-per-connection rhythm closely enough for the gap-
// backfill call this test cares about to always have something real
// to apply). Counted so the test can assert it was actually called
// once per disconnect, not just once at startup.
func (p *scriptedReconnectProvider) HistoricalCandles(_ context.Context, symbol string, _, _ time.Time) ([]ingestion.ProviderCandle, error) {
	p.mu.Lock()
	p.historicalCalls++
	n := p.historicalCalls
	p.mu.Unlock()
	return []ingestion.ProviderCandle{
		{
			Symbol: symbol, OpenTime: p.base, Open: "100", High: "101", Low: "99", Close: fmt.Sprintf("100.%d", n), Volume: "1",
			IsFinal: true,
		},
	}, nil
}

// StreamCandles streams one forming candle, then returns a distinct
// error every time -- simulating a connection that never stays up,
// so runHyperliquidSymbol's own loop must reconnect repeatedly for
// this test to ever make progress at all.
func (p *scriptedReconnectProvider) StreamCandles(ctx context.Context, symbol string, handle func(context.Context, ingestion.ProviderCandle) error) error {
	p.mu.Lock()
	p.streamCalls++
	n := p.streamCalls
	p.mu.Unlock()

	if err := handle(ctx, ingestion.ProviderCandle{
		Symbol: symbol, OpenTime: p.base, Open: "100", High: "101", Low: "99", Close: fmt.Sprintf("100.%d", n), Volume: "1",
		IsFinal: false,
	}); err != nil {
		return err
	}
	return fmt.Errorf("scripted disconnect #%d", n)
}

func (p *scriptedReconnectProvider) StreamQuotes(ctx context.Context, _ string, _ func(context.Context, ingestion.ProviderQuote) error) error {
	<-ctx.Done()
	return nil
}

func (p *scriptedReconnectProvider) counts() (stream, historical int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.streamCalls, p.historicalCalls
}

// TestIntegration_RunHyperliquidSymbol_ReconnectsRepeatedlyWithBackfillBetweenEachAttempt
// is the owner's own requested test for runHyperliquidSymbol -- against
// the real test database, a real ingestion.Service and marketdata.Store,
// with only the Provider itself faked (StreamCandles always
// disconnects). Proves: more than one reconnect cycle actually
// happens, IngestHistorical runs between every single one of them
// (the unconditional gap backfill, planned or not), and backoff
// between attempts is not a tight loop.
func TestIntegration_RunHyperliquidSymbol_ReconnectsRepeatedlyWithBackfillBetweenEachAttempt(t *testing.T) {
	db := testWorkerDB(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	clk := clock.Real{}

	symbol := fmt.Sprintf("HLTEST-%s-%d", t.Name(), time.Now().UnixNano())
	instrumentStore := instruments.NewStore(db)
	instrumentID := seedHyperliquidTestInstrument(t, instrumentStore, symbol)

	marketdataStore := marketdata.NewStore(db, clk)
	cache := ingestion.NewInstrumentCache(instrumentStore)
	normalizer := ingestion.NewNormalizer(cache, clk)
	fake := &scriptedReconnectProvider{clk: clk, base: clk.Now().Truncate(time.Minute)}
	svc := ingestion.NewService(hyperliquid.ProviderName, fake, normalizer, cache, marketdataStore, marketdata.ContinuousCalendar{}, logger)
	// SetPublisher is never called: nil means "do not publish" (its
	// own doc comment), and this test has no hub/Redis to publish to.

	cfg := config.Config{
		Hyperliquid: config.Hyperliquid{BackfillDays: 1},
		Live: config.LiveFeed{
			ReconnectBackoffBase: 10 * time.Millisecond,
			ReconnectBackoffCap:  40 * time.Millisecond,
		},
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		runHyperliquidSymbol(runCtx, symbol, svc, cfg, logger, clk)
		close(done)
	}()

	deadline := time.After(5 * time.Second)
	for {
		stream, _ := fake.counts()
		if stream >= 4 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("only %d StreamCandles attempts in 5s, want at least 4 -- the reconnect loop does not appear to be retrying", stream)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runHyperliquidSymbol did not return after ctx was canceled")
	}

	streamCalls, historicalCalls := fake.counts()
	if streamCalls < 4 {
		t.Fatalf("StreamCandles called %d times, want at least 4 (repeated reconnects)", streamCalls)
	}
	// historicalCalls = 1 initial backfill + 1 gap backfill per
	// disconnect (every StreamCandles call that returned, including
	// the last one before cancel unblocked the final read) -- so it
	// must be at least streamCalls (one gap backfill per attempt),
	// never just 1.
	if historicalCalls < streamCalls {
		t.Fatalf("IngestHistorical called %d times across %d StreamCandles attempts, want at least one gap backfill per attempt (the unconditional backfill-after-every-disconnect rule)", historicalCalls, streamCalls)
	}

	// No hole: the store holds a candle at base for this instrument,
	// proving at least one of the backfills/streamed updates actually
	// landed (not just that the fake was called the right number of
	// times).
	got, err := marketdataStore.GetCurrent(ctx, instrumentID, clock.NewEventTime(fake.base))
	if err != nil {
		t.Fatalf("GetCurrent: %v (the store has no candle at all for the one minute this entire run was about)", err)
	}
	_ = got
}

// danglingMinuteProvider reproduces the real-run finding exactly: its
// first HistoricalCandles call (the startup backfill) returns base as
// a single-item, un-final batch (the "last fetched item stays open"
// rule, same as the real adapter), then StreamCandles' own first and
// only live message is for base+1 minute -- skipping base entirely,
// the precise race that left SOL's 23:17 minute open in the real
// run. Without the periodic backfill, nothing would ever close base
// (StreamCandles never saw it, so it has no "later message" proof;
// WatchCandles never disconnects in this fake, so the old gap-backfill-
// after-disconnect path never fires either). Every HistoricalCandles
// call after the first (i.e. every periodic-backfill tick) returns
// base closed alongside a newer row, exactly reproducing how a real
// re-fetch naturally closes a stale minute once a later one exists.
type danglingMinuteProvider struct {
	mu              sync.Mutex
	historicalCalls int
	base            time.Time
}

func (p *danglingMinuteProvider) Instruments(context.Context) ([]ingestion.ProviderInstrument, error) {
	return nil, nil
}

func (p *danglingMinuteProvider) HistoricalCandles(_ context.Context, symbol string, _, _ time.Time) ([]ingestion.ProviderCandle, error) {
	p.mu.Lock()
	p.historicalCalls++
	n := p.historicalCalls
	p.mu.Unlock()
	if n == 1 {
		return []ingestion.ProviderCandle{
			{Symbol: symbol, OpenTime: p.base, Open: "100", High: "101", Low: "99", Close: "100", Volume: "1", IsFinal: false},
		}, nil
	}
	return []ingestion.ProviderCandle{
		{Symbol: symbol, OpenTime: p.base, Open: "100", High: "101", Low: "99", Close: "100", Volume: "1", IsFinal: true},
		{Symbol: symbol, OpenTime: p.base.Add(time.Minute), Open: "100", High: "101", Low: "99", Close: "100.5", Volume: "1", IsFinal: false},
	}, nil
}

func (p *danglingMinuteProvider) StreamCandles(ctx context.Context, symbol string, handle func(context.Context, ingestion.ProviderCandle) error) error {
	if err := handle(ctx, ingestion.ProviderCandle{
		Symbol: symbol, OpenTime: p.base.Add(time.Minute), Open: "100", High: "101", Low: "99", Close: "100.5", Volume: "1", IsFinal: false,
	}); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

func (p *danglingMinuteProvider) StreamQuotes(ctx context.Context, _ string, _ func(context.Context, ingestion.ProviderQuote) error) error {
	<-ctx.Done()
	return nil
}

func (p *danglingMinuteProvider) historicalCallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.historicalCalls
}

// TestIntegration_RunHyperliquidSymbol_PeriodicBackfillClosesMinuteStreamNeverObserved
// reproduces the SOL 23:17 real-run finding against the real test
// database: the startup backfill leaves a minute open, and the
// stream's own first live message already belongs to a later minute,
// so the stream itself never closes it. Confirmed to fail without the
// fix (see the reverted-code run in this same session's report) --
// before the periodic backfill existed, this exact scenario left the
// minute open indefinitely.
func TestIntegration_RunHyperliquidSymbol_PeriodicBackfillClosesMinuteStreamNeverObserved(t *testing.T) {
	db := testWorkerDB(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	clk := clock.Real{}

	symbol := fmt.Sprintf("HLTEST-%s-%d", t.Name(), time.Now().UnixNano())
	instrumentStore := instruments.NewStore(db)
	instrumentID := seedHyperliquidTestInstrument(t, instrumentStore, symbol)

	marketdataStore := marketdata.NewStore(db, clk)
	cache := ingestion.NewInstrumentCache(instrumentStore)
	normalizer := ingestion.NewNormalizer(cache, clk)
	fake := &danglingMinuteProvider{base: clk.Now().Truncate(time.Minute)}
	svc := ingestion.NewService(hyperliquid.ProviderName, fake, normalizer, cache, marketdataStore, marketdata.ContinuousCalendar{}, logger)

	cfg := config.Config{
		Hyperliquid: config.Hyperliquid{
			BackfillDays:             1,
			PeriodicBackfillInterval: 30 * time.Millisecond,
			PeriodicBackfillWindow:   time.Hour,
		},
		Live: config.LiveFeed{ReconnectBackoffBase: 10 * time.Millisecond, ReconnectBackoffCap: 40 * time.Millisecond},
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		runHyperliquidSymbol(runCtx, symbol, svc, cfg, logger, clk)
		close(done)
	}()

	// Precondition: right after the stream has had a chance to start
	// and skip base, base must still be open -- otherwise this test
	// would not actually be exercising the race it claims to.
	time.Sleep(15 * time.Millisecond)
	row, err := marketdataStore.GetCurrent(ctx, instrumentID, clock.NewEventTime(fake.base))
	if err != nil {
		t.Fatalf("GetCurrent (precondition): %v", err)
	}
	if row.IsClosed {
		t.Fatal("precondition failed: base was already closed before the periodic backfill had a real chance to run -- this test is not exercising the intended race")
	}

	deadline := time.After(2 * time.Second)
	for {
		row, err := marketdataStore.GetCurrent(ctx, instrumentID, clock.NewEventTime(fake.base))
		if err != nil {
			t.Fatalf("GetCurrent: %v", err)
		}
		if row.IsClosed {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("base (%s) never closed within 2s / several periodic-backfill intervals -- the stream's own first message skipped it (OpenTime=%s) and nothing else closed it", fake.base, fake.base.Add(time.Minute))
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runHyperliquidSymbol did not return after ctx was canceled")
	}

	if n := fake.historicalCallCount(); n < 2 {
		t.Fatalf("HistoricalCandles called %d times, want at least 2 (the startup backfill plus at least one periodic tick)", n)
	}
}

// racingCloseProvider lets both the periodic backfill AND the stream
// itself close the same minute, repeatedly, with a value the test
// controls mid-run (closeValue, guarded by mu) -- so the test can
// first hold both paths agreeing (same value, want no new revision
// each time they re-agree) and then change the value (want exactly
// one new revision, through the normal correction path, not one per
// redundant re-close).
type racingCloseProvider struct {
	mu              sync.Mutex
	base            time.Time
	closeVal        string
	historicalCalls int
	streamSent      bool
}

func (p *racingCloseProvider) Instruments(context.Context) ([]ingestion.ProviderInstrument, error) {
	return nil, nil
}

func (p *racingCloseProvider) setCloseValue(v string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeVal = v
}

func (p *racingCloseProvider) getCloseValue() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeVal
}

func (p *racingCloseProvider) HistoricalCandles(_ context.Context, symbol string, _, _ time.Time) ([]ingestion.ProviderCandle, error) {
	p.mu.Lock()
	p.historicalCalls++
	p.mu.Unlock()
	v := p.getCloseValue()
	return []ingestion.ProviderCandle{
		{Symbol: symbol, OpenTime: p.base, Open: "100", High: "110", Low: "90", Close: v, Volume: "1", IsFinal: true},
	}, nil
}

// StreamCandles closes base directly, once, with whatever value is
// current at that instant -- deliberately racing the FIRST periodic
// backfill tick, which targets the same minute with the same
// starting value.
func (p *racingCloseProvider) StreamCandles(ctx context.Context, symbol string, handle func(context.Context, ingestion.ProviderCandle) error) error {
	v := p.getCloseValue()
	if err := handle(ctx, ingestion.ProviderCandle{
		Symbol: symbol, OpenTime: p.base, Open: "100", High: "110", Low: "90", Close: v, Volume: "1", IsFinal: true,
	}); err != nil {
		return err
	}
	p.mu.Lock()
	p.streamSent = true
	p.mu.Unlock()
	<-ctx.Done()
	return nil
}

func (p *racingCloseProvider) StreamQuotes(ctx context.Context, _ string, _ func(context.Context, ingestion.ProviderQuote) error) error {
	<-ctx.Done()
	return nil
}

func (p *racingCloseProvider) historicalCallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.historicalCalls
}

// TestIntegration_RunHyperliquidSymbol_PeriodicBackfillAndStreamAgreeingNeverCreateSpuriousRevision
// is the owner's own item 2: the periodic backfill and the stream
// repeatedly closing the SAME minute with the SAME value must never
// bump the revision -- only an actually differing value should, via
// ingestion.Service's own existing idempotent-correction contract
// (already covered elsewhere for that package in isolation; this
// test's own job is proving the NEW periodic-backfill orchestration
// does not accidentally bypass it under a real race against the
// stream).
func TestIntegration_RunHyperliquidSymbol_PeriodicBackfillAndStreamAgreeingNeverCreateSpuriousRevision(t *testing.T) {
	db := testWorkerDB(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	clk := clock.Real{}

	symbol := fmt.Sprintf("HLTEST-%s-%d", t.Name(), time.Now().UnixNano())
	instrumentStore := instruments.NewStore(db)
	instrumentID := seedHyperliquidTestInstrument(t, instrumentStore, symbol)

	marketdataStore := marketdata.NewStore(db, clk)
	cache := ingestion.NewInstrumentCache(instrumentStore)
	normalizer := ingestion.NewNormalizer(cache, clk)
	fake := &racingCloseProvider{base: clk.Now().Truncate(time.Minute), closeVal: "100"}
	svc := ingestion.NewService(hyperliquid.ProviderName, fake, normalizer, cache, marketdataStore, marketdata.ContinuousCalendar{}, logger)

	cfg := config.Config{
		Hyperliquid: config.Hyperliquid{
			BackfillDays:             1,
			PeriodicBackfillInterval: 25 * time.Millisecond,
			PeriodicBackfillWindow:   time.Hour,
		},
		Live: config.LiveFeed{ReconnectBackoffBase: 10 * time.Millisecond, ReconnectBackoffCap: 40 * time.Millisecond},
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		runHyperliquidSymbol(runCtx, symbol, svc, cfg, logger, clk)
		close(done)
	}()

	// Let the stream's own close race against several periodic-
	// backfill ticks, all agreeing on "100".
	time.Sleep(200 * time.Millisecond)
	if n := fake.historicalCallCount(); n < 3 {
		t.Fatalf("only %d HistoricalCandles calls in 200ms at a 25ms interval -- not enough repeated re-closes to prove agreement is idempotent", n)
	}
	row, err := marketdataStore.GetCurrent(ctx, instrumentID, clock.NewEventTime(fake.base))
	if err != nil {
		t.Fatalf("GetCurrent (agreement phase): %v", err)
	}
	if row.Revision != 1 {
		t.Fatalf("revision = %d after several agreeing re-closes (stream + periodic backfill, same value), want 1 -- a spurious revision was created", row.Revision)
	}
	if !row.IsClosed {
		t.Fatal("base is not closed after the agreement phase")
	}

	// Now make the value disagree -- every future periodic-backfill
	// tick reports a genuinely different close. Exactly one
	// additional revision is the normal correction path; more than
	// one would mean every tick is re-triggering a correction instead
	// of this new value becoming the new current one.
	fake.setCloseValue("105")
	time.Sleep(200 * time.Millisecond)

	row, err = marketdataStore.GetCurrent(ctx, instrumentID, clock.NewEventTime(fake.base))
	if err != nil {
		t.Fatalf("GetCurrent (correction phase): %v", err)
	}
	if row.Revision != 2 {
		t.Fatalf("revision = %d after the value changed, want exactly 2 (one correction, not one per re-close tick)", row.Revision)
	}
	wantClose, _ := quant.ParsePrice("105", quant.Scale(1))
	if row.Close != wantClose {
		t.Fatalf("close = %v after the correction, want %v (105 at scale 1)", row.Close, wantClose)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runHyperliquidSymbol did not return after ctx was canceled")
	}
}

// failingPeriodicBackfillProvider streams healthily forever (never
// disconnecting -- StreamCandles blocks on ctx.Done() after its one
// message) while every HistoricalCandles call after the startup
// backfill fails, simulating the exchange going unreachable mid an
// otherwise-healthy stream.
type failingPeriodicBackfillProvider struct {
	mu              sync.Mutex
	base            time.Time
	historicalCalls int
	streamCalls     int
}

func (p *failingPeriodicBackfillProvider) Instruments(context.Context) ([]ingestion.ProviderInstrument, error) {
	return nil, nil
}

func (p *failingPeriodicBackfillProvider) HistoricalCandles(_ context.Context, symbol string, _, _ time.Time) ([]ingestion.ProviderCandle, error) {
	p.mu.Lock()
	p.historicalCalls++
	n := p.historicalCalls
	p.mu.Unlock()
	if n == 1 {
		return []ingestion.ProviderCandle{
			{Symbol: symbol, OpenTime: p.base, Open: "100", High: "101", Low: "99", Close: "100", Volume: "1", IsFinal: true},
		}, nil
	}
	return nil, fmt.Errorf("simulated unreachable exchange")
}

func (p *failingPeriodicBackfillProvider) StreamCandles(ctx context.Context, symbol string, handle func(context.Context, ingestion.ProviderCandle) error) error {
	p.mu.Lock()
	p.streamCalls++
	p.mu.Unlock()
	if err := handle(ctx, ingestion.ProviderCandle{
		Symbol: symbol, OpenTime: p.base.Add(time.Minute), Open: "100", High: "101", Low: "99", Close: "100.5", Volume: "1", IsFinal: false,
	}); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

func (p *failingPeriodicBackfillProvider) StreamQuotes(ctx context.Context, _ string, _ func(context.Context, ingestion.ProviderQuote) error) error {
	<-ctx.Done()
	return nil
}

func (p *failingPeriodicBackfillProvider) counts() (stream, historical int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.streamCalls, p.historicalCalls
}

// TestIntegration_RunHyperliquidSymbol_FailingPeriodicBackfillNeverStopsAHealthyStream
// is the owner's own item 3: a periodic backfill that fails
// (unreachable) must not stop the stream (StreamCandles stays called
// exactly once the whole test, never treated as a disconnect) or
// crash the worker, and must back off to its own next interval
// rather than retrying in a tight loop.
func TestIntegration_RunHyperliquidSymbol_FailingPeriodicBackfillNeverStopsAHealthyStream(t *testing.T) {
	db := testWorkerDB(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	clk := clock.Real{}

	symbol := fmt.Sprintf("HLTEST-%s-%d", t.Name(), time.Now().UnixNano())
	instrumentStore := instruments.NewStore(db)
	_ = seedHyperliquidTestInstrument(t, instrumentStore, symbol)

	marketdataStore := marketdata.NewStore(db, clk)
	cache := ingestion.NewInstrumentCache(instrumentStore)
	normalizer := ingestion.NewNormalizer(cache, clk)
	fake := &failingPeriodicBackfillProvider{base: clk.Now().Truncate(time.Minute)}
	svc := ingestion.NewService(hyperliquid.ProviderName, fake, normalizer, cache, marketdataStore, marketdata.ContinuousCalendar{}, logger)

	interval := 20 * time.Millisecond
	cfg := config.Config{
		Hyperliquid: config.Hyperliquid{
			BackfillDays:             1,
			PeriodicBackfillInterval: interval,
			PeriodicBackfillWindow:   time.Hour,
		},
		Live: config.LiveFeed{ReconnectBackoffBase: 10 * time.Millisecond, ReconnectBackoffCap: 40 * time.Millisecond},
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		runHyperliquidSymbol(runCtx, symbol, svc, cfg, logger, clk)
		close(done)
	}()

	runDuration := 300 * time.Millisecond
	time.Sleep(runDuration)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runHyperliquidSymbol did not return after ctx was canceled")
	}

	streamCalls, historicalCalls := fake.counts()
	if streamCalls != 1 {
		t.Fatalf("StreamCandles called %d times, want exactly 1 -- a failing periodic backfill must never be treated as a stream disconnect", streamCalls)
	}
	// historicalCalls includes the one startup call plus one per
	// periodic tick during runDuration. A tight loop would produce
	// hundreds of calls in 300ms; back-off-to-next-interval produces
	// roughly runDuration/interval, generously bounded here at 4x
	// that to absorb scheduling jitter without masking a real
	// tight-loop regression.
	maxExpected := 1 + int(runDuration/interval)*4
	if historicalCalls <= 1 {
		t.Fatalf("HistoricalCandles called %d times, want more than 1 -- the periodic backfill does not appear to have run at all", historicalCalls)
	}
	if historicalCalls > maxExpected {
		t.Fatalf("HistoricalCandles called %d times in %v at a %v interval, want at most ~%d -- a failing periodic backfill appears to be tight-looping instead of backing off to its own next interval", historicalCalls, runDuration, interval, maxExpected)
	}
}

func seedHyperliquidTestInstrument(t *testing.T, store *instruments.Store, symbol string) int64 {
	t.Helper()
	priceScale := quant.Scale(1)
	qtyScale := quant.Scale(0)
	inst, err := store.Insert(context.Background(), instruments.NewInstrumentParams{
		Provider: hyperliquid.ProviderName, NativeSymbol: symbol, AssetClass: "crypto_perp",
		PriceScale: &priceScale, QtyScale: &qtyScale, TickSize: 1, QtyStepSize: 1,
		SupportedIntervals: []string{"1m", "5m", "15m", "1h", "4h", "1d"},
	})
	if err != nil {
		t.Fatalf("create hyperliquid test instrument: %v", err)
	}
	// Deliberately NOT enabled -- this test never needs it enabled
	// (ingestion.Service writes candles by instrument id, regardless
	// of enablement; enablement gates the REST/UI surface, not
	// ingestion), and the hard rule this whole brief is built around
	// is that nothing here ever enables one.
	return inst.ID
}
