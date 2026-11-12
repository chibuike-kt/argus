package marketdata

import (
	"context"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/store"
)

// putLocated is put (aggregate_test.go) with one difference: the
// stored row's own OpenTime carries loc, not whatever Location openTime
// itself had. The fake's own ListCurrentCandlesInRange filters by the
// map KEY (openTime, unchanged, still a plain instant comparison via
// Before -- location-independent), so this reproduces exactly what a
// real Postgres row looks like after lib/pq parses it back with a
// server-reported "TimeZone" session parameter that is not the literal
// string "UTC": the right instant, a different *time.Location.
func (f *fakeCandleReader) putLocated(openTime time.Time, loc *time.Location, isClosed bool, open, high, low, closePrice, volume int64) {
	f.rows[openTime] = store.Candle{
		OpenTime: openTime.In(loc),
		Open:     open,
		High:     high,
		Low:      low,
		Close:    closePrice,
		Volume:   volume,
		IsClosed: isClosed,
		Revision: 1,
	}
}

// fixedUTCEquivalentZone is a *time.Location distinct from the time.UTC
// singleton pointer but representing the identical, permanent zero
// offset -- standing in for "Etc/UTC" when the test binary's own tzdata
// is unavailable. time.FixedZone never consults tzdata, so this always
// works; a parallel case loads the real "Etc/UTC" IANA zone where
// available, since that is the literal name the production bug is
// named after (lib/pq's own time.LoadLocation(serverTimeZoneName)).
var fixedUTCEquivalentZone = time.FixedZone("Etc/UTC", 0)

func etcUTCOrSkip(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Etc/UTC")
	if err != nil {
		t.Skipf("Etc/UTC zone data unavailable in this environment: %v", err)
	}
	return loc
}

func TestAggregate_RowLocationEtcUTC_StillMatches(t *testing.T) {
	loc := etcUTCOrSkip(t)
	r := newFakeCandleReader()
	base := utc(2026, time.November, 12, 10, 0)
	for i := 0; i < 5; i++ {
		r.putLocated(base.Add(time.Duration(i)*time.Minute), loc, true, 100, 110, 95, 105, 10)
	}

	w := Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(5 * time.Minute))}
	agg, incomplete, err := Aggregate(context.Background(), r, 1, w)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if incomplete != nil {
		t.Fatalf("Aggregate reported incomplete despite every minute present, only under a different Location: %+v", incomplete)
	}
	if agg == nil {
		t.Fatal("agg is nil")
	}
	if agg.Volume != 50 {
		t.Errorf("Volume = %d, want 50 (5 rows x 10)", agg.Volume)
	}
}

func TestAggregate_RowLocationFixedZoneEquivalentToUTC_StillMatches(t *testing.T) {
	r := newFakeCandleReader()
	base := utc(2026, time.November, 12, 10, 0)
	for i := 0; i < 5; i++ {
		r.putLocated(base.Add(time.Duration(i)*time.Minute), fixedUTCEquivalentZone, true, 100, 110, 95, 105, 10)
	}

	w := Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(5 * time.Minute))}
	agg, incomplete, err := Aggregate(context.Background(), r, 1, w)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if incomplete != nil {
		t.Fatalf("Aggregate reported incomplete despite every minute present, only under a different (but offset-identical) Location: %+v", incomplete)
	}
	if agg.Volume != 50 {
		t.Errorf("Volume = %d, want 50", agg.Volume)
	}
}

func TestAggregate_RowLocationNonUTCZone_StillMatches(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("America/New_York zone data unavailable: %v", err)
	}
	r := newFakeCandleReader()
	base := utc(2026, time.November, 12, 10, 0)
	for i := 0; i < 5; i++ {
		// In(loc) changes only the display/zone metadata, never the
		// underlying instant -- the row still represents the same
		// moment base.Add(i*time.Minute) names in UTC.
		r.putLocated(base.Add(time.Duration(i)*time.Minute), loc, true, 100, 110, 95, 105, 10)
	}

	w := Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(5 * time.Minute))}
	agg, incomplete, err := Aggregate(context.Background(), r, 1, w)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if incomplete != nil {
		t.Fatalf("Aggregate reported incomplete despite every minute present, only under a non-UTC Location: %+v", incomplete)
	}
	if agg.Volume != 50 {
		t.Errorf("Volume = %d, want 50", agg.Volume)
	}
}

func TestAggregatePartial_RowLocationEtcUTC_StillMatches(t *testing.T) {
	loc := etcUTCOrSkip(t)
	r := newFakeCandleReader()
	base := utc(2026, time.November, 12, 10, 0)
	r.putLocated(base, loc, true, 100, 110, 95, 105, 10)
	r.putLocated(base.Add(time.Minute), loc, false, 105, 120, 100, 115, 20)

	w := Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(5 * time.Minute))}
	got, err := AggregatePartial(context.Background(), r, 1, w)
	if err != nil {
		t.Fatalf("AggregatePartial: %v", err)
	}
	if !got.HasData {
		t.Fatal("HasData = false, want true: both rows are present, only under a different Location")
	}
	if got.Volume != 30 {
		t.Errorf("Volume = %d, want 30", got.Volume)
	}
}

func TestAggregatePartial_RowLocationNonUTCZone_StillMatches(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("America/New_York zone data unavailable: %v", err)
	}
	r := newFakeCandleReader()
	base := utc(2026, time.November, 12, 10, 0)
	r.putLocated(base, loc, true, 100, 110, 95, 105, 10)
	r.putLocated(base.Add(time.Minute), loc, false, 105, 120, 100, 115, 20)

	w := Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(5 * time.Minute))}
	got, err := AggregatePartial(context.Background(), r, 1, w)
	if err != nil {
		t.Fatalf("AggregatePartial: %v", err)
	}
	if !got.HasData {
		t.Fatal("HasData = false, want true: both rows are present, only under a non-UTC Location")
	}
	if got.Volume != 30 {
		t.Errorf("Volume = %d, want 30", got.Volume)
	}
}
