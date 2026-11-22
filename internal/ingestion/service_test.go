package ingestion

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
)

// discardLogger is a *slog.Logger that writes nowhere, used so test
// output isn't flooded by this package's deliberate reject-and-log
// behavior.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, nil))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// fakeCandleStore is a handwritten fake for candleStore (guide 4, 10):
// an in-memory map keyed by (instrumentID, openTime), supporting exactly
// the state transitions the real schema enforces (forming vs. closed,
// supersede replaces the current row), so applyCandle's branching can be
// tested without a database.
type fakeCandleStore struct {
	byKey  map[fakeCandleKey]marketdata.Candle
	gaps   []fakeGap
	quote  *marketdata.BidAsk
	nextID int64
}

type fakeCandleKey struct {
	InstrumentID int64
	OpenTime     time.Time
}

type fakeGap struct {
	InstrumentID int64
	Start, End   time.Time
}

func newFakeCandleStore() *fakeCandleStore {
	return &fakeCandleStore{byKey: map[fakeCandleKey]marketdata.Candle{}}
}

func (f *fakeCandleStore) key(instrumentID int64, openTime clock.EventTime) fakeCandleKey {
	return fakeCandleKey{InstrumentID: instrumentID, OpenTime: openTime.Time()}
}

func (f *fakeCandleStore) GetCurrent(_ context.Context, instrumentID int64, openTime clock.EventTime) (marketdata.Candle, error) {
	c, ok := f.byKey[f.key(instrumentID, openTime)]
	if !ok {
		return marketdata.Candle{}, marketdata.ErrCandleNotFound
	}
	return c, nil
}

func (f *fakeCandleStore) UpsertForming(_ context.Context, t marketdata.Tick) (marketdata.Candle, error) {
	key := f.key(t.InstrumentID, t.OpenTime)
	existing, ok := f.byKey[key]
	if ok && existing.IsClosed {
		// Mirrors marketdata.Store: a forming value never overwrites a close.
		return marketdata.Candle{}, marketdata.ErrCandleAlreadyClosed
	}
	id := existing.ID
	if !ok {
		f.nextID++
		id = f.nextID
	}
	c := marketdata.Candle{
		ID: id, InstrumentID: t.InstrumentID, OpenTime: t.OpenTime,
		Open: t.Open, High: t.High, Low: t.Low, Close: t.Close, Volume: t.Volume,
		Revision: 1, IsClosed: false, TickAligned: t.TickAligned,
		Provider: t.Provider, IngestedAt: t.IngestedAt,
	}
	f.byKey[key] = c
	return c, nil
}

// CloseWith mirrors marketdata.Store.CloseWith: freeze with t's values.
func (f *fakeCandleStore) CloseWith(_ context.Context, t marketdata.Tick) (marketdata.Candle, error) {
	key := f.key(t.InstrumentID, t.OpenTime)
	c, ok := f.byKey[key]
	if !ok {
		return marketdata.Candle{}, marketdata.ErrCandleNotFound
	}
	if c.IsClosed {
		return marketdata.Candle{}, marketdata.ErrCandleAlreadyClosed
	}
	c.Open, c.High, c.Low, c.Close, c.Volume = t.Open, t.High, t.Low, t.Close, t.Volume
	c.TickAligned, c.Provider, c.IngestedAt = t.TickAligned, t.Provider, t.IngestedAt
	c.IsClosed = true
	f.byKey[key] = c
	return c, nil
}

func (f *fakeCandleStore) SupersedeCandle(_ context.Context, instrumentID int64, openTime clock.EventTime, corr marketdata.Correction) (marketdata.Candle, error) {
	key := f.key(instrumentID, openTime)
	old, ok := f.byKey[key]
	if !ok {
		return marketdata.Candle{}, marketdata.ErrCandleNotFound
	}
	f.nextID++
	c := marketdata.Candle{
		ID: f.nextID, InstrumentID: instrumentID, OpenTime: openTime,
		Open: corr.Open, High: corr.High, Low: corr.Low, Close: corr.Close, Volume: corr.Volume,
		Revision: old.Revision + 1, IsClosed: true, TickAligned: corr.TickAligned,
		Provider: corr.Provider, IngestedAt: corr.IngestedAt,
	}
	f.byKey[key] = c
	return c, nil
}

