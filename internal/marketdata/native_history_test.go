package marketdata

import (
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/quant"
)

// L1.2: native candles win on the same open time, the order is the one
// asked for, and the result is cut to limit from the right end.
func TestMergeNativeHistory(t *testing.T) {
	h := func(n int) time.Time { return time.Date(2026, 11, 20, n, 0, 0, 0, time.UTC) }
	native := func(n int, cl int64) Candle {
		return Candle{OpenTime: clock.NewEventTime(h(n)), CloseTime: clock.NewEventTime(h(n + 1)), Close: quant.Price(cl), Revision: 3}
	}
	agg := func(n int, cl int64) AggregateOHLCV {
		return AggregateOHLCV{Window: Window{Start: clock.NewEventTime(h(n)), End: clock.NewEventTime(h(n + 1))}, Close: quant.Price(cl), Revision: 1}
	}
	ny, _ := time.LoadLocation("America/New_York")
	// The same instant in another location still matches (keyed by UnixNano).
	nyAgg := AggregateOHLCV{Window: Window{Start: clock.NewEventTime(h(2).In(ny)), End: clock.NewEventTime(h(3))}, Close: 999, Revision: 1}
	got := mergeNativeHistory([]Candle{native(1, 10), native(2, 20)}, []AggregateOHLCV{agg(0, 5), nyAgg, agg(3, 35)}, false, 10)
	want := []int64{5, 10, 20, 35}
	if len(got) != len(want) {
		t.Fatalf("got %d candles, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Close != quant.Price(w) {
			t.Fatalf("ascending[%d].Close = %d, want %d (native wins on 02:00)", i, got[i].Close, w)
		}
	}
	if got[2].Revision != 3 {
		t.Fatalf("the native candle kept revision %d, want its own 3", got[2].Revision)
	}
	desc := mergeNativeHistory([]Candle{native(1, 10), native(2, 20)}, []AggregateOHLCV{agg(0, 5), agg(3, 35)}, true, 2)
	if len(desc) != 2 || desc[0].Close != 35 || desc[1].Close != 20 {
		t.Fatalf("descending limit 2 = %+v, want 03:00 then 02:00", desc)
	}
	asc := mergeNativeHistory([]Candle{native(1, 10), native(2, 20)}, []AggregateOHLCV{agg(0, 5), agg(3, 35)}, false, 2)
	if len(asc) != 2 || asc[0].Close != 5 || asc[1].Close != 10 {
		t.Fatalf("ascending limit 2 = %+v, want 00:00 then 01:00", asc)
	}
}
