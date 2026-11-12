//go:build integration

package marketdata

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/quant"
)

// testDBWithTimeZone opens a connection pool whose own session reports
// "TimeZone" as zone (via the Postgres startup packet's own "options"
// GUC-setting mechanism -- "-c timezone=<zone>"), independent of
// whatever the shared test database container's own default is. This
// is what actually reproduces the bug: lib/pq's own
// processParameterStatus (conn.go) calls time.LoadLocation on exactly
// this server-reported name, and only the literal name "UTC" resolves
// to the time.UTC singleton -- "Etc/UTC" (this test's own target, and a
// common Postgres image default) resolves to a different, but offset-
// identical, *time.Location.
func testDBWithTimeZone(t *testing.T, zone string) *sql.DB {
	t.Helper()
	base := os.Getenv("ARGUS_TEST_DATABASE_URL")
	if base == "" {
		base = "postgres://argus:argus@localhost:5432/argus?sslmode=disable"
	}
	sep := "&"
	if !containsQuery(base) {
		sep = "?"
	}
	dsn := fmt.Sprintf("%s%soptions=-c%%20timezone%%3D%s", base, sep, urlEncodeZone(zone))

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open test database with timezone %s: %v", zone, err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("integration test database unreachable at %s: %v", dsn, err)
	}

	var got string
	if err := db.QueryRowContext(ctx, "SHOW timezone").Scan(&got); err != nil {
		t.Fatalf("SHOW timezone: %v", err)
	}
	if got != zone {
		t.Skipf("server did not honor the requested session timezone (got %q, want %q) -- options may be restricted by postgresql.conf", got, zone)
	}
	return db
}

func containsQuery(dsn string) bool {
	for _, c := range dsn {
		if c == '?' {
			return true
		}
	}
	return false
}

func urlEncodeZone(zone string) string {
	out := make([]byte, 0, len(zone)+4)
	for _, c := range []byte(zone) {
		if c == '/' {
			out = append(out, '%', '2', 'F')
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

// TestIntegration_Aggregate_EtcUTCServerTimezone is the regression
// test for the map[time.Time] bug: before the fix, every derived
// window read back through a connection whose own session TimeZone is
// "Etc/UTC" (not the literal "UTC") was reported IncompleteWindow
// regardless of how many rows actually existed, because the map key
// built from a real row's OpenTime (lib/pq's own *time.Location,
// loaded via time.LoadLocation("Etc/UTC")) never matched a lookup key
// built through clock.EventTime (always the time.UTC singleton).
func TestIntegration_Aggregate_EtcUTCServerTimezone(t *testing.T) {
	writeDB := testDB(t)
	instrumentID := testInstrument(t, writeDB)
	writeStore := NewStore(writeDB, clock.Real{})

	base := et(2026, time.November, 12, 10, 0)
	for i := 0; i < 5; i++ {
		seedClosedMinute(t, writeStore, instrumentID, clock.NewEventTime(base.Time().Add(time.Duration(i)*time.Minute)), int64(100+i))
	}

	readDB := testDBWithTimeZone(t, "Etc/UTC")
	readStore := NewStore(readDB, clock.Real{})

	w := Window{Start: base, End: clock.NewEventTime(base.Time().Add(5 * time.Minute))}
	agg, incomplete, err := Aggregate(context.Background(), readStore, instrumentID, w)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if incomplete != nil {
		t.Fatalf("Aggregate reported incomplete reading through an Etc/UTC session, despite every minute being closed and present: %+v", incomplete)
	}
	if agg == nil || agg.Volume != 50 {
		t.Fatalf("agg = %+v, want non-nil with Volume=50", agg)
	}
}

func TestIntegration_AggregateRange_EtcUTCServerTimezone(t *testing.T) {
	writeDB := testDB(t)
	instrumentID := testInstrument(t, writeDB)
	writeStore := NewStore(writeDB, clock.Real{})

	base := et(2026, time.November, 12, 11, 0)
	for i := 0; i < 10; i++ {
		seedClosedMinute(t, writeStore, instrumentID, clock.NewEventTime(base.Time().Add(time.Duration(i)*time.Minute)), int64(100+i))
	}

	readDB := testDBWithTimeZone(t, "Etc/UTC")
	readStore := NewStore(readDB, clock.Real{})

	windows := []Window{
		{Start: base, End: clock.NewEventTime(base.Time().Add(5 * time.Minute))},
		{Start: clock.NewEventTime(base.Time().Add(5 * time.Minute)), End: clock.NewEventTime(base.Time().Add(10 * time.Minute))},
	}
	got, err := readStore.AggregateRange(context.Background(), instrumentID, windows)
	if err != nil {
		t.Fatalf("AggregateRange: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("AggregateRange returned %d windows, want 2 (an Etc/UTC session should not silently drop a complete window)", len(got))
	}
}

func TestIntegration_LatestCompleteWindow_EtcUTCServerTimezone_DerivedInterval(t *testing.T) {
	writeDB := testDB(t)
	instrumentID := testInstrument(t, writeDB)
	writeStore := NewStore(writeDB, clock.Real{})

	base := et(2026, time.November, 12, 12, 0)
	for i := 0; i < 5; i++ {
		seedClosedMinute(t, writeStore, instrumentID, clock.NewEventTime(base.Time().Add(time.Duration(i)*time.Minute)), int64(100+i))
	}

	readDB := testDBWithTimeZone(t, "Etc/UTC")
	readStore := NewStore(readDB, clock.Real{})

	got, err := readStore.LatestCompleteWindow(context.Background(), instrumentID, 5*time.Minute)
	if err != nil {
		t.Fatalf("LatestCompleteWindow: %v", err)
	}
	if got == nil {
		t.Fatal("LatestCompleteWindow = nil, want the 5-minute window built from the 5 closed minutes just seeded -- an Etc/UTC session must not make a complete derived window look incomplete")
	}
}

func TestIntegration_AggregatePartial_EtcUTCServerTimezone(t *testing.T) {
	writeDB := testDB(t)
	instrumentID := testInstrument(t, writeDB)
	writeStore := NewStore(writeDB, clock.Real{})

	base := et(2026, time.November, 12, 13, 0)
	if _, err := writeStore.UpsertForming(context.Background(), Tick{
		InstrumentID: instrumentID, OpenTime: base,
		Open: 100, High: 110, Low: 95, Close: quant.Price(105), Volume: 10,
		Provider: "test", IngestedAt: it(time.Now()),
	}); err != nil {
		t.Fatalf("UpsertForming: %v", err)
	}

	readDB := testDBWithTimeZone(t, "Etc/UTC")
	readStore := NewStore(readDB, clock.Real{})

	w := Window{Start: base, End: clock.NewEventTime(base.Time().Add(5 * time.Minute))}
	got, err := readStore.AggregatePartial(context.Background(), instrumentID, w)
	if err != nil {
		t.Fatalf("AggregatePartial: %v", err)
	}
	if !got.HasData {
		t.Fatal("HasData = false, want true: the just-seeded forming row must be visible reading through an Etc/UTC session")
	}
}
