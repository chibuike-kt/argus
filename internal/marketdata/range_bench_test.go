package marketdata

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// benchReader seeds n hourly windows' worth of 1-minute rows (60*n
// total) for BenchmarkAggregateRange's own sub-benchmarks below.
func benchReader(hours int) (*fakeCandleReader, []Window) {
	r := newFakeCandleReader()
	base := utc(2026, time.October, 1, 0, 0)
	for h := 0; h < hours; h++ {
		for m := 0; m < 60; m++ {
			r.put(base.Add(time.Duration(h)*time.Hour+time.Duration(m)*time.Minute), true, 100, 110, 95, 105, 10)
		}
	}
	windows := WindowsInRange(base, base.Add(time.Duration(hours)*time.Hour), time.Hour, false, hours)
	return r, windows
}

// BenchmarkAggregateRange reports how AggregateRange's own in-memory
// reduction scales with row count, now that it buckets the fetched
// rows once per request (rowsByOpenTime) instead of once per window.
// Before that change this was O(windows * total_rows); the
// expectation after it is that ns/op grows roughly linearly with
// hours (equivalently, with total row count, since each hour
// contributes a fixed 60 rows) -- run with -bench and compare the
// per-size ns/op directly; a quadratic regression would show up as
// each doubling of hours more than doubling ns/op.
func BenchmarkAggregateRange(b *testing.B) {
	for _, hours := range []int{50, 100, 200, 400, 800} {
		r, windows := benchReader(hours)
		b.Run(strconv.Itoa(hours)+"h", func(b *testing.B) {
			ctx := context.Background()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := AggregateRange(ctx, r, 1, windows); err != nil {
					b.Fatalf("AggregateRange: %v", err)
				}
			}
		})
	}
}
