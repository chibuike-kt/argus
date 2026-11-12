package marketdata

import (
	"context"
	"testing"
	"time"
)

func TestAggregateRange_ExcludesIncompleteWindows(t *testing.T) {
	r := newFakeCandleReader()
	base := utc(2026, time.October, 26, 18, 0)

	// Window 1 (18:00-18:05): complete.
	for i := 0; i < 5; i++ {
		r.put(base.Add(time.Duration(i)*time.Minute), true, 100, 110, 95, 105, 10)
	}
	// Window 2 (18:05-18:10): missing 18:07 entirely -- incomplete.
	for i := 5; i < 10; i++ {
		if i == 7 {
			continue
		}
		r.put(base.Add(time.Duration(i)*time.Minute), true, 100, 110, 95, 105, 10)
	}
	// Window 3 (18:10-18:15): complete.
	for i := 10; i < 15; i++ {
		r.put(base.Add(time.Duration(i)*time.Minute), true, 100, 110, 95, 105, 10)
	}

	windows := WindowsInRange(base, base.Add(15*time.Minute), 5*time.Minute, false, 10)
	if len(windows) != 3 {
		t.Fatalf("test setup error: got %d windows, want 3", len(windows))
	}

	got, err := AggregateRange(context.Background(), r, 1, windows)
	if err != nil {
		t.Fatalf("AggregateRange: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d aggregates, want 2 (the incomplete middle window excluded): %+v", len(got), got)
	}
	if !got[0].Window.Start.Time().Equal(base) {
		t.Errorf("got[0].Window.Start = %s, want %s", got[0].Window.Start.Time(), base)
	}
	if !got[1].Window.Start.Time().Equal(base.Add(10 * time.Minute)) {
		t.Errorf("got[1].Window.Start = %s, want %s", got[1].Window.Start.Time(), base.Add(10*time.Minute))
	}
}

func TestAggregateRange_GoldenValues(t *testing.T) {
	r := newFakeCandleReader()
	base := utc(2026, time.October, 26, 18, 0)
	r.put(base, true, 100, 110, 95, 105, 10)
	r.put(base.Add(1*time.Minute), true, 105, 120, 100, 115, 20)
	r.put(base.Add(2*time.Minute), true, 115, 118, 108, 112, 15)
	r.put(base.Add(3*time.Minute), true, 112, 130, 110, 125, 25)
	r.put(base.Add(4*time.Minute), true, 125, 127, 90, 99, 30)

	windows := WindowsInRange(base, base.Add(5*time.Minute), 5*time.Minute, false, 10)
	got, err := AggregateRange(context.Background(), r, 1, windows)
	if err != nil {
		t.Fatalf("AggregateRange: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d aggregates, want 1", len(got))
	}
	// Hand computed exactly as TestAggregate_Complete already verifies
	// for the same five rows: open=100 (first open), close=99 (last
	// close), high=130 (max), low=90 (min), volume=100 (sum).
	agg := got[0]
	if agg.Open != 100 || agg.Close != 99 || agg.High != 130 || agg.Low != 90 || agg.Volume != 100 {
		t.Errorf("agg = %+v, want Open=100 Close=99 High=130 Low=90 Volume=100", agg)
	}
}

func TestAggregateRange_EmptyWindowList(t *testing.T) {
	r := newFakeCandleReader()
	got, err := AggregateRange(context.Background(), r, 1, nil)
	if err != nil {
		t.Fatalf("AggregateRange: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want empty", got)
	}
	if r.calls != 0 {
		t.Errorf("calls = %d, want 0: an empty window list must not query at all", r.calls)
	}
}

// TestAggregateRange_OneQueryRegardlessOfWindowCount proves addendum
// item B: the database call count does not grow with the number of
// bars requested. 500 hourly windows (the same shape the addendum's own
// "500 bar hourly request" timing ask uses) must still be exactly one
// call, not 500.
func TestAggregateRange_OneQueryRegardlessOfWindowCount(t *testing.T) {
	r := newFakeCandleReader()
	base := utc(2026, time.October, 1, 0, 0)
	const hours = 500
	for h := 0; h < hours; h++ {
		for m := 0; m < 60; m++ {
			r.put(base.Add(time.Duration(h)*time.Hour+time.Duration(m)*time.Minute), true, 100, 110, 95, 105, 10)
		}
	}

	windows := WindowsInRange(base, base.Add(hours*time.Hour), time.Hour, false, hours)
	if len(windows) != hours {
		t.Fatalf("test setup error: got %d windows, want %d", len(windows), hours)
	}

	got, err := AggregateRange(context.Background(), r, 1, windows)
	if err != nil {
		t.Fatalf("AggregateRange: %v", err)
	}
	if len(got) != hours {
		t.Fatalf("got %d aggregates, want %d (every hour complete)", len(got), hours)
	}
	if r.calls != 1 {
		t.Errorf("calls = %d, want exactly 1 for %d windows (addendum item B)", r.calls, hours)
	}
}