func (f *fakeCandleStore) RecordGap(_ context.Context, instrumentID int64, start, end clock.EventTime) (marketdata.Gap, error) {
	f.gaps = append(f.gaps, fakeGap{InstrumentID: instrumentID, Start: start.Time(), End: end.Time()})
	return marketdata.Gap{InstrumentID: instrumentID, Start: start, End: end}, nil
}

// ListClosedInRangeLimited mirrors marketdata.Store: current closed
// candles with open_time in [start, end), oldest first.
func (f *fakeCandleStore) ListClosedInRangeLimited(_ context.Context, instrumentID int64, start, end time.Time, limit int32) ([]marketdata.Candle, error) {
	var out []marketdata.Candle
	for k, c := range f.byKey {
		ot := k.OpenTime
		if k.InstrumentID == instrumentID && c.IsClosed && !ot.Before(start) && ot.Before(end) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OpenTime.Time().Before(out[j].OpenTime.Time()) })
	if int32(len(out)) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeCandleStore) UpsertQuote(_ context.Context, q marketdata.BidAsk) (marketdata.BidAsk, error) {
	if f.quote != nil && q.QuoteTime.Time().Before(f.quote.QuoteTime.Time()) {
		return marketdata.BidAsk{}, marketdata.ErrStaleQuote
	}
	f.quote = &q
	return q, nil
}

func testService(t *testing.T, store *fakeCandleStore, provider Provider, cal marketdata.Calendar) *Service {
	t.Helper()
	lookup := &fakeInstrumentLookup{byKey: map[string]instruments.Instrument{
		"fixture/BTCUSDT": fixtureInstrument(),
	}}
	cache := NewInstrumentCache(lookup)
	normalizer := NewNormalizer(cache, clock.Real{})
	svc := &Service{
		providerName: "fixture",
		provider:     provider,
		normalizer:   normalizer,
		instruments:  cache,
		store:        store,
		calendar:     cal,
		logger:       discardLogger(),
	}
	return svc
}

func normalizedCandle(openTime time.Time, open, high, low, closePrice int64, isFinal bool) NormalizedCandle {
	return NormalizedCandle{
		InstrumentID: 1,
		Provider:     "fixture",
		OpenTime:     clock.NewEventTime(openTime),
		Open:         quant.Price(open),
		High:         quant.Price(high),
		Low:          quant.Price(low),
		Close:        quant.Price(closePrice),
		Volume:       quant.Qty(10),
		TickAligned:  true,
		IngestedAt:   clock.NewIngestionTime(time.Now()),
		IsFinal:      isFinal,
	}
}

func TestApplyCandle_NewForming(t *testing.T) {
	store := newFakeCandleStore()
	svc := testService(t, store, nil, nil)
	openTime := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)

	nc := normalizedCandle(openTime, 100, 110, 95, 105, false)
	if err := svc.applyCandle(context.Background(), nc); err != nil {
		t.Fatalf("applyCandle: %v", err)
	}

	got, err := store.GetCurrent(context.Background(), 1, clock.NewEventTime(openTime))
	if err != nil {
		t.Fatalf("GetCurrent: %v", err)
	}
	if got.IsClosed {
		t.Error("candle is closed, want still forming (IsFinal was false)")
	}
	if got.Revision != 1 {
		t.Errorf("Revision = %d, want 1", got.Revision)
	}
}

