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
	}
}

func (f *fakeCandleReader) ListCurrentCandlesInRange(_ context.Context, _ int64, start, end time.Time) ([]store.Candle, error) {
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
