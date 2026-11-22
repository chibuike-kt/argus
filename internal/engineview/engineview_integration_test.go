//go:build integration

package engineview

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/setups"

	// lib/pq registers the "postgres" driver.
	_ "github.com/lib/pq"
)

// These run against the real schema (db/migrations): the window and
// keyset queries are SQL, and a fake cannot show that a page boundary
// neither overlaps nor skips a row. Each test runs twice: with the
// session time zone UTC and America/New_York (CLAUDE.md, check 1).

func dsns(t *testing.T) map[string]string {
	t.Helper()
	dsn := os.Getenv("ARGUS_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://argus:argus@localhost:5432/argus?sslmode=disable"
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return map[string]string{
		"utc":      dsn + sep + "timezone=Etc/UTC",
		"new_york": dsn + sep + "timezone=America/New_York",
	}
}

func openDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("integration database unreachable: %v", err)
	}
	return db
}

func newInstrument(t *testing.T, db *sql.DB, enabled bool) int64 {
	t.Helper()
	scale := quant.Scale(2)
	inst, err := instruments.NewStore(db).Insert(context.Background(), instruments.NewInstrumentParams{
		Provider: "test-provider", NativeSymbol: fmt.Sprintf("EV-%d", time.Now().UnixNano()), AssetClass: "crypto_perp",
		PriceScale: &scale, QtyScale: &scale, TickSize: 1, QtyStepSize: 1, SupportedIntervals: []string{"1m"},
	})
	if err != nil {
		t.Fatalf("instrument: %v", err)
	}
	if enabled {
		if _, err := db.Exec(`UPDATE instruments SET enabled = TRUE WHERE id = $1`, inst.ID); err != nil {
			t.Fatal(err)
		}
	}
	return inst.ID
}

var base = time.Date(2026, 11, 21, 23, 0, 0, 0, time.UTC)

func minute(m int) time.Time { return base.Add(time.Duration(m) * time.Minute) }

func insertCandle(t *testing.T, db *sql.DB, inst int64, m int) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(`INSERT INTO candles (instrument_id, open_time, close_time, open, high, low, close, volume, revision, is_closed, tick_aligned, provider, ingested_at)
		VALUES ($1, $2, $3, 100, 110, 90, 105, 1, 1, true, true, 'test', now()) RETURNING id`, inst, minute(m), minute(m+1)).Scan(&id)
	if err != nil {
		t.Fatalf("candle %d: %v", m, err)
	}
	return id
}

func insertSwing(t *testing.T, db *sql.DB, inst int64, typ string, m int, price int64) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(`INSERT INTO confirmed_swings (instrument_id, timeframe, swing_type, pivot_time, price, confirmed_at, evaluation_time, algorithm_version, source_candle_ids)
		VALUES ($1, '1m', $2, $3, $4, $5, $5, 'test', '{}') RETURNING id`, inst, typ, minute(m), price, minute(m+3)).Scan(&id)
	if err != nil {
		t.Fatalf("swing %s %d: %v", typ, m, err)
	}
	return id
}