func TestApplyCandle_NewAndImmediatelyFinal(t *testing.T) {
	store := newFakeCandleStore()
	svc := testService(t, store, nil, nil)
	openTime := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)

	nc := normalizedCandle(openTime, 100, 110, 95, 105, true)
	if err := svc.applyCandle(context.Background(), nc); err != nil {
		t.Fatalf("applyCandle: %v", err)
	}

	got, err := store.GetCurrent(context.Background(), 1, clock.NewEventTime(openTime))
	if err != nil {
		t.Fatalf("GetCurrent: %v", err)
	}
	if !got.IsClosed {
		t.Error("candle is not closed, want closed: IsFinal was true even on the first message (this slice's IsFinal policy: trust it immediately)")
	}
}

func TestApplyCandle_OverwritesStillForming(t *testing.T) {
	store := newFakeCandleStore()
	svc := testService(t, store, nil, nil)
	openTime := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)

	if err := svc.applyCandle(context.Background(), normalizedCandle(openTime, 100, 105, 95, 100, false)); err != nil {
		t.Fatalf("first applyCandle: %v", err)
	}
	if err := svc.applyCandle(context.Background(), normalizedCandle(openTime, 100, 120, 95, 115, false)); err != nil {
		t.Fatalf("second applyCandle: %v", err)
	}

	got, err := store.GetCurrent(context.Background(), 1, clock.NewEventTime(openTime))
	if err != nil {
		t.Fatalf("GetCurrent: %v", err)
	}
	if got.Revision != 1 || got.High != 120 {
		t.Fatalf("got %+v, want revision 1 with the overwritten High=120 (a tick must never create a revision)", got)
	}
}

func TestApplyCandle_DuplicateFinalIsANoOp(t *testing.T) {
	store := newFakeCandleStore()
	svc := testService(t, store, nil, nil)
	openTime := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)

	nc := normalizedCandle(openTime, 100, 110, 95, 105, true)
	if err := svc.applyCandle(context.Background(), nc); err != nil {
		t.Fatalf("first applyCandle: %v", err)
	}
	before, _ := store.GetCurrent(context.Background(), 1, clock.NewEventTime(openTime))

	// Identical redelivery of the same final values.
	if err := svc.applyCandle(context.Background(), nc); err != nil {
		t.Fatalf("duplicate applyCandle: %v", err)
	}
	after, _ := store.GetCurrent(context.Background(), 1, clock.NewEventTime(openTime))

	if after.ID != before.ID || after.Revision != before.Revision {
		t.Fatalf("a duplicate redelivery changed the stored candle: before=%+v after=%+v, want no change", before, after)
	}
}

func TestApplyCandle_DifferentFinalValuesIsACorrection(t *testing.T) {
	store := newFakeCandleStore()
	svc := testService(t, store, nil, nil)
	openTime := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)

	if err := svc.applyCandle(context.Background(), normalizedCandle(openTime, 100, 110, 95, 105, true)); err != nil {
		t.Fatalf("first applyCandle: %v", err)
	}
	if err := svc.applyCandle(context.Background(), normalizedCandle(openTime, 100, 130, 90, 120, true)); err != nil {
		t.Fatalf("correction applyCandle: %v", err)
	}

	got, err := store.GetCurrent(context.Background(), 1, clock.NewEventTime(openTime))
	if err != nil {
		t.Fatalf("GetCurrent: %v", err)
	}
	if got.Revision != 2 || got.High != 130 {
		t.Fatalf("got %+v, want revision 2 with the corrected High=130", got)
	}
}

