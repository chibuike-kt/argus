//go:build integration

package marketdata

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/store"
)

// L1.1 decision 2: native 15m, 1h and 4h candles share the candles table
// with the 1 minute series, so every 1 minute read and write must name its
// interval. This test stores native rows (and native gaps) at the same open
// times as the 1 minute rows, at a minute the 1 minute series is missing,
// and later than the newest 1 minute candle, then shows that every 1 minute
// path returns exactly what it returned before, and that every 1 minute
// write leaves the native rows untouched. Both a UTC and a non UTC session.

// oneMinuteView is everything the 1 minute read paths return for the test
// instrument.
type oneMinuteView struct {
	Current      Candle
	LatestClosed Candle
	Agg          *AggregateOHLCV
	Range        []AggregateOHLCV
	Partial      PartialAggregate
	Asc          []Candle
	Desc         []Candle
	Stats        Stats
	Missing      []MissingRange
	Count        int64
	Gaps         []Gap
	EngineLatest time.Time
	EngineAfter  int64
}

func readOneMinuteView(ctx context.Context, t *testing.T, s *Store, q *store.Queries, id int64, base time.Time) oneMinuteView {
	t.Helper()
	var v oneMinuteView
	var err error
	if v.Current, err = s.GetCurrent(ctx, id, clock.NewEventTime(base)); err != nil {
		t.Fatalf("GetCurrent: %v", err)
	}
	if v.LatestClosed, err = s.GetLatestClosed(ctx, id); err != nil {
		t.Fatalf("GetLatestClosed: %v", err)
	}
	agg, inc, err := Aggregate(ctx, s, id, Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(15 * time.Minute))})
	if err != nil || inc != nil {
		t.Fatalf("Aggregate: %v %+v", err, inc)
	}
	v.Agg = agg
	var windows []Window
	for w := base; w.Before(base.Add(2 * time.Hour)); w = w.Add(15 * time.Minute) {
		windows = append(windows, Window{Start: clock.NewEventTime(w), End: clock.NewEventTime(w.Add(15 * time.Minute))})
	}
	if v.Range, err = s.AggregateRange(ctx, id, windows); err != nil {
		t.Fatalf("AggregateRange: %v", err)
	}
	if v.Partial, err = s.AggregatePartial(ctx, id, Window{Start: clock.NewEventTime(base.Add(time.Hour)), End: clock.NewEventTime(base.Add(2 * time.Hour))}); err != nil {
		t.Fatalf("AggregatePartial: %v", err)
	}
	if v.Asc, err = s.ListClosedInRangeLimited(ctx, id, base.Add(-24*time.Hour), base.Add(24*time.Hour), 1000); err != nil {
		t.Fatalf("ListClosedInRangeLimited: %v", err)
	}
	if v.Desc, err = s.ListClosedInRangeLimitedDesc(ctx, id, nil, nil, 5); err != nil {
		t.Fatalf("ListClosedInRangeLimitedDesc: %v", err)
	}
	if v.Stats, err = s.Stats(ctx, id); err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if v.Missing, err = s.MissingRanges(ctx, id, base, base.Add(2*time.Hour), base); err != nil {
		t.Fatalf("MissingRanges: %v", err)
	}
	if v.Count, err = s.CountClosed(ctx, id, base.Add(-24*time.Hour), base.Add(24*time.Hour)); err != nil {
		t.Fatalf("CountClosed: %v", err)
	}
	if v.Gaps, err = s.ListOpenGaps(ctx, id); err != nil {
		t.Fatalf("ListOpenGaps: %v", err)
	}
	rows, err := q.ListEngineStatus(ctx, string(Interval1m))
	if err != nil {
		t.Fatalf("ListEngineStatus: %v", err)
	}
	for _, r := range rows {
		if r.InstrumentID == id {
			v.EngineLatest = r.LatestClosedCloseTime.UTC()
			v.EngineAfter = r.ClosedCandlesAfterCursor
		}
	}
	return v
}

// nativeRows is every non 1m candle and gap of the instrument, as stored.
type nativeRows struct {
	Candles []store.Candle
	Gaps    int
}