func TestIntegration_StructureWindow_PagesNeverOverlapOrSkip(t *testing.T) {
	for zone, dsn := range dsns(t) {
		t.Run(zone, func(t *testing.T) {
			db := openDB(t, dsn)
			ctx := context.Background()
			inst := newInstrument(t, db, true)

			wantSwings := map[int64]bool{}
			wantBreaks := map[int64]bool{}
			wantSweeps := map[int64]bool{}
			candles := map[int]int64{}
			for m := 0; m < 60; m++ {
				candles[m] = insertCandle(t, db, inst, m)
			}
			// Swings every minute, alternating; at minute 20 a high AND a
			// low share the pivot time (the case a page cut must keep
			// together).
			swingAt := map[int]int64{}
			for m := 0; m < 50; m++ {
				typ := "high"
				if m%2 == 1 {
					typ = "low"
				}
				id := insertSwing(t, db, inst, typ, m, int64(10000+m))
				swingAt[m] = id
				wantSwings[id] = true
			}
			id := insertSwing(t, db, inst, "low", 20, 9000)
			wantSwings[id] = true

			// A superseded swing is never returned.
			old := insertSwing(t, db, inst, "high", 55, 1)
			if _, err := db.Exec(`UPDATE confirmed_swings SET superseded_at = now() WHERE id = $1`, old); err != nil {
				t.Fatal(err)
			}

			// A sweep and a break every other minute.
			for m := 1; m < 50; m += 2 {
				var sid int64
				if err := db.QueryRow(`INSERT INTO liquidity_sweeps (instrument_id, timeframe, side, swept_swing_id, trigger_open_time, trigger_candle_id, penetration_extreme, reclaim_close, evaluation_time, algorithm_version)
					VALUES ($1, '1m', 'buy', $2, $3, $4, 120, 101, now(), 'test') RETURNING id`, inst, swingAt[m-1], minute(m), candles[m]).Scan(&sid); err != nil {
					t.Fatal(err)
				}
				wantSweeps[sid] = true
				var bid int64
				if err := db.QueryRow(`INSERT INTO structure_events (instrument_id, timeframe, event_type, direction_before, direction_after, trigger_open_time, trigger_candle_id, reference_swing_ids, evaluation_time, algorithm_version)
					VALUES ($1, '1m', 'bos', 'bullish', 'bullish', $2, $3, ARRAY[$4::bigint], now(), 'test') RETURNING id`, inst, minute(m), candles[m], swingAt[m-1]).Scan(&bid); err != nil {
					t.Fatal(err)
				}
				wantBreaks[bid] = true
			}

			s := NewStore(db)
			from, to := minute(0), minute(60)
			gotSwings, gotBreaks, gotSweeps := map[int64]int{}, map[int64]int{}, map[int64]int{}
			pages := 0
			for {
				w, err := s.StructureWindow(ctx, inst, "1m", from, to, 10)
				if err != nil {
					t.Fatal(err)
				}
				pages++
				for _, x := range w.Swings {
					gotSwings[x.ID]++
					if !x.PivotTime.Before(w.To) || x.PivotTime.Before(from) || x.PivotTime.Location() != time.UTC {
						t.Fatalf("swing %d at %s outside its page [%s, %s) or not UTC", x.ID, x.PivotTime, from, w.To)
					}
				}
				for _, x := range w.Breaks {
					gotBreaks[x.ID]++
					if x.BrokenSwing == nil || x.BrokenSwing.SwingID == 0 {
						t.Fatalf("bos %d has no broken swing", x.ID)
					}
				}
				for _, x := range w.Sweeps {
					gotSweeps[x.ID]++
					if x.Swept.SwingID == 0 || x.PenetrationExtreme != 120 {
						t.Fatalf("sweep %+v lost its swept swing or prices", x)
					}
				}
				if w.NextFrom == nil {
					break
				}
				if !w.NextFrom.After(from) {
					t.Fatalf("page did not advance: next %s, from %s", w.NextFrom, from)
				}
				from = *w.NextFrom
				if pages > 50 {
					t.Fatal("paging did not end")
				}
			}
			check := func(kind string, want map[int64]bool, got map[int64]int) {
				for id := range want {
					if got[id] != 1 {
						t.Errorf("%s %d returned %d times, want exactly once", kind, id, got[id])
					}
				}
				for id := range got {
					if !want[id] {
						t.Errorf("%s %d returned but not expected (superseded or another instrument)", kind, id)
					}
				}
			}
			check("swing", wantSwings, gotSwings)
			check("break", wantBreaks, gotBreaks)
			check("sweep", wantSweeps, gotSweeps)
			if pages < 4 {
				t.Fatalf("%d pages for 51 swings at limit 10, want several", pages)
			}
		})
	}
}

