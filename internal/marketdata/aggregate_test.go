package marketdata

import (
	"context"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/store"
)

// fakeCandleReader is a handwritten fake for candleReader (guide 10: no
// mock framework for pure logic). It answers ListCurrentCandlesInRange
// from an in-memory set of rows keyed by open_time.
type fakeCandleReader struct {
	rows map[time.Time]store.Candle
	err  error
	// calls counts ListCurrentCandlesInRange invocations -- used only by
	// TestAggregateRange_OneQueryRegardlessOfWindowCount (addendum item
	// B) to prove database call count does not grow with bar count.
	calls int
}

func newFakeCandleReader() *fakeCandleReader {
	return &fakeCandleReader{rows: map[time.Time]store.Candle{}}
}

func (f *fakeCandleReader) put(openTime time.Time, isClosed bool, open, high, low, closePrice, volume int64) {
	f.rows[openTime] = store.Candle{
		OpenTime: openTime,
		Open:     open,
		High:     high,
		Low:      low,
		Close:    closePrice,
		Volume:   volume,
		IsClosed: isClosed,
		// Revision 1 matches a real, never-corrected candle row's own
		// invariant (candles_revision_positive: revision >= 1) -- a test
		// that cares about a corrected row's own revision uses
		// putRevision below instead of overriding the zero value here.
		Revision: 1,
	}
}

// putRevision overrides an already-put row's own revision in place --
// used only by tests of the derived-candle-revision rule
// (adr-004-ws-contract-review.md, review item 4): a correction bumps a
// real candle's revision without changing anything else put already set.
func (f *fakeCandleReader) putRevision(openTime time.Time, revision int32) {
	row := f.rows[openTime]
	row.Revision = revision
	f.rows[openTime] = row
}

func (f *fakeCandleReader) ListCurrentCandlesInRange(_ context.Context, _ int64, start, end time.Time) ([]store.Candle, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	var out []store.Candle
	for t, row := range f.rows {
		if !t.Before(start) && t.Before(end) {
			out = append(out, row)
		}
	}
	return out, nil
}

func utc(y int, m time.Month, d, h, mi int) time.Time {
	return time.Date(y, m, d, h, mi, 0, 0, time.UTC)
}

func TestAggregate_Complete(t *testing.T) {
	r := newFakeCandleReader()
	base := utc(2026, time.October, 26, 18, 0)
	// 5 closed 1m candles: 18:00..18:04.
	r.put(base, true, 100, 110, 95, 105, 10)
	r.put(base.Add(1*time.Minute), true, 105, 120, 100, 115, 20)
	r.put(base.Add(2*time.Minute), true, 115, 118, 108, 112, 15)
	r.put(base.Add(3*time.Minute), true, 112, 130, 110, 125, 25)
	r.put(base.Add(4*time.Minute), true, 125, 127, 90, 99, 30)

	w := Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(5 * time.Minute))}
	agg, incomplete, err := Aggregate(context.Background(), r, 1, w)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if incomplete != nil {
		t.Fatalf("Aggregate reported incomplete unexpectedly: %+v", incomplete)
	}
	if agg == nil {
		t.Fatal("Aggregate returned nil result with no error and no incomplete")
	}

	// open = first candle's open, close = last candle's close.
	if agg.Open != 100 {
		t.Errorf("Open = %d, want 100", agg.Open)
	}
	if agg.Close != 99 {
		t.Errorf("Close = %d, want 99", agg.Close)
	}
	// high = max across all 5 candles' highs (130), low = min across all
	// 5 candles' lows (90).
	if agg.High != 130 {
		t.Errorf("High = %d, want 130", agg.High)
	}
	if agg.Low != 90 {
		t.Errorf("Low = %d, want 90", agg.Low)
	}
	// volume = sum of all 5: 10+20+15+25+30 = 100.
	if agg.Volume != 100 {
		t.Errorf("Volume = %d, want 100", agg.Volume)
	}
}