// TestApplyCandle_CorrectionLogsAWarning proves applyCandle's only
// mitigation for the out-of-order-supersede gap (see Service's and
// applyCandle's doc comments) actually fires: every call to
// SupersedeCandle must be preceded by a WARN-level log entry, so an
// operator has a trace to notice a correction that turns out to be a
// stale, reordered message rather than a genuine one.
func TestApplyCandle_CorrectionLogsAWarning(t *testing.T) {
	store := newFakeCandleStore()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	lookup := &fakeInstrumentLookup{byKey: map[string]instruments.Instrument{
		"fixture/BTCUSDT": fixtureInstrument(),
	}}
	cache := NewInstrumentCache(lookup)
	svc := &Service{
		providerName: "fixture",
		normalizer:   NewNormalizer(cache, clock.Real{}),
		instruments:  cache,
		store:        store,
		logger:       logger,
	}
	openTime := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)

	if err := svc.applyCandle(context.Background(), normalizedCandle(openTime, 100, 110, 95, 105, true)); err != nil {
		t.Fatalf("first applyCandle: %v", err)
	}
	buf.Reset() // only interested in what the correction itself logs

	if err := svc.applyCandle(context.Background(), normalizedCandle(openTime, 100, 130, 90, 120, true)); err != nil {
		t.Fatalf("correction applyCandle: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "level=WARN") {
		t.Fatalf("correction did not log at WARN level, got: %s", out)
	}
	if !strings.Contains(out, "superseding closed candle") {
		t.Fatalf("WARN log did not mention the supersede, got: %s", out)
	}
}

func TestDetectAndRecordGaps_ConsolidatesConsecutiveMissingMinutes(t *testing.T) {
	store := newFakeCandleStore()
	svc := testService(t, store, nil, fakeCalendar{status: marketdata.MarketStatusOpen})

	start := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)
	present := minutes(start, start.Add(1*time.Minute), start.Add(4*time.Minute)) // 12:02, 12:03 missing

	if err := svc.detectAndRecordGaps(context.Background(), 1, "crypto_perp", present, start, end); err != nil {
		t.Fatalf("detectAndRecordGaps: %v", err)
	}

	if len(store.gaps) != 1 {
		t.Fatalf("recorded %d gaps, want exactly 1 consolidated range: %+v", len(store.gaps), store.gaps)
	}
	wantStart := start.Add(2 * time.Minute)
	wantEnd := start.Add(4 * time.Minute)
	if !store.gaps[0].Start.Equal(wantStart) || !store.gaps[0].End.Equal(wantEnd) {
		t.Fatalf("gap = [%s,%s), want [%s,%s)", store.gaps[0].Start, store.gaps[0].End, wantStart, wantEnd)
	}
}

func TestDetectAndRecordGaps_ScheduledClosureIsNeverAGap(t *testing.T) {
	store := newFakeCandleStore()
	svc := testService(t, store, nil, fakeCalendar{status: marketdata.MarketStatusScheduledClosure})

	start := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	end := start.Add(3 * time.Minute)
	present := minutes() // nothing present; calendar says closed for all of it

	if err := svc.detectAndRecordGaps(context.Background(), 1, "forex", present, start, end); err != nil {
		t.Fatalf("detectAndRecordGaps: %v", err)
	}
	if len(store.gaps) != 0 {
		t.Fatalf("recorded %d gaps, want 0: a scheduled closure must never be reported as a gap (ADR-006)", len(store.gaps))
	}
}

func TestDetectAndRecordGaps_InconclusiveCalendarStillRecordsAGap(t *testing.T) {
	store := newFakeCandleStore()
	svc := testService(t, store, nil, fakeCalendar{err: errors.New("not implemented")})

	start := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	end := start.Add(1 * time.Minute)

	if err := svc.detectAndRecordGaps(context.Background(), 1, "forex", minutes(), start, end); err != nil {
		t.Fatalf("detectAndRecordGaps: %v", err)
	}
	if len(store.gaps) != 1 {
		t.Fatalf("recorded %d gaps, want 1: a calendar error must not suppress a candidate gap (under-reporting is worse than a false positive)", len(store.gaps))
	}
}