func plan(candleID int64, status analysis.PlanStatus) analysis.TradePlan {
	return analysis.TradePlan{
		Status: status, Direction: analysis.DirectionBullish, Entry: 90, Stop: 70,
		POI: analysis.POI{Type: analysis.POIOrderBlock, Direction: analysis.DirectionBullish, Low: 80, High: 90, OrderBlockCandleID: candleID, OrderBlockCandleRevision: 1, InDiscount: true},
	}
}

func TestIntegration_Activity_KeysetCoversEveryRowOnce(t *testing.T) {
	for zone, dsn := range dsns(t) {
		t.Run(zone, func(t *testing.T) {
			db := openDB(t, dsn)
			ctx := context.Background()
			inst := newInstrument(t, db, true)
			other := newInstrument(t, db, true)

			// Three setups, each created straight into triggered inside one
			// transaction: detected and triggered share created_at (the
			// real worker's case), so only (kind, id) orders them.
			want := map[string]bool{}
			for i := 0; i < 3; i++ {
				cid := insertCandle(t, db, inst, i)
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				st := setups.NewStore(db).WithTx(tx)
				s, _, err := setups.ReconcileWithMode(ctx, st, inst, "1m", plan(cid, analysis.PlanTriggered), "v-test", minute(i+1), time.Now(), setups.FoundReplay)
				if err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				evs, err := setups.NewStore(db).ListEvents(ctx, s.ID)
				if err != nil || len(evs) != 2 {
					t.Fatalf("events %+v err %v, want detected and triggered", evs, err)
				}
				for _, e := range evs {
					want[fmt.Sprintf("setup:%d", e.ID)] = true
				}
			}
			// A structure event, a resolved gap, an open gap (never listed),
			// and a row on another instrument (never listed).
			c10 := insertCandle(t, db, inst, 10)
			sw := insertSwing(t, db, inst, "high", 9, 100)
			var bid int64
			if err := db.QueryRow(`INSERT INTO structure_events (instrument_id, timeframe, event_type, direction_before, direction_after, trigger_open_time, trigger_candle_id, reference_swing_ids, evaluation_time, algorithm_version)
				VALUES ($1, '1m', 'choch', 'bearish', 'bullish', $2, $3, ARRAY[$4::bigint], now(), 'test') RETURNING id`, inst, minute(10), c10, sw).Scan(&bid); err != nil {
				t.Fatal(err)
			}
			want[fmt.Sprintf("structure:%d", bid)] = true
			var gid int64
			if err := db.QueryRow(`INSERT INTO candle_gaps (instrument_id, gap_start, gap_end, detected_at, resolved_at, resolution) VALUES ($1, $2, $3, now(), now(), 'backfilled') RETURNING id`,
				inst, minute(30), minute(33)).Scan(&gid); err != nil {
				t.Fatal(err)
			}
			want[fmt.Sprintf("gap:%d", gid)] = true
			if _, err := db.Exec(`INSERT INTO candle_gaps (instrument_id, gap_start, gap_end, detected_at) VALUES ($1, $2, $3, now())`, inst, minute(40), minute(41)); err != nil {
				t.Fatal(err)
			}
			oc := insertCandle(t, db, other, 0)
			osw := insertSwing(t, db, other, "low", 0, 1)
			if _, err := db.Exec(`INSERT INTO structure_events (instrument_id, timeframe, event_type, direction_before, direction_after, trigger_open_time, trigger_candle_id, reference_swing_ids, evaluation_time, algorithm_version)
				VALUES ($1, '1m', 'bos', 'bullish', 'bullish', $2, $3, ARRAY[$4::bigint], now(), 'test')`, other, minute(0), oc, osw); err != nil {
				t.Fatal(err)
			}

			s := NewStore(db)
			got := map[string]int{}
			var before *Cursor
			var prev *ActivityItem
			for pages := 0; ; pages++ {
				items, next, err := s.Activity(ctx, ActivityQuery{InstrumentIDs: []int64{inst}, Setups: true, Structure: true, Gaps: true, Before: before, Limit: 1})
				if err != nil {
					t.Fatal(err)
				}
				for i := range items {
					it := items[i]
					got[fmt.Sprintf("%s:%d", it.Kind, it.ID)]++
					if it.InstrumentID != inst {
						t.Fatalf("row of instrument %d listed", it.InstrumentID)
					}
					if prev != nil && it.At.After(prev.At) {
						t.Fatalf("not newest first: %s after %s", it.At, prev.At)
					}
					if it.Kind == KindSetup && (it.SetupID == nil || it.FoundMode != "replay") {
						t.Fatalf("setup row %+v lacks its setup id or found mode", it)
					}
					prev = &it
				}
				if next == nil {
					break
				}
				// The cursor survives its own wire encoding.
				c, err := DecodeCursor(next.Encode())
				if err != nil {
					t.Fatal(err)
				}
				before = &c
				if pages > 20 {
					t.Fatal("paging did not end")
				}
			}
			for k := range want {
				if got[k] != 1 {
					t.Errorf("%s listed %d times, want once", k, got[k])
				}
			}
			for k := range got {
				if !want[k] {
					t.Errorf("%s listed but not expected (an open gap or another instrument)", k)
				}
			}

			// Kind filter: structure only.
			items, _, err := s.Activity(ctx, ActivityQuery{InstrumentIDs: []int64{inst}, Structure: true, Limit: 50})
			if err != nil || len(items) != 1 || items[0].Kind != KindStructure || items[0].Detail != "choch" || items[0].Direction != "bullish" {
				t.Fatalf("structure-only = %+v err %v", items, err)
			}
		})
	}
}