func TestAggregate_MissingMinute_ReturnsIncompleteNamingIt(t *testing.T) {
	r := newFakeCandleReader()
	base := utc(2026, time.October, 26, 18, 0)
	// Only 18:00, 18:01, 18:03, 18:04 present -- 18:02 is missing entirely.
	r.put(base, true, 100, 110, 95, 105, 10)
	r.put(base.Add(1*time.Minute), true, 105, 120, 100, 115, 20)
	r.put(base.Add(3*time.Minute), true, 112, 130, 110, 125, 25)
	r.put(base.Add(4*time.Minute), true, 125, 127, 90, 99, 30)

	w := Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(5 * time.Minute))}
	agg, incomplete, err := Aggregate(context.Background(), r, 1, w)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if agg != nil {
		t.Fatalf("Aggregate returned a computed OHLCV for an incomplete window: %+v", agg)
	}
	if incomplete == nil {
		t.Fatal("Aggregate did not report incomplete for a window missing a minute")
	}
	if len(incomplete.Missing) != 1 {
		t.Fatalf("Missing = %v, want exactly 1 entry", incomplete.Missing)
	}
	wantMissing := base.Add(2 * time.Minute)
	if !incomplete.Missing[0].Time().Equal(wantMissing) {
		t.Fatalf("Missing[0] = %s, want %s", incomplete.Missing[0], wantMissing)
	}
}

func TestAggregate_ForminCandleCountsAsMissing(t *testing.T) {
	r := newFakeCandleReader()
	base := utc(2026, time.October, 26, 18, 0)
	r.put(base, true, 100, 110, 95, 105, 10)
	r.put(base.Add(1*time.Minute), true, 105, 120, 100, 115, 20)
	// 18:02 exists but is still forming -- not a reliable source yet.
	r.put(base.Add(2*time.Minute), false, 115, 118, 108, 112, 15)

	w := Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(3 * time.Minute))}
	agg, incomplete, err := Aggregate(context.Background(), r, 1, w)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if agg != nil {
		t.Fatalf("Aggregate used a still-forming candle as if it were reliable: %+v", agg)
	}
	if incomplete == nil || len(incomplete.Missing) != 1 {
		t.Fatalf("incomplete = %+v, want exactly the forming minute reported missing", incomplete)
	}
	if !incomplete.Missing[0].Time().Equal(base.Add(2 * time.Minute)) {
		t.Fatalf("Missing[0] = %s, want the forming candle's open_time", incomplete.Missing[0])
	}
}

func TestAggregate_AllMissing_NamesEveryMinute(t *testing.T) {
	r := newFakeCandleReader() // empty: nothing recorded at all.
	base := utc(2026, time.October, 26, 18, 0)
	w := Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(5 * time.Minute))}

	_, incomplete, err := Aggregate(context.Background(), r, 1, w)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if incomplete == nil || len(incomplete.Missing) != 5 {
		t.Fatalf("incomplete = %+v, want all 5 minutes reported missing", incomplete)
	}
}

func TestAggregate_InvalidWindow(t *testing.T) {
	r := newFakeCandleReader()
	base := utc(2026, time.October, 26, 18, 0)

	tests := []struct {
		name string
		w    Window
	}{
		{"end before start", Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(-time.Minute))}},
		{"end equals start", Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base)}},
		{"not whole minutes", Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(90 * time.Second))}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Aggregate(context.Background(), r, 1, tt.w)
			if err == nil {
				t.Fatal("expected an error for an invalid window")
			}
		})
	}
}

func TestUTCWindow(t *testing.T) {
	// 18:07:30 truncated to a 5-minute boundary is 18:05, not 18:00 -- this
	// confirms UTCWindow aligns to multiples of d since the epoch, not to
	// the nearest hour or some other implicit boundary.
	t5 := clock.NewEventTime(utc(2026, time.October, 26, 18, 7).Add(30 * time.Second))
	w := UTCWindow(t5, 5*time.Minute)
	want := utc(2026, time.October, 26, 18, 5)
	if !w.Start.Time().Equal(want) {
		t.Errorf("UTCWindow(18:07:30, 5m).Start = %s, want %s", w.Start, want)
	}
	if !w.End.Time().Equal(want.Add(5 * time.Minute)) {
		t.Errorf("UTCWindow(18:07:30, 5m).End = %s, want %s", w.End, want.Add(5*time.Minute))
	}
}

// --- adr-004-ws-contract-review.md, review item 4: derived revision ---

func TestAggregate_DerivedRevision_NoCorrections(t *testing.T) {
	r := newFakeCandleReader()
	base := utc(2026, time.October, 26, 18, 0)
	for i := 0; i < 5; i++ {
		r.put(base.Add(time.Duration(i)*time.Minute), true, 100, 110, 95, 105, 10)
	}
	w := Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(5 * time.Minute))}
	agg, incomplete, err := Aggregate(context.Background(), r, 1, w)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if incomplete != nil {
		t.Fatalf("Aggregate reported incomplete unexpectedly: %+v", incomplete)
	}
	if agg.Revision != 1 {
		t.Errorf("Revision = %d, want 1 (every source candle at its own revision 1)", agg.Revision)
	}
}

