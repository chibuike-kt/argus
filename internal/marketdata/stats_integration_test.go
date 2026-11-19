//go:build integration

package marketdata

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/quant"
)

// seedSeries bulk-inserts current, closed 1-minute candles for minute
// indexes from..to inclusive (open_time = start + i minutes), with
// close = 10000 + i and volume = 2, skipping any index in skip. One
// INSERT ... SELECT generate_series, so a 300,000-row seed takes
// seconds, not a round trip per row. (trg_candles_enforce_lifecycle
// governs UPDATE only; inserting a closed revision-1 row directly is
// legal, as InsertCorrectingCandle already does.)
func seedSeries(t testing.TB, db *sql.DB, instrumentID int64, start time.Time, from, to int, skip ...int) {
	t.Helper()
	skips := make([]int64, len(skip))
	for i, s := range skip {
		skips[i] = int64(s)
	}
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO candles (instrument_id, open_time, close_time, open, high, low, close, volume,
		                     revision, is_closed, tick_aligned, provider, ingested_at)
		SELECT $1, $2::timestamptz + i * interval '1 minute', $2::timestamptz + (i + 1) * interval '1 minute',
		       10000 + i, 10000 + i, 10000 + i, 10000 + i, 2,
		       1, TRUE, TRUE, 'test', now()
		FROM generate_series($3::int, $4::int) AS i
		WHERE NOT (i = ANY($5::bigint[]))`,
		instrumentID, start, from, to, int64Array(skips))
	if err != nil {
		t.Fatalf("seed series [%d,%d]: %v", from, to, err)
	}
}

// int64Array renders a Postgres array literal, avoiding a pq import
// just for pq.Array in this one helper.
func int64Array(v []int64) string {
	out := []byte{'{'}
	for i, x := range v {
		if i > 0 {
			out = append(out, ',')
		}
		out = strconv.AppendInt(out, x, 10)
	}
	return string(append(out, '}'))
}

var statsT0 = time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)

// Instrument scales from testInstrument: price 2, qty 6.

func TestIntegration_Stats_ExactlyTwentyFourHours(t *testing.T) {
	db := testDB(t)
	id := testInstrument(t, db)
	// i = 0..1440: latest i=1440 (close 114.40), reference i=0 exactly
	// 24h earlier (close 100.00).
	seedSeries(t, db, id, statsT0, 0, 1440)

	s, err := NewStore(db, clock.Real{}).Stats(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if s.Latest == nil || s.Latest.Close != 11440 {
		t.Fatalf("Latest = %+v, want close 11440", s.Latest)
	}
	if !s.Latest.OpenTime.Time().Equal(statsT0.Add(1440 * time.Minute)) {
		t.Fatalf("latest open time = %v", s.Latest.OpenTime)
	}
	// Hand-computed: 114.40 - 100.00 = 14.40; 14.40/100.00 = 14.40%.
	if s.Live.Change24h == nil || s.Live.Change24h.Abs.String(2) != "14.40" || s.Live.Change24h.Pct.String() != "14.40" {
		t.Fatalf("Change24h = %+v, want 14.40 / 14.40%%", s.Live.Change24h)
	}
	// Window (i=0, i=1440] holds 1440 candles x volume 2 = 2880 -> at
	// qty scale 6, "0.002880".
	if s.Live.Volume24h == nil || s.Live.Volume24h.String(6) != "0.002880" {
		t.Fatalf("Volume24h = %v, want 0.002880", s.Live.Volume24h)
	}
	if s.Performance["1D"] == nil || s.Performance["1D"].String() != "14.40" {
		t.Fatalf("1D = %v", s.Performance["1D"])
	}
	if s.Performance["1W"] != nil {
		t.Fatalf("1W = %v, want absent", s.Performance["1W"])
	}
}

func TestIntegration_Stats_ShorterThanTwentyFourHours(t *testing.T) {
	db := testDB(t)
	store := NewStore(db, clock.Real{})

	t.Run("1440 candles: no 24h reference, but a full volume window", func(t *testing.T) {
		id := testInstrument(t, db)
		seedSeries(t, db, id, statsT0, 1, 1440)
		s, err := store.Stats(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if s.Live.Change24h != nil || s.Performance["1D"] != nil {
			t.Fatalf("Change24h = %+v, 1D = %v, want both absent", s.Live.Change24h, s.Performance["1D"])
		}
		if s.Live.Volume24h == nil || *s.Live.Volume24h != 2880 {
			t.Fatalf("Volume24h = %v, want 2880", s.Live.Volume24h)
		}
	})

	t.Run("100 candles: everything but last close absent", func(t *testing.T) {
		id := testInstrument(t, db)
		seedSeries(t, db, id, statsT0, 0, 99)
		s, err := store.Stats(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if s.Latest == nil || s.Latest.Close != 10099 {
			t.Fatalf("Latest = %+v", s.Latest)
		}
		if s.Live.Change24h != nil || s.Live.Volume24h != nil {
			t.Fatalf("Change24h = %+v Volume24h = %v, want absent", s.Live.Change24h, s.Live.Volume24h)
		}
	})

	t.Run("no candles at all", func(t *testing.T) {
		id := testInstrument(t, db)
		s, err := store.Stats(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if s.Latest != nil || s.Live != nil || s.Base != nil {
			t.Fatalf("got %+v, want all absent", s)
		}
		if len(s.Performance) != len(PerformancePeriods()) {
			t.Fatalf("Performance has %d keys, want one per period", len(s.Performance))
		}
	})
}

// A missing minute exactly at the reference is absent even though both
// neighbours exist: never a nearby candle, never interpolated. The
// reference minute is outside the (T-24h, T] volume window, so volume
// stays present.
func TestIntegration_Stats_MissingReferenceMinute(t *testing.T) {
	db := testDB(t)
	id := testInstrument(t, db)
	seedSeries(t, db, id, statsT0, -10, 1440, 0)

	s, err := NewStore(db, clock.Real{}).Stats(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if s.Live.Change24h != nil || s.Performance["1D"] != nil {
		t.Fatalf("Change24h = %+v 1D = %v, want absent (reference minute missing)", s.Live.Change24h, s.Performance["1D"])
	}
	if s.Live.Volume24h == nil || *s.Live.Volume24h != 2880 {
		t.Fatalf("Volume24h = %v, want 2880", s.Live.Volume24h)
	}

	// A missing minute inside the volume window makes volume absent.
	id2 := testInstrument(t, db)
	seedSeries(t, db, id2, statsT0, 0, 1440, 700)
	s2, err := NewStore(db, clock.Real{}).Stats(context.Background(), id2)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Live.Volume24h != nil {
		t.Fatalf("Volume24h = %v with minute 700 missing, want absent", *s2.Live.Volume24h)
	}
	if s2.Live.Change24h == nil {
		t.Fatal("Change24h absent, want present (reference minute exists)")
	}
}

// Superseded rows are never used: the reference and the volume window
// both read the current revision.
func TestIntegration_Stats_UsesCurrentRevision(t *testing.T) {
	db := testDB(t)
	id := testInstrument(t, db)
	seedSeries(t, db, id, statsT0, 0, 1440)
	store := NewStore(db, clock.Real{})
	ctx := context.Background()

	// Reference (i=0) corrected from 100.00 to 80.00.
	if _, err := store.SupersedeCandle(ctx, id, clock.NewEventTime(statsT0), Correction{
		Open: 8000, High: 8000, Low: 8000, Close: 8000, Volume: 2, Provider: "test", IngestedAt: it(time.Now()),
	}); err != nil {
		t.Fatal(err)
	}
	// i=5 (inside the window) corrected from volume 2 to 102.
	if _, err := store.SupersedeCandle(ctx, id, clock.NewEventTime(statsT0.Add(5*time.Minute)), Correction{
		Open: 10005, High: 10005, Low: 10005, Close: 10005, Volume: 102, Provider: "test", IngestedAt: it(time.Now()),
	}); err != nil {
		t.Fatal(err)
	}

	s, err := store.Stats(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// Hand-computed: 114.40 - 80.00 = 34.40; 34.40/80.00 = 43.00%.
	if s.Live.Change24h == nil || s.Live.Change24h.Abs.String(2) != "34.40" || s.Live.Change24h.Pct.String() != "43.00" {
		t.Fatalf("Change24h = %+v, want 34.40 / 43.00%%", s.Live.Change24h)
	}
	// 1439 x 2 + 102 = 2980.
	if s.Live.Volume24h == nil || *s.Live.Volume24h != 2980 {
		t.Fatalf("Volume24h = %v, want 2980", s.Live.Volume24h)
	}
}

// D3: the forming candle is the live anchor. Its close is the live
// price; the 24h reference is the closed candle at forming.open_time -
// 24h - 1 minute; the volume window is the 1440 minutes ending at the
// forming one. Performance stays on the latest CLOSED candle.
func TestIntegration_Stats_FormingAnchor(t *testing.T) {
	db := testDB(t)
	id := testInstrument(t, db)
	seedSeries(t, db, id, statsT0, 0, 1440)
	store := NewStore(db, clock.Real{})
	tickAt := time.Date(2026, 11, 11, 0, 1, 17, 0, time.UTC)
	if _, err := store.UpsertForming(context.Background(), Tick{
		InstrumentID: id, OpenTime: clock.NewEventTime(statsT0.Add(1441 * time.Minute)),
		Open: 50000, High: 50000, Low: 50000, Close: 50000, Volume: 9, Provider: "test", IngestedAt: it(tickAt),
	}); err != nil {
		t.Fatal(err)
	}
	s, err := store.Stats(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if s.Latest == nil || s.Latest.Close != 11440 {
		t.Fatalf("Latest = %+v, want the closed i=1440 candle", s.Latest)
	}
	if s.Live == nil || s.Live.LastPrice != 50000 || s.Live.AnchorClosed {
		t.Fatalf("Live = %+v, want the forming close 50000", s.Live)
	}
	if !s.Live.AsOf.Time().Equal(statsT0.Add(1441*time.Minute)) || !s.Live.LastTickAt.Time().Equal(tickAt) {
		t.Fatalf("as_of %v last tick %v", s.Live.AsOf, s.Live.LastTickAt)
	}
	// Reference: 1441 - 1441 minutes = i=0, close 100.00. Hand-computed:
	// 500.00 - 100.00 = 400.00; 400/100 = 400.00%.
	if !s.Live.ReferenceOpenTime.Time().Equal(statsT0) {
		t.Fatalf("reference open time = %v, want %v", s.Live.ReferenceOpenTime, statsT0)
	}
	if s.Live.Change24h == nil || s.Live.Change24h.Abs.String(2) != "400.00" || s.Live.Change24h.Pct.String() != "400.00" {
		t.Fatalf("Change24h = %+v, want 400.00 / 400.00%%", s.Live.Change24h)
	}
	// Window: closed i=2..1440 (1439 x 2 = 2878) plus the forming 9.
	if s.Live.VolumeMinutes != 1440 || s.Live.Volume24h == nil || *s.Live.Volume24h != 2887 {
		t.Fatalf("Volume24h = %v over %d minutes, want 2887 over 1440", s.Live.Volume24h, s.Live.VolumeMinutes)
	}
	// Rule 4 unchanged: i=1440 (114.40) vs i=0 (100.00) = 14.40%.
	if p := s.Performance["1D"]; p == nil || p.String() != "14.40" {
		t.Fatalf("1D = %v, want 14.40 (closed anchor)", p)
	}

	// The per-tick path gives the same figures from the same base.
	again, err := s.Base.Apply(50000, 9, it(tickAt))
	if err != nil {
		t.Fatal(err)
	}
	if again.Change24h.Abs != s.Live.Change24h.Abs || *again.Volume24h != *s.Live.Volume24h {
		t.Fatalf("Base.Apply = %+v, want the same as Stats' %+v", again, s.Live)
	}
}

// D3's reference is EXACTLY forming.open_time - 24h - 1 minute: a missing
// minute there is absent even though the closed-anchor reference (24h
// before the latest closed candle) exists.
func TestIntegration_Stats_FormingAnchorMissingReference(t *testing.T) {
	db := testDB(t)
	id := testInstrument(t, db)
	seedSeries(t, db, id, statsT0, -5, 1440, 0)
	store := NewStore(db, clock.Real{})
	if _, err := store.UpsertForming(context.Background(), Tick{
		InstrumentID: id, OpenTime: clock.NewEventTime(statsT0.Add(1441 * time.Minute)),
		Open: 50000, High: 50000, Low: 50000, Close: 50000, Volume: 9, Provider: "test", IngestedAt: it(time.Now()),
	}); err != nil {
		t.Fatal(err)
	}
	s, err := store.Stats(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if s.Live.Change24h != nil {
		t.Fatalf("Change24h = %+v, want absent (i=0 missing)", s.Live.Change24h)
	}
	if !s.Live.ReferenceOpenTime.Time().Equal(statsT0) {
		t.Fatalf("reference open time = %v, want the missing minute %v", s.Live.ReferenceOpenTime, statsT0)
	}
}

// A forming row older than the newest candle (a minute that never
// closed) is neither the anchor nor counted in the volume window.
func TestIntegration_Stats_StaleFormingRowNotCounted(t *testing.T) {
	db := testDB(t)
	id := testInstrument(t, db)
	seedSeries(t, db, id, statsT0, 0, 1440, 700)
	store := NewStore(db, clock.Real{})
	if _, err := store.UpsertForming(context.Background(), Tick{
		InstrumentID: id, OpenTime: clock.NewEventTime(statsT0.Add(700 * time.Minute)),
		Open: 1, High: 1, Low: 1, Close: 1, Volume: 1, Provider: "test", IngestedAt: it(time.Now()),
	}); err != nil {
		t.Fatal(err)
	}
	s, err := store.Stats(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if s.Live.LastPrice != 11440 || !s.Live.AnchorClosed {
		t.Fatalf("Live = %+v, want the closed i=1440 candle as anchor", s.Live)
	}
	if s.Live.Volume24h != nil || s.Live.VolumeMinutes != 1439 {
		t.Fatalf("Volume24h = %v over %d minutes, want absent over 1439", s.Live.Volume24h, s.Live.VolumeMinutes)
	}
}

func TestIntegration_Stats_WeekPerformance(t *testing.T) {
	db := testDB(t)
	id := testInstrument(t, db)
	seedSeries(t, db, id, statsT0, 0, 10080) // 7 days + 1 minute

	s, err := NewStore(db, clock.Real{}).Stats(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	// Latest i=10080 close 200.80. 1W reference i=0 close 100.00:
	// +100.80%. 1D reference i=8640 close 186.40: 14.40/186.40 =
	// 7.7253...% -> 7.73.
	if p := s.Performance["1W"]; p == nil || p.String() != "100.80" {
		t.Fatalf("1W = %v, want 100.80", p)
	}
	if p := s.Performance["1D"]; p == nil || p.String() != "7.73" {
		t.Fatalf("1D = %v, want 7.73", p)
	}
	for _, k := range []string{"1M", "3M", "6M", "1Y"} {
		if s.Performance[k] != nil {
			t.Fatalf("%s = %v, want absent", k, s.Performance[k])
		}
	}
}

// The reference offset must be a fixed duration in every session time
// zone. Latest = 2026-11-01 12:00 UTC, 6 hours after New York left DST
// (06:00 UTC): "timestamptz - interval '1 day'" in an America/New_York
// session lands on 2026-10-31 11:00 UTC (one wall-clock day back, 25
// real hours), which is not seeded -- so a regression to day intervals
// fails here, while the fixed 86400-second offset finds 12:00 UTC.
func TestIntegration_Stats_TimeZones(t *testing.T) {
	for _, zone := range []string{"Etc/UTC", "America/New_York"} {
		t.Run(zone, func(t *testing.T) {
			db := testDBWithTimeZone(t, zone)
			id := testInstrument(t, db)
			start := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
			seedSeries(t, db, id, start, 0, 1440)

			s, err := NewStore(db, clock.Real{}).Stats(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			wantLatest := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
			if !s.Latest.OpenTime.Time().Equal(wantLatest) {
				t.Fatalf("latest open time = %v, want %v", s.Latest.OpenTime, wantLatest)
			}
			if s.Live.Change24h == nil || s.Live.Change24h.Abs.String(2) != "14.40" {
				t.Fatalf("Change24h = %+v, want 14.40 (reference exactly 86400s back)", s.Live.Change24h)
			}
			if s.Live.Volume24h == nil || *s.Live.Volume24h != quant.Qty(2880) {
				t.Fatalf("Volume24h = %v, want 2880", s.Live.Volume24h)
			}

			// D3 with a forming anchor at 12:01 UTC: the reference is
			// exactly 86460s back (2026-10-31 12:00 UTC), whatever the
			// session zone.
			if _, err := NewStore(db, clock.Real{}).UpsertForming(context.Background(), Tick{
				InstrumentID: id, OpenTime: clock.NewEventTime(wantLatest.Add(time.Minute)),
				Open: 20000, High: 20000, Low: 20000, Close: 20000, Volume: 1, Provider: "test", IngestedAt: it(time.Now()),
			}); err != nil {
				t.Fatal(err)
			}
			s, err = NewStore(db, clock.Real{}).Stats(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if !s.Live.ReferenceOpenTime.Time().Equal(start) {
				t.Fatalf("forming reference = %v, want %v", s.Live.ReferenceOpenTime, start)
			}
			// 200.00 - 100.00 = 100.00.
			if s.Live.Change24h == nil || s.Live.Change24h.Abs.String(2) != "100.00" {
				t.Fatalf("forming Change24h = %+v, want 100.00", s.Live.Change24h)
			}
		})
	}
}

// Scale (CLAUDE.md "before you say a change is done", item 5): the
// statement's cost should not depend on how much history exists. 30,000
// rows is ~3 weeks of 1-minute data (the brief's realistic size);
// 300,000 is ten times that.
func TestIntegration_Stats_Timing(t *testing.T) {
	// Opt-in (ARGUS_RUN_SLOW_TESTS=1, the same switch as the repo's other
	// slow tests): it seeds 330,000 rows into the shared test database.
	// The rows are deliberately NOT deleted afterwards: candles.superseded_by
	// is a self-referencing foreign key with no index, so every deleted
	// row costs a scan of the whole candles table -- a cleanup DELETE of
	// this size ran for over nine minutes and pushed the package past
	// go test's 10-minute limit under -race.
	if os.Getenv("ARGUS_RUN_SLOW_TESTS") != "1" {
		t.Skip("slow: set ARGUS_RUN_SLOW_TESTS=1")
	}
	db := testDB(t)
	store := NewStore(db, clock.Real{})
	for _, n := range []int{30_000, 300_000} {
		id := testInstrument(t, db)
		seedSeries(t, db, id, statsT0, 0, n-1)
		if _, err := db.Exec(`ANALYZE candles`); err != nil {
			t.Fatal(err)
		}
		const runs = 20
		var total, worst time.Duration
		for range runs {
			start := time.Now()
			s, err := store.Stats(context.Background(), id)
			el := time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			if s.Live.Change24h == nil || s.Live.Volume24h == nil {
				t.Fatalf("n=%d: want change and volume present", n)
			}
			total += el
			worst = max(worst, el)
		}
		t.Logf("Stats over %d rows: mean %v, worst %v (%d runs)", n, total/runs, worst, runs)
	}
}