func TestDetectAndRecordGaps_ClosureInterruptsAGapRange(t *testing.T) {
	// Minutes: [missing][scheduled closure][missing] -- must produce two
	// separate gap ranges, not one spanning the closure.
	store := newFakeCandleStore()
	start := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	closureMinute := start.Add(1 * time.Minute)

	cal := closureAtCalendar{closure: closureMinute}
	svc := testService(t, store, nil, cal)

	end := start.Add(3 * time.Minute)
	if err := svc.detectAndRecordGaps(context.Background(), 1, "forex", minutes(), start, end); err != nil {
		t.Fatalf("detectAndRecordGaps: %v", err)
	}
	if len(store.gaps) != 2 {
		t.Fatalf("recorded %d gaps, want 2 (split by the scheduled closure in the middle): %+v", len(store.gaps), store.gaps)
	}
}

// closureAtCalendar reports MarketStatusScheduledClosure only at one
// exact instant, Open otherwise -- used to prove a closure in the middle
// of a missing range splits it into two gaps rather than being absorbed.
type closureAtCalendar struct{ closure time.Time }

func (c closureAtCalendar) Status(_ context.Context, _ string, t clock.EventTime) (marketdata.MarketStatus, error) {
	if t.Time().Equal(c.closure) {
		return marketdata.MarketStatusScheduledClosure, nil
	}
	return marketdata.MarketStatusOpen, nil
}

func TestHandleLiveQuote_StaleQuoteDoesNotPropagateAsError(t *testing.T) {
	store := newFakeCandleStore()
	svc := testService(t, store, nil, nil)

	newer := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	older := newer.Add(-time.Minute)

	if err := svc.handleLiveQuote(context.Background(), ProviderQuote{
		Symbol: "BTCUSDT", Bid: "100.00", Ask: "101.00", QuoteTime: newer,
	}); err != nil {
		t.Fatalf("handleLiveQuote (newer): %v", err)
	}

	err := svc.handleLiveQuote(context.Background(), ProviderQuote{
		Symbol: "BTCUSDT", Bid: "1.00", Ask: "2.00", QuoteTime: older,
	})
	if err != nil {
		t.Fatalf("handleLiveQuote (stale): err = %v, want nil (a stale quote is an expected outcome, not a stream-stopping failure)", err)
	}
}

func TestHandleLiveCandle_RejectedCandleDoesNotPropagateAsError(t *testing.T) {
	store := newFakeCandleStore()
	svc := testService(t, store, nil, nil)

	err := svc.handleLiveCandle(context.Background(), "BTCUSDT", ProviderCandle{
		Symbol:   "BTCUSDT",
		OpenTime: time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC),
		Open:     "not-a-number", High: "1", Low: "1", Close: "1", Volume: "1",
	})
	if err != nil {
		t.Fatalf("handleLiveCandle with a malformed candle: err = %v, want nil (reject-and-log, not stream-stopping)", err)
	}
	if len(store.byKey) != 0 {
		t.Fatalf("a rejected candle was still written to the store: %+v", store.byKey)
	}
}

// minutes is the fetched-minutes set detectAndRecordGaps takes, keyed by
// the UTC instant in nanoseconds.
func minutes(ts ...time.Time) map[int64]bool {
	m := make(map[int64]bool, len(ts))
	for _, t := range ts {
		m[t.UTC().UnixNano()] = true
	}
	return m
}

func everyMinute(from time.Time, n int) map[int64]bool {
	m := make(map[int64]bool, n)
	for i := 0; i < n; i++ {
		m[from.Add(time.Duration(i)*time.Minute).UTC().UnixNano()] = true
	}
	return m
}

