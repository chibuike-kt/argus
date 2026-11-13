package marketdata

import (
	"context"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
)

func TestAggregatePartial_IncompleteWindowStillReturnsBestEffort(t *testing.T) {
	r := newFakeCandleReader()
	base := utc(2026, time.October, 26, 18, 0)
	// Only the first 2 of 5 minutes present -- Aggregate would refuse
	// this window entirely; AggregatePartial must not.
	r.put(base, true, 100, 110, 95, 105, 10)
	r.put(base.Add(1*time.Minute), false, 105, 120, 100, 115, 20) // still forming

	w := Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(5 * time.Minute))}
	got, err := AggregatePartial(context.Background(), r, 1, w)
	if err != nil {
		t.Fatalf("AggregatePartial: %v", err)
	}
	if !got.HasData {
		t.Fatal("HasData = false, want true: two rows are present")
	}
	if got.Open != 100 || got.Close != 115 || got.High != 120 || got.Low != 95 {
		t.Errorf("got = %+v, want Open=100 Close=115 High=120 Low=95", got)
	}
	if got.Volume != 30 {
		t.Errorf("Volume = %d, want 30", got.Volume)
	}
}

func TestAggregatePartial_NoDataAtAll(t *testing.T) {
	r := newFakeCandleReader()
	base := utc(2026, time.October, 26, 18, 0)
	w := Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(5 * time.Minute))}
	got, err := AggregatePartial(context.Background(), r, 1, w)
	if err != nil {
		t.Fatalf("AggregatePartial: %v", err)
	}
	if got.HasData {
		t.Errorf("HasData = true, want false: no rows at all exist")
	}
}

func TestAggregatePartial_DerivedRevisionFromWhateverIsPresent(t *testing.T) {
	r := newFakeCandleReader()
	base := utc(2026, time.October, 26, 18, 0)
	r.put(base, true, 100, 110, 95, 105, 10)
	r.putRevision(base, 2)
	r.put(base.Add(1*time.Minute), true, 105, 120, 100, 115, 20)

	w := Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(5 * time.Minute))}
	got, err := AggregatePartial(context.Background(), r, 1, w)
	if err != nil {
		t.Fatalf("AggregatePartial: %v", err)
	}
	if got.Revision != 2 {
		t.Errorf("Revision = %d, want 2 (1 + (2-1) + (1-1))", got.Revision)
	}
}