func TestIntegration_EngineStatus_FromTheCursor(t *testing.T) {
	for zone, dsn := range dsns(t) {
		t.Run(zone, func(t *testing.T) {
			db := openDB(t, dsn)
			ctx := context.Background()
			never := newInstrument(t, db, true)
			running := newInstrument(t, db, true)
			disabled := newInstrument(t, db, false)
			for m := 0; m < 6; m++ {
				insertCandle(t, db, running, m)
			}
			// Evaluated through minute 3's close (minute(3) is the close of
			// the candle opening at minute 2): candles 3, 4, 5 are behind.
			if _, err := db.Exec(`INSERT INTO processing_cursors (instrument_id, timeframe, last_processed_close_time, updated_at) VALUES ($1, '1m', $2, $3)`,
				running, minute(3), minute(3).Add(28*time.Second)); err != nil {
				t.Fatal(err)
			}
			rows, err := NewStore(db).EngineStatus(ctx)
			if err != nil {
				t.Fatal(err)
			}
			by := map[int64]InstrumentStatus{}
			for _, r := range rows {
				by[r.InstrumentID] = r
			}
			if _, ok := by[disabled]; ok {
				t.Fatal("a disabled instrument is listed")
			}
			n, ok := by[never]
			if !ok || n.Analysed || n.EvaluatedThrough != nil || n.LatestClosedCloseTime != nil {
				t.Fatalf("never analysed = %+v", n)
			}
			r := by[running]
			if !r.Analysed || r.Timeframe != "1m" || !r.EvaluatedThrough.Equal(minute(3)) || !r.LastEvaluatedAt.Equal(minute(3).Add(28*time.Second)) {
				t.Fatalf("running = %+v", r)
			}
			if !r.LatestClosedCloseTime.Equal(minute(6)) || r.ClosedCandlesBehind != 3 {
				t.Fatalf("latest %s behind %d, want %s and 3", r.LatestClosedCloseTime, r.ClosedCandlesBehind, minute(6))
			}
			if s, ok := r.BehindSeconds(); !ok || s != 180 {
				t.Fatalf("behind %ds, want 180", s)
			}
			if r.EvaluatedThrough.Location() != time.UTC {
				t.Fatal("times must be UTC whatever the session zone")
			}
		})
	}
}
