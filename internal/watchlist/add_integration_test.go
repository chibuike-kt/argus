//go:build integration

package watchlist

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/tracking"
)

// Phase 4.1a (D15): the audited add, the caps and the reorder, against the
// real schema, in a UTC and a New York session (CLAUDE.md, check 1). Only
// this package creates watched Hyperliquid rows (the tracked set is
// database-wide), and every test removes its watchlist rows afterwards.

var zones = []string{"Etc/UTC", "America/New_York"}

func dbInZone(t *testing.T, zone string) *sql.DB {
	t.Helper()
	dsn := os.Getenv("ARGUS_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://argus:argus@localhost:5432/argus_test?sslmode=disable"
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("postgres", dsn+sep+"options=-c%20timezone%3D"+strings.ReplaceAll(zone, "/", "%2F"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var got string
	if err := db.QueryRowContext(ctx, "SHOW TimeZone").Scan(&got); err != nil {
		t.Skipf("integration database unreachable: %v", err)
	}
	if got != zone {
		t.Fatalf("session time zone %q, want %q", got, zone)
	}
	return db
}

// hlInstrument registers a disabled Hyperliquid perpetual, as the catalog
// refresh does.
func hlInstrument(t *testing.T, db *sql.DB, enabled bool) int64 {
	t.Helper()
	priceScale, qtyScale := quant.Scale(2), quant.Scale(3)
	inst, err := instruments.NewStore(db).Insert(context.Background(), instruments.NewInstrumentParams{
		Provider: "hyperliquid", NativeSymbol: fmt.Sprintf("WLTEST%d", time.Now().UnixNano()), AssetClass: "crypto_perp",
		PriceScale: &priceScale, QtyScale: &qtyScale, TickSize: 1, QtyStepSize: 1, SupportedIntervals: []string{"1m"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		if _, err := db.Exec(`UPDATE instruments SET enabled = TRUE WHERE id = $1`, inst.ID); err != nil {
			t.Fatal(err)
		}
	}
	return inst.ID
}

// userWithCleanup creates a user whose watchlist rows are removed after the
// test, so the database-wide tracked set does not grow run after run.
func userWithCleanup(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	id := testUser(t, db)
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM watchlist_items WHERE user_id = $1`, id) })
	return id
}

func opts(capTracked int) AddOptions {
	return AddOptions{MaxItems: 50, AllowEnable: true, Environment: "local", Floor: []string{}, TrackedCap: capTracked}
}

func trackedCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	tr, err := tracking.NewStore(db).Tracked(context.Background(), []string{})
	if err != nil {
		t.Fatal(err)
	}
	return len(tr)
}

func TestIntegration_Add_EnablesADisabledPerpetualOnceWithAnAuditRecord(t *testing.T) {
	for _, zone := range zones {
		t.Run(zone, func(t *testing.T) {
			db := dbInZone(t, zone)
			ctx := context.Background()
			s := NewStore(db, instruments.NewStore(db))
			user := userWithCleanup(t, db)
			first := hlInstrument(t, db, true)
			if _, err := s.Add(ctx, user, first, opts(1000)); err != nil {
				t.Fatal(err)
			}
			id := hlInstrument(t, db, false)
			before := time.Now().UTC()

			res, err := s.Add(ctx, user, id, opts(1000))
			if err != nil {
				t.Fatal(err)
			}
			if !res.Enabled || res.Already || len(res.Items) != 2 || res.Items[1].InstrumentID != id {
				t.Fatalf("add = %+v, want enabled and appended after the first item", res)
			}
			var enabled bool
			if err := db.QueryRow(`SELECT enabled FROM instruments WHERE id = $1`, id).Scan(&enabled); err != nil || !enabled {
				t.Fatalf("instrument enabled = %v (%v), want true", enabled, err)
			}
			var who sql.NullInt64
			var reason, env string
			var at time.Time
			if err := db.QueryRow(`SELECT user_id, reason, environment, enabled_at FROM instrument_enablements WHERE instrument_id = $1`, id).Scan(&who, &reason, &env, &at); err != nil {
				t.Fatalf("audit row: %v", err)
			}
			if who.Int64 != user || reason != "watchlist add" || env != "local" {
				t.Fatalf("audit = user %v reason %q env %q, want %d, watchlist add, local", who, reason, env, user)
			}
			if at.Before(before.Add(-time.Minute)) || at.After(time.Now().UTC().Add(time.Minute)) {
				t.Fatalf("audit enabled_at %s is not the add's time (%s)", at.UTC(), before)
			}

			// Again: idempotent, nothing enabled, no second audit row.
			again, err := s.Add(ctx, user, id, opts(1000))
			if err != nil || !again.Already || again.Enabled || len(again.Items) != 2 {
				t.Fatalf("second add = %+v %v, want already present", again, err)
			}
			var rows int
			_ = db.QueryRow(`SELECT count(*) FROM instrument_enablements WHERE instrument_id = $1`, id).Scan(&rows)
			if rows != 1 {
				t.Fatalf("%d audit rows, want 1", rows)
			}
		})
	}
}

func TestIntegration_Add_NeverEnablesInProductionOrANonHyperliquidInstrument(t *testing.T) {
	db := dbInZone(t, "Etc/UTC")
	ctx := context.Background()
	s := NewStore(db, instruments.NewStore(db))
	user := userWithCleanup(t, db)

	hl := hlInstrument(t, db, false)
	prod := opts(1000)
	prod.AllowEnable = false
	if _, err := s.Add(ctx, user, hl, prod); !errors.Is(err, ErrInstrumentDisabled) {
		t.Fatalf("production add of a disabled perpetual: err = %v, want ErrInstrumentDisabled", err)
	}
	other := testInstrument(t, db, false) // provider test-provider
	if _, err := s.Add(ctx, user, other, opts(1000)); !errors.Is(err, ErrInstrumentDisabled) {
		t.Fatalf("non-Hyperliquid disabled add: err = %v, want ErrInstrumentDisabled", err)
	}
	var enabledCount, audit, items int
	_ = db.QueryRow(`SELECT count(*) FROM instruments WHERE id IN ($1, $2) AND enabled`, hl, other).Scan(&enabledCount)
	_ = db.QueryRow(`SELECT count(*) FROM instrument_enablements WHERE instrument_id IN ($1, $2)`, hl, other).Scan(&audit)
	_ = db.QueryRow(`SELECT count(*) FROM watchlist_items WHERE user_id = $1`, user).Scan(&items)
	if enabledCount != 0 || audit != 0 || items != 0 {
		t.Fatalf("after refused adds: %d enabled, %d audit rows, %d items; want all 0", enabledCount, audit, items)
	}
	if _, err := s.Add(ctx, user, 9_000_000_000, opts(1000)); !errors.Is(err, ErrInstrumentNotFound) {
		t.Fatalf("unknown instrument: err = %v", err)
	}
}

func TestIntegration_Add_PerUserCap(t *testing.T) {
	db := dbInZone(t, "Etc/UTC")
	ctx := context.Background()
	s := NewStore(db, instruments.NewStore(db))
	user := userWithCleanup(t, db)
	o := opts(1000)
	o.MaxItems = 2
	for i := 0; i < 2; i++ {
		if _, err := s.Add(ctx, user, testInstrument(t, db, true), o); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Add(ctx, user, testInstrument(t, db, true), o); !errors.Is(err, ErrTooManyItems) {
		t.Fatalf("third add with a cap of 2: err = %v, want ErrTooManyItems", err)
	}
}

// Two users race to add two different new symbols when one tracked slot is
// left: exactly one wins, every time. A symbol already tracked can always
// be added.
func TestIntegration_Add_TrackedCap_ConcurrentAddsCannotBothPass(t *testing.T) {
	for _, zone := range zones {
		t.Run(zone, func(t *testing.T) {
			db := dbInZone(t, zone)
			ctx := context.Background()
			s := NewStore(db, instruments.NewStore(db))
			for round := 0; round < 10; round++ {
				capNow := trackedCount(t, db) + 1
				a, b := hlInstrument(t, db, false), hlInstrument(t, db, false)
				ua, ub := userWithCleanup(t, db), userWithCleanup(t, db)
				var wg sync.WaitGroup
				start := make(chan struct{})
				errs := make([]error, 2)
				for i, pair := range [][2]int64{{ua, a}, {ub, b}} {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						_, errs[i] = s.Add(ctx, pair[0], pair[1], opts(capNow))
					}()
				}
				close(start)
				wg.Wait()
				ok, capped := 0, 0
				for _, err := range errs {
					switch {
					case err == nil:
						ok++
					case errors.Is(err, ErrTrackedCapReached):
						capped++
					default:
						t.Fatalf("round %d: unexpected error %v", round, err)
					}
				}
				if ok != 1 || capped != 1 {
					t.Fatalf("round %d: %d adds passed and %d were capped with one slot left, want exactly 1 and 1", round, ok, capped)
				}
				// The winner's symbol is tracked now: another user can add it.
				winner := a
				if errs[0] != nil {
					winner = b
				}
				if _, err := s.Add(ctx, userWithCleanup(t, db), winner, opts(capNow)); err != nil {
					t.Fatalf("round %d: adding an already tracked symbol at the cap: %v", round, err)
				}
				// Free the slot for the next round.
				_, _ = db.Exec(`DELETE FROM watchlist_items WHERE instrument_id IN ($1, $2)`, a, b)
			}
		})
	}
}

func TestIntegration_Reorder_OnlyAPermutationOfTheCurrentList(t *testing.T) {
	db := dbInZone(t, "America/New_York")
	ctx := context.Background()
	s := NewStore(db, instruments.NewStore(db))
	user := userWithCleanup(t, db)
	ids := []int64{testInstrument(t, db, true), testInstrument(t, db, true), testInstrument(t, db, true)}
	for _, id := range ids {
		if _, err := s.Add(ctx, user, id, opts(1000)); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.Reorder(ctx, user, []int64{ids[2], ids[0], ids[1]})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].InstrumentID != ids[2] || items[1].InstrumentID != ids[0] || items[2].InstrumentID != ids[1] {
		t.Fatalf("order = %+v", items)
	}
	extra := testInstrument(t, db, true)
	if _, err := s.Add(ctx, user, extra, opts(1000)); err != nil {
		t.Fatal(err)
	}
	// The list is now ids[2], ids[0], ids[1], extra. Every refused order
	// below except the first two has the right length, so it is the per-id
	// check that refuses it, not the count.
	bad := map[string][]int64{
		"missing one":               {ids[0], ids[1], extra},
		"stale after add":           {ids[0], ids[1], ids[2]}, // another tab added extra meanwhile
		"a stranger, right length":  {ids[0], ids[1], ids[2], 9_000_000_000},
		"a duplicate, right length": {ids[0], ids[0], ids[1], extra},
	}
	for name, order := range bad {
		if _, err := s.Reorder(ctx, user, order); !errors.Is(err, ErrOrderMismatch) {
			t.Errorf("%s: err = %v, want ErrOrderMismatch", name, err)
		}
	}
	now, _ := s.List(ctx, user)
	if len(now) != 4 || now[0].InstrumentID != ids[2] || now[3].InstrumentID != extra {
		t.Fatalf("a refused reorder changed the list: %+v", now)
	}
}

func TestIntegration_TrackingState_NeedsAttentionIsNeverOverwrittenByThePipeline(t *testing.T) {
	db := dbInZone(t, "America/New_York")
	ctx := context.Background()
	ts := tracking.NewStore(db)
	id := hlInstrument(t, db, true)
	at := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC) // inside the New York DST change
	if err := ts.SetState(ctx, id, tracking.Live, "", 0, at); err != nil {
		t.Fatal(err)
	}
	if err := ts.SetState(ctx, id, tracking.NeedsAttention, "precision changed", 0, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := ts.SetState(ctx, id, tracking.Live, "", 0, at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	st, err := ts.States(ctx, []int64{id})
	if err != nil {
		t.Fatal(err)
	}
	got := st[id]
	if got.State != tracking.NeedsAttention || got.Reason != "precision changed" || !got.UpdatedAt.Equal(at.Add(time.Minute)) || got.UpdatedAt.Location() != time.UTC {
		t.Fatalf("state = %+v, want needs_attention kept, at %s in UTC", got, at.Add(time.Minute))
	}
}

// Architect review of 4.1a, item 3: clear-attention removes only a
// needs_attention state, only for that Hyperliquid symbol; afterwards the
// pipeline's states are recorded again.
func TestIntegration_TrackingState_ClearNeedsAttention(t *testing.T) {
	for _, zone := range []string{"Etc/UTC", "America/New_York"} {
		t.Run(zone, func(t *testing.T) {
			db := dbInZone(t, zone)
			ctx := context.Background()
			ts := tracking.NewStore(db)
			id := hlInstrument(t, db, true)
			other := hlInstrument(t, db, true)
			var sym string
			if err := db.QueryRow(`SELECT native_symbol FROM instruments WHERE id = $1`, id).Scan(&sym); err != nil {
				t.Fatal(err)
			}
			at := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)
			if err := ts.SetState(ctx, id, tracking.NeedsAttention, "precision changed", 0, at); err != nil {
				t.Fatal(err)
			}
			if err := ts.SetState(ctx, other, tracking.NeedsAttention, "other", 0, at); err != nil {
				t.Fatal(err)
			}
			prev, err := ts.ClearNeedsAttention(ctx, sym)
			if err != nil {
				t.Fatal(err)
			}
			if prev.InstrumentID != id || prev.Reason != "precision changed" || !prev.UpdatedAt.Equal(at) || prev.UpdatedAt.Location() != time.UTC {
				t.Fatalf("cleared %+v, want the symbol's own state at %s in UTC", prev, at)
			}
			if _, err := ts.ClearNeedsAttention(ctx, sym); !errors.Is(err, tracking.ErrNoAttention) {
				t.Fatalf("second clear err = %v, want ErrNoAttention", err)
			}
			if err := ts.SetState(ctx, id, tracking.Live, "", 0, at.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			st, err := ts.States(ctx, []int64{id, other})
			if err != nil {
				t.Fatal(err)
			}
			if st[id].State != tracking.Live || st[other].State != tracking.NeedsAttention {
				t.Fatalf("states %+v, want the cleared symbol live again and the other still needs_attention", st)
			}
			// A live symbol is not cleared (nothing to clear).
			if _, err := ts.ClearNeedsAttention(ctx, sym); !errors.Is(err, tracking.ErrNoAttention) {
				t.Fatalf("clearing a live symbol err = %v, want ErrNoAttention", err)
			}
		})
	}
}

// Architect review of 4.1a, items 5 and 6: Replace (PUT /watchlist) applies
// the same tracked cap as Add, counting the configured floor as tracked,
// writes nothing when refused, and says how many more can be added.
func TestIntegration_Replace_AppliesTheTrackedCap(t *testing.T) {
	for _, zone := range zones {
		t.Run(zone, func(t *testing.T) {
			db := dbInZone(t, zone)
			ctx := context.Background()
			s := NewStore(db, instruments.NewStore(db))
			u := userWithCleanup(t, db)
			a, b, c := hlInstrument(t, db, true), hlInstrument(t, db, true), hlInstrument(t, db, true)
			list := func() []int64 {
				items, err := s.List(ctx, u)
				if err != nil {
					t.Fatal(err)
				}
				out := make([]int64, len(items))
				for i, it := range items {
					out[i] = it.InstrumentID
				}
				return out
			}
			base := trackedCount(t, db)
			one := TrackedCapOptions{Floor: []string{}, Max: base + 1}
			if _, err := s.Replace(ctx, u, []int64{a}, 50, one); err != nil {
				t.Fatalf("a list within the cap was refused: %v", err)
			}
			// Two new symbols with no slot left: refused, the list unchanged.
			_, err := s.Replace(ctx, u, []int64{a, b, c}, 50, one)
			var ce *TrackedCapError
			if !errors.Is(err, ErrTrackedCapReached) || !errors.As(err, &ce) {
				t.Fatalf("err = %v, want a TrackedCapError", err)
			}
			if ce.Adding != 2 || ce.Tracked != base+1 || ce.Remaining() != 0 || !strings.Contains(ce.Error(), "no more symbols can be added") {
				t.Fatalf("cap error %+v (%q), want adding 2, tracked %d, none remaining", ce, ce.Error(), base+1)
			}
			if got := list(); len(got) != 1 || got[0] != a {
				t.Fatalf("a refused replace changed the list to %v", got)
			}
			// Swapping a for b frees a's slot: allowed.
			if _, err := s.Replace(ctx, u, []int64{b}, 50, one); err != nil {
				t.Fatalf("swapping one tracked symbol for another was refused: %v", err)
			}

			// The floor counts as tracked: with a floor symbol on nobody's
			// list, a cap that would fit c without the floor refuses it.
			f := hlInstrument(t, db, false)
			var floorSym string
			if err := db.QueryRow(`SELECT native_symbol FROM instruments WHERE id = $1`, f).Scan(&floorSym); err != nil {
				t.Fatal(err)
			}
			withFloor := TrackedCapOptions{Floor: []string{floorSym}, Max: base + 2}
			_, err = s.Replace(ctx, u, []int64{b, c}, 50, withFloor)
			if !errors.As(err, &ce) || ce.Floor != 1 || ce.Tracked != base+2 || ce.Remaining() != 0 {
				t.Fatalf("err = %v, want refused with the floor symbol counted as tracked", err)
			}
			if got := list(); len(got) != 1 || got[0] != b {
				t.Fatalf("a refused replace changed the list to %v", got)
			}

			// Add refuses c the same way, with the same numbers (item 6).
			addOpt := opts(base + 2)
			addOpt.Floor = []string{floorSym}
			_, err = s.Add(ctx, u, c, addOpt)
			if !errors.As(err, &ce) || ce.Floor != 1 || ce.Tracked != base+2 || ce.Adding != 1 || !strings.Contains(ce.Error(), "no more symbols can be added") {
				t.Fatalf("add err = %v, want a TrackedCapError counting the floor", err)
			}
		})
	}
}