func readNativeRows(t *testing.T, db *sql.DB, id int64) nativeRows {
	t.Helper()
	rows, err := db.Query(`SELECT id, open_time, close_time, open, high, low, close, volume, revision, is_closed, interval, superseded_at IS NOT NULL
		FROM candles WHERE instrument_id = $1 AND interval <> '1m' ORDER BY interval, open_time, revision`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out nativeRows
	for rows.Next() {
		var c store.Candle
		var sup bool
		if err := rows.Scan(&c.ID, &c.OpenTime, &c.CloseTime, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume, &c.Revision, &c.IsClosed, &c.Interval, &sup); err != nil {
			t.Fatal(err)
		}
		c.OpenTime, c.CloseTime = c.OpenTime.UTC(), c.CloseTime.UTC()
		c.SupersededAt = sql.NullTime{Valid: sup}
		out.Candles = append(out.Candles, c)
	}
	if err := db.QueryRow(`SELECT count(*) FROM candle_gaps WHERE instrument_id = $1 AND interval <> '1m' AND resolved_at IS NULL`, id).Scan(&out.Gaps); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestIntegration_OneMinutePathsIgnoreNativeRows(t *testing.T) {
	for _, zone := range []string{"Etc/UTC", "America/New_York"} {
		t.Run(zone, func(t *testing.T) {
			db := testDBWithTimeZone(t, zone)
			ctx := context.Background()
			id := testInstrument(t, db)
			// Enabled so the engine status query (enabled instruments only)
			// lists it; a test row in the test database.
			if _, err := db.Exec(`UPDATE instruments SET enabled = true WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
			s := NewStore(db, clock.Real{})
			q := store.New(db)
			// A 4h boundary, so every native interval has a candle opening here.
			base := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

			// 1 minute series: 12:00 to 13:29 closed, 12:30 missing (a hole),
			// 13:30 forming.
			for i := 0; i < 90; i++ {
				if i == 30 {
					continue
				}
				seedClosedMinute(t, s, id, clock.NewEventTime(base.Add(time.Duration(i)*time.Minute)), int64(96+i%14)) // within seedClosedMinute's 95 to 110 range
			}
			forming := clock.NewEventTime(base.Add(90 * time.Minute))
			if _, err := s.UpsertForming(ctx, Tick{InstrumentID: id, OpenTime: forming, Open: 100, High: 110, Low: 95, Close: 105, Volume: 3, Provider: "test", IngestedAt: it(time.Now())}); err != nil {
				t.Fatal(err)
			}

			before := readOneMinuteView(ctx, t, s, q, id, base)

			// Native rows with values no 1 minute row has: at 12:00 (the same
			// open time as a 1m row), 12:30 (where 1m has a hole), and later
			// than the newest 1 minute candle.
			farFuture := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			native := func(iv Interval, open time.Time) {
				if _, _, err := s.InsertClosed(ctx, iv, Tick{InstrumentID: id, OpenTime: clock.NewEventTime(open), Open: 9000, High: 9900, Low: 8000, Close: 9500, Volume: 777, Provider: "test", IngestedAt: it(time.Now())}, farFuture); err != nil {
					t.Fatalf("InsertClosed %s %s: %v", iv, open, err)
				}
			}
			for m := base; m.Before(base.Add(6 * time.Hour)); m = m.Add(15 * time.Minute) {
				native(Interval15m, m)
			}
			for m := base; m.Before(base.Add(8 * time.Hour)); m = m.Add(time.Hour) {
				native(Interval1h, m)
			}
			native(Interval4h, base)
			native(Interval4h, base.Add(4*time.Hour))
			// A native gap over the same range as the 1 minute hole.
			if _, err := s.RecordGapAt(ctx, Interval1h, id, clock.NewEventTime(base.Add(30*time.Minute)), clock.NewEventTime(base.Add(31*time.Minute))); err != nil {
				t.Fatal(err)
			}

			after := readOneMinuteView(ctx, t, s, q, id, base)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("a 1 minute read changed once native rows were stored:\nbefore %+v\nafter  %+v", before, after)
			}

			// Every 1 minute write, at open times native rows share.
			nativeBefore := readNativeRows(t, db, id)
			// Forming upsert into the 1m hole (a native 15m row opens at 12:30).
			hole := clock.NewEventTime(base.Add(30 * time.Minute))
			if _, err := s.UpsertForming(ctx, Tick{InstrumentID: id, OpenTime: hole, Open: 1, High: 2, Low: 1, Close: 2, Volume: 1, Provider: "test", IngestedAt: it(time.Now())}); err != nil {
				t.Fatalf("UpsertForming at the hole: %v", err)
			}
			if _, err := s.CloseWith(ctx, Tick{InstrumentID: id, OpenTime: hole, Open: 1, High: 3, Low: 1, Close: 3, Volume: 2, Provider: "test", IngestedAt: it(time.Now())}); err != nil {
				t.Fatalf("CloseWith at the hole: %v", err)
			}
			if _, err := s.Close(ctx, id, forming); err != nil {
				t.Fatalf("Close: %v", err)
			}
			corrected, err := s.SupersedeCandle(ctx, id, clock.NewEventTime(base), Correction{Open: 1, High: 9, Low: 1, Close: 5, Volume: 4, Provider: "test", IngestedAt: it(time.Now())})
			if err != nil {
				t.Fatalf("SupersedeCandle at 12:00 (native rows share it): %v", err)
			}
			if corrected.Revision != 2 || corrected.Interval != Interval1m || !corrected.CloseTime.Time().Equal(base.Add(time.Minute)) {
				t.Fatalf("1m correction = revision %d interval %s close %s, want revision 2 of the 1m candle closing 12:01", corrected.Revision, corrected.Interval, corrected.CloseTime)
			}
			if _, err := s.RecordGap(ctx, id, clock.NewEventTime(base.Add(30*time.Minute)), clock.NewEventTime(base.Add(31*time.Minute))); err != nil {
				t.Fatalf("RecordGap 1m over the native gap's range: %v", err)
			}
			if gaps, err := s.ListOpenGaps(ctx, id); err != nil || len(gaps) != 1 || gaps[0].Interval != Interval1m {
				t.Fatalf("ListOpenGaps = %+v, %v; want only the 1m gap", gaps, err)
			}
			if _, err := s.ResolveGap(ctx, id, clock.NewEventTime(base.Add(30*time.Minute)), clock.NewEventTime(base.Add(31*time.Minute)), "backfilled"); err != nil {
				t.Fatalf("ResolveGap 1m: %v", err)
			}
			if nativeAfter := readNativeRows(t, db, id); !reflect.DeepEqual(nativeBefore, nativeAfter) {
				t.Fatalf("a 1 minute write touched a native row or gap:\nbefore %+v\nafter  %+v", nativeBefore, nativeAfter)
			}
		})
	}
}

// Native candles: closed only, aligned to their interval in any session
// time zone, a duration that matches the interval, and corrections through
// the same revision rule as the 1 minute series.
func TestIntegration_NativeCandles_ClosedAlignedAndCorrected(t *testing.T) {
	for _, zone := range []string{"Etc/UTC", "America/New_York"} {
		t.Run(zone, func(t *testing.T) {
			db := testDBWithTimeZone(t, zone)
			ctx := context.Background()
			id := testInstrument(t, db)
			s := NewStore(db, clock.Real{})
			open := time.Date(2026, 11, 20, 16, 0, 0, 0, time.UTC) // 11:00 in New York
			tick := Tick{InstrumentID: id, OpenTime: clock.NewEventTime(open), Open: 100, High: 120, Low: 90, Close: 110, Volume: 5, Provider: "test", IngestedAt: it(time.Now())}

			// Not closed yet at now: refused, nothing written.
			if _, _, err := s.InsertClosed(ctx, Interval4h, tick, open.Add(4*time.Hour-time.Nanosecond)); !errors.Is(err, ErrNativeCandleNotClosed) {
				t.Fatalf("InsertClosed before the close = %v, want ErrNativeCandleNotClosed", err)
			}
			// Closed exactly at its close time: stored, revision 1.
			c, inserted, err := s.InsertClosed(ctx, Interval4h, tick, open.Add(4*time.Hour))
			if err != nil || !inserted || c.Revision != 1 || !c.IsClosed || c.Interval != Interval4h || !c.CloseTime.Time().Equal(open.Add(4*time.Hour)) {
				t.Fatalf("InsertClosed = %+v inserted=%v err=%v", c, inserted, err)
			}
			// The same candle again: nothing written, the stored one returned.
			again, inserted, err := s.InsertClosed(ctx, Interval4h, tick, open.Add(5*time.Hour))
			if err != nil || inserted || again.ID != c.ID {
				t.Fatalf("second InsertClosed = %+v inserted=%v err=%v, want the stored row, not inserted", again, inserted, err)
			}
			// A correction: revision 2, same interval and close time.
			corr, err := s.SupersedeCandleAt(ctx, Interval4h, id, clock.NewEventTime(open), Correction{Open: 100, High: 130, Low: 90, Close: 111, Volume: 6, Provider: "test", IngestedAt: it(time.Now())})
			if err != nil || corr.Revision != 2 || corr.Interval != Interval4h || !corr.CloseTime.Time().Equal(open.Add(4*time.Hour)) {
				t.Fatalf("SupersedeCandleAt = %+v, %v", corr, err)
			}
			if latest, err := s.GetLatestClosedAt(ctx, Interval4h, id); err != nil || latest.ID != corr.ID {
				t.Fatalf("GetLatestClosedAt = %+v, %v; want the correction", latest, err)
			}
			// Misaligned in Go, refused before the database.
			mis := tick
			mis.OpenTime = clock.NewEventTime(open.Add(time.Hour))
			if _, _, err := s.InsertClosed(ctx, Interval4h, mis, open.Add(24*time.Hour)); !errors.Is(err, ErrNotIntervalAligned) {
				t.Fatalf("misaligned InsertClosed = %v, want ErrNotIntervalAligned", err)
			}
			// And by the database itself, in this session's time zone: 4h
			// candles open on 4h boundaries of UTC (epoch modulo), a wrong
			// duration and an unknown interval are rejected.
			raw := func(open, closeT time.Time, interval string) error {
				_, err := db.Exec(`INSERT INTO candles (instrument_id, open_time, close_time, open, high, low, close, volume, revision, is_closed, tick_aligned, provider, ingested_at, interval)
					VALUES ($1, $2, $3, 1, 1, 1, 1, 1, 1, true, true, 'test', now(), $4)`, id, open, closeT, interval)
				return err
			}
			day := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
			if err := raw(day.Add(5*time.Hour), day.Add(9*time.Hour), "4h"); err == nil {
				t.Fatal("a 4h candle opening at 05:00 UTC was accepted")
			}
			if err := raw(day.Add(13*time.Minute), day.Add(28*time.Minute), "15m"); err == nil {
				t.Fatal("a 15m candle opening at :13 was accepted")
			}
			if err := raw(day, day.Add(30*time.Minute), "1h"); err == nil {
				t.Fatal("a 1h candle lasting 30 minutes was accepted")
			}
			if err := raw(day, day.Add(5*time.Minute), "5m"); err == nil {
				t.Fatal("a 5m candle was accepted (5m stays derived, never stored)")
			}
			if err := raw(day.Add(4*time.Hour), day.Add(8*time.Hour), "4h"); err != nil {
				t.Fatalf("an aligned 4h candle at 04:00 UTC was refused: %v", err)
			}
		})
	}
}

// MissingRangesAt steps by the interval and reads only that interval's
// recorded gaps.
func TestIntegration_MissingRangesAt_StepsByTheInterval(t *testing.T) {
	db := testDBWithTimeZone(t, "America/New_York")
	ctx := context.Background()
	id := testInstrument(t, db)
	s := NewStore(db, clock.Real{})
	base := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	far := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for h := 0; h < 10; h++ {
		if h == 3 || h == 4 || h == 7 {
			continue
		}
		if _, _, err := s.InsertClosed(ctx, Interval1h, Tick{InstrumentID: id, OpenTime: clock.NewEventTime(base.Add(time.Duration(h) * time.Hour)), Open: 1, High: 1, Low: 1, Close: 1, Volume: quant.Qty(1), Provider: "test", IngestedAt: it(time.Now())}, far); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.MissingRangesAt(ctx, Interval1h, id, base, base.Add(10*time.Hour), base)
	if err != nil {
		t.Fatal(err)
	}
	want := []MissingRange{{Start: base.Add(3 * time.Hour), End: base.Add(5 * time.Hour), Slots: 2}, {Start: base.Add(7 * time.Hour), End: base.Add(8 * time.Hour), Slots: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MissingRangesAt(1h) = %+v, want %+v", got, want)
	}
	// A recorded 1h gap before lostBefore hides that hole; a 1m gap over the
	// same range does not.
	if _, err := s.RecordGap(ctx, id, clock.NewEventTime(base.Add(7*time.Hour)), clock.NewEventTime(base.Add(8*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.MissingRangesAt(ctx, Interval1h, id, base, base.Add(10*time.Hour), base.Add(10*time.Hour)); len(got) != 2 {
		t.Fatalf("a 1m gap hid a 1h hole: %+v", got)
	}
	if _, err := s.RecordGapAt(ctx, Interval1h, id, clock.NewEventTime(base.Add(7*time.Hour)), clock.NewEventTime(base.Add(8*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.MissingRangesAt(ctx, Interval1h, id, base, base.Add(10*time.Hour), base.Add(10*time.Hour)); len(got) != 1 || got[0].Slots != 2 {
		t.Fatalf("MissingRangesAt after recording the 1h gap = %+v, want only the 03:00 to 05:00 hole", got)
	}
	if n, err := s.CountClosedAt(ctx, Interval1h, id, base, base.Add(10*time.Hour)); err != nil || n != 7 {
		t.Fatalf("CountClosedAt(1h) = %d, %v; want 7", n, err)
	}
	if n, err := s.CountClosed(ctx, id, base, base.Add(10*time.Hour)); err != nil || n != 0 {
		t.Fatalf("CountClosed(1m) = %d, %v; want 0 (only 1h rows exist)", n, err)
	}
}