// The real-run shape (gap-recording fix): the periodic backfill fetches
// [now-5m, now) with an unaligned now, every minute. Every closed minute
// in it was returned, so nothing is missing. Before the fix the walk
// started at the unaligned now-5m, no slot fell on a minute, and the
// whole window was recorded as a gap on every tick.
func TestDetectAndRecordGaps_UnalignedPeriodicWindowWithEveryMinutePresentRecordsNothing(t *testing.T) {
	store := newFakeCandleStore()
	svc := testService(t, store, nil, fakeCalendar{status: marketdata.MarketStatusOpen})
	now := time.Date(2026, time.November, 22, 1, 48, 23, 922440000, time.UTC)
	start := now.Add(-5 * time.Minute) // 01:43:23.92244, the stored row's own start
	// The fetch returned 01:43 (whose open is before start) to the forming 01:48.
	present := everyMinute(time.Date(2026, time.November, 22, 1, 43, 0, 0, time.UTC), 6)

	if err := svc.detectAndRecordGaps(context.Background(), 1, "crypto_perp", present, start, now); err != nil {
		t.Fatal(err)
	}
	if len(store.gaps) != 0 {
		t.Fatalf("recorded %+v, want no gap: every closed minute of the window was fetched", store.gaps)
	}
}

// The minute still forming is never a gap, even when the fetch did not
// return it: it has not closed, so it cannot be missing yet.
func TestDetectAndRecordGaps_FormingMinuteIsNeverAGap(t *testing.T) {
	store := newFakeCandleStore()
	svc := testService(t, store, nil, fakeCalendar{status: marketdata.MarketStatusOpen})
	start := time.Date(2026, time.November, 22, 1, 43, 0, 0, time.UTC)
	now := start.Add(5*time.Minute + 20*time.Second) // 01:48:20: 01:48 is forming
	present := everyMinute(start, 5)                 // 01:43-01:47, the forming 01:48 not returned

	if err := svc.detectAndRecordGaps(context.Background(), 1, "crypto_perp", present, start, now); err != nil {
		t.Fatal(err)
	}
	if len(store.gaps) != 0 {
		t.Fatalf("recorded %+v, want none: 01:48 is still forming", store.gaps)
	}
}

// A real hole inside an unaligned window is still recorded, on whole
// minutes, and only the closed minutes of the window are examined.
func TestDetectAndRecordGaps_RealHoleInUnalignedWindowIsRecordedOnMinuteBounds(t *testing.T) {
	store := newFakeCandleStore()
	svc := testService(t, store, nil, fakeCalendar{status: marketdata.MarketStatusOpen})
	m := func(i int) time.Time { return time.Date(2026, time.November, 22, 1, 40+i, 0, 0, time.UTC) }
	start := m(0).Add(30 * time.Second)                    // 01:40:30
	now := m(6).Add(30 * time.Second)                      // 01:46:30, 01:46 forming
	present := minutes(m(0), m(1), m(2), m(4), m(5), m(6)) // 01:43 missing

	if err := svc.detectAndRecordGaps(context.Background(), 1, "crypto_perp", present, start, now); err != nil {
		t.Fatal(err)
	}
	if len(store.gaps) != 1 || !store.gaps[0].Start.Equal(m(3)) || !store.gaps[0].End.Equal(m(4)) {
		t.Fatalf("recorded %+v, want exactly [01:43, 01:44)", store.gaps)
	}
	for _, g := range store.gaps {
		if !g.Start.Equal(g.Start.Truncate(time.Minute)) || !g.End.Equal(g.End.Truncate(time.Minute)) {
			t.Fatalf("gap %+v is not on minute bounds", g)
		}
	}
}