func TestAggregate_DerivedRevision_OneSupersession(t *testing.T) {
	r := newFakeCandleReader()
	base := utc(2026, time.October, 26, 18, 0)
	for i := 0; i < 5; i++ {
		r.put(base.Add(time.Duration(i)*time.Minute), true, 100, 110, 95, 105, 10)
	}
	r.putRevision(base.Add(2*time.Minute), 2) // one corrected minute inside the window

	w := Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(5 * time.Minute))}
	agg, incomplete, err := Aggregate(context.Background(), r, 1, w)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if incomplete != nil {
		t.Fatalf("Aggregate reported incomplete unexpectedly: %+v", incomplete)
	}
	if agg.Revision != 2 {
		t.Errorf("Revision = %d, want 2: one superseded source minute", agg.Revision)
	}
}

func TestAggregate_DerivedRevision_TwoSupersessions(t *testing.T) {
	r := newFakeCandleReader()
	base := utc(2026, time.October, 26, 18, 0)
	for i := 0; i < 5; i++ {
		r.put(base.Add(time.Duration(i)*time.Minute), true, 100, 110, 95, 105, 10)
	}
	r.putRevision(base.Add(1*time.Minute), 2)
	r.putRevision(base.Add(3*time.Minute), 3) // a twice-corrected minute contributes 2, not 1

	w := Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(5 * time.Minute))}
	agg, incomplete, err := Aggregate(context.Background(), r, 1, w)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if incomplete != nil {
		t.Fatalf("Aggregate reported incomplete unexpectedly: %+v", incomplete)
	}
	// 1 (base) + (2-1) + (3-1) = 1 + 1 + 2 = 4, which is a sum of TWO
	// supersession EVENTS whose own revision deltas are 1 and 2 -- the
	// addendum's own "one supersession -> 2, two supersessions -> 3"
	// example assumes each supersession contributes exactly 1 (every
	// corrected minute goes from revision 1 to 2, never further), which
	// TestAggregate_DerivedRevision_TwoDistinctSupersessions below
	// reproduces exactly; this test instead confirms the general formula
	// (sum of revision-1, not a flat count of corrected minutes) for a
	// minute corrected twice.
	if agg.Revision != 4 {
		t.Errorf("Revision = %d, want 4 (1 + (2-1) + (3-1)): the general formula, not a flat count of corrected minutes", agg.Revision)
	}
}

func TestAggregate_DerivedRevision_TwoDistinctSupersessions(t *testing.T) {
	r := newFakeCandleReader()
	base := utc(2026, time.October, 26, 18, 0)
	for i := 0; i < 5; i++ {
		r.put(base.Add(time.Duration(i)*time.Minute), true, 100, 110, 95, 105, 10)
	}
	r.putRevision(base.Add(1*time.Minute), 2)
	r.putRevision(base.Add(3*time.Minute), 2) // two DIFFERENT minutes, each corrected once

	w := Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(5 * time.Minute))}
	agg, incomplete, err := Aggregate(context.Background(), r, 1, w)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if incomplete != nil {
		t.Fatalf("Aggregate reported incomplete unexpectedly: %+v", incomplete)
	}
	if agg.Revision != 3 {
		t.Errorf("Revision = %d, want 3: two distinct supersessions, the addendum's own example (A)", agg.Revision)
	}
}

func TestAggregate_DerivedRevision_OneMinuteWindowEqualsCandleRevision(t *testing.T) {
	r := newFakeCandleReader()
	base := utc(2026, time.October, 26, 18, 0)
	r.put(base, true, 100, 110, 95, 105, 10)
	r.putRevision(base, 5)

	w := Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(time.Minute))}
	agg, incomplete, err := Aggregate(context.Background(), r, 1, w)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if incomplete != nil {
		t.Fatalf("Aggregate reported incomplete unexpectedly: %+v", incomplete)
	}
	if agg.Revision != 5 {
		t.Errorf("Revision = %d, want 5: a 1-minute window's own derived revision is exactly its one source candle's revision", agg.Revision)
	}
}