// A minute the fetch did not return but the live stream already closed
// (stored closed) is not missing.
func TestDetectAndRecordGaps_MinuteClosedByTheStreamIsNotAGap(t *testing.T) {
	store := newFakeCandleStore()
	svc := testService(t, store, nil, fakeCalendar{status: marketdata.MarketStatusOpen})
	start := time.Date(2026, time.November, 22, 1, 40, 0, 0, time.UTC)
	closedByStream := start.Add(2 * time.Minute)
	store.byKey[store.key(1, clock.NewEventTime(closedByStream))] = marketdata.Candle{
		InstrumentID: 1, OpenTime: clock.NewEventTime(closedByStream), IsClosed: true, Revision: 1,
	}
	present := minutes(start, start.Add(time.Minute), start.Add(3*time.Minute))

	if err := svc.detectAndRecordGaps(context.Background(), 1, "crypto_perp", present, start, start.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(store.gaps) != 0 {
		t.Fatalf("recorded %+v, want none: 01:42 is stored closed", store.gaps)
	}
}

// historicalOnlyProvider serves HistoricalCandles from a fixed list; the
// rest of Provider is unused here.
type historicalOnlyProvider struct{ candles []ProviderCandle }

func (p historicalOnlyProvider) Instruments(context.Context) ([]ProviderInstrument, error) {
	return nil, nil
}
func (p historicalOnlyProvider) HistoricalCandles(context.Context, string, time.Time, time.Time) ([]ProviderCandle, error) {
	return p.candles, nil
}
func (p historicalOnlyProvider) StreamCandles(ctx context.Context, _ string, _ func(context.Context, ProviderCandle) error) error {
	<-ctx.Done()
	return nil
}
func (p historicalOnlyProvider) StreamQuotes(ctx context.Context, _ string, _ func(context.Context, ProviderQuote) error) error {
	<-ctx.Done()
	return nil
}

// CLAUDE.md check 1, through the whole IngestHistorical path: a window
// given in America/New_York across the November 1 2026 DST fall-back
// (01:00-02:00 local happens twice), with every minute returned in UTC.
// Before the fix the fetched set was keyed by time.Time, whose map key
// includes the location, so not one New York slot matched a UTC open time
// and every minute was recorded as missing.
func TestIngestHistorical_NonUTCWindowAcrossDST_RecordsNoFalseGap(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata")
	}
	// 05:50 UTC = 01:50 EDT; 06:10 UTC = 01:10 EST (after the fall-back).
	startUTC := time.Date(2026, time.November, 1, 5, 50, 0, 0, time.UTC)
	start := startUTC.In(ny)
	end := startUTC.Add(20 * time.Minute).In(ny)
	var raw []ProviderCandle
	for i := 0; i <= 20; i++ { // through the forming minute, like the real adapter
		raw = append(raw, ProviderCandle{
			Symbol: "BTCUSDT", OpenTime: startUTC.Add(time.Duration(i) * time.Minute),
			Open: "100.00", High: "100.00", Low: "100.00", Close: "100.00", Volume: "1", IsFinal: i < 20,
		})
	}
	store := newFakeCandleStore()
	svc := testService(t, store, historicalOnlyProvider{candles: raw}, fakeCalendar{status: marketdata.MarketStatusOpen})

	if err := svc.IngestHistorical(context.Background(), "BTCUSDT", start, end); err != nil {
		t.Fatal(err)
	}
	if len(store.gaps) != 0 {
		t.Fatalf("recorded %d gaps (first %+v), want none: every minute was fetched", len(store.gaps), store.gaps[0])
	}
}

func TestClosedMinuteBounds(t *testing.T) {
	at := func(h, m, s int) time.Time { return time.Date(2026, time.November, 22, h, m, s, 0, time.UTC) }
	cases := []struct {
		start, end, from, to time.Time
	}{
		{at(1, 43, 23), at(1, 48, 23), at(1, 44, 0), at(1, 48, 0)}, // unaligned both ends
		{at(1, 43, 0), at(1, 48, 0), at(1, 43, 0), at(1, 48, 0)},   // aligned: unchanged
		{at(1, 48, 1), at(1, 48, 59), at(1, 49, 0), at(1, 48, 0)},  // inside one minute: empty
	}
	for _, c := range cases {
		from, to := closedMinuteBounds(c.start, c.end)
		if !from.Equal(c.from) || !to.Equal(c.to) {
			t.Errorf("closedMinuteBounds(%s, %s) = [%s, %s), want [%s, %s)", c.start, c.end, from, to, c.from, c.to)
		}
	}
}
