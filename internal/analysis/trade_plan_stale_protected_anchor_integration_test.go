//go:build integration

package analysis

import (
	"context"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
)

// TestIntegration_TradePlan_StaleProtectedAnchorClearedByCloseThrough
// is the worker-hardening brief's own golden fixture (Part A), flipped
// for spec v0.12.0 (batch 8): the fixture data is unchanged, but the
// gap it used to reproduce -- ProtectedLowSwingID surviving as a stale
// anchor because invalidateProtectedAnchor only clears the side a
// CHOCH flips AWAY from, never re-validated against price -- is now
// closed by applyCloseThroughInvalidation. The order-block candle at
// minute 15 closes at 80, strictly below P's own price (100): that
// close-through clears ProtectedLowSwingID before the BOS at minute 30
// ever runs, so the BOS is no longer sweep-validated,
// ResolveOrderBlock finds nothing to search for, and ResolveTradePlan
// resolves PlanNone legitimately (ok == true) instead of the
// entry-not-beyond-stop invariant violation this fixture used to prove.
func TestIntegration_TradePlan_StaleProtectedAnchorClearedByCloseThrough(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)
	ctx := context.Background()

	// H1 (minute 2, price 200): the structural-liquidity candidate's
	// preceding opposite swing only -- not the reference high used
	// for the break below.
	seedSwing(t, store, instrumentID, SwingHigh, 2, 200, []int64{1, 2, 3, 4, 5})
	// A real candle at minute 10, purely so the synthetic sweep below
	// has a valid trigger_candle_id to reference (a real FK) -- its
	// own OHLC values are not otherwise read by anything in this test.
	ids := feedCloseCandles(t, db, instrumentID, []closeBar{{10, 105, 95, 100}})
	// P (minute 10, price 100): the STALE protected low. In the real
	// run this predated the bullish leg's own CHOCH; here it is simply
	// seeded directly, since reproducing an actual CHOCH is not needed
	// to show the downstream effect.
	p := seedSwing(t, store, instrumentID, SwingLow, 10, 100, []int64{9, 10, 11, 12, 13})
	// A sweep at P's own pivot_time/side: decideSweepValidation (the
	// mechanism that marks a break "sweep-validated") looks one up by
	// exact (pivot_time, side), not merely by ProtectedLowSwingID
	// being set -- ResolveOrderBlock requires a sweep-VALIDATED break,
	// so without this row the break below would not validate and the
	// order block search would never run at all.
	if _, err := store.InsertSweep(ctx, NewSweep{
		InstrumentID: instrumentID, Timeframe: "1m", Side: SweepLow, SweptSwingID: p.ID,
		TriggerOpenTime: p.PivotTime.Time(), TriggerCandleID: ids[10],
		PenetrationExtreme: 100, ReclaimClose: 105,
		EvaluationTime: clock.NewEvaluationTime(time.Now().UTC()), AlgorithmVersion: testAlgorithmVersion,
	}); err != nil {
		t.Fatalf("InsertSweep: %v", err)
	}

	if err := store.SetState(ctx, StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
		ReferenceSwingLowID: &p.ID, ProtectedLowSwingID: &p.ID,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	// The order block candidate: bearish, zone [78,92] -- entirely
	// BELOW P's price (100), the pathological relationship. Placed
	// within [P.PivotTime, B.OpenTime), the only window
	// ResolveOrderBlock's search ever bounds itself by.
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{15, 90, 92, 78, 80},
	})
	if err := e.EvaluateRange(ctx, instrumentID, "1m", et(15), et(16)); err != nil {
		t.Fatalf("EvaluateRange (order block candle): %v", err)
	}

	// The close-through assertion this fixture exists to pin down:
	// minute 15's close (80) is strictly below P's own price (100), so
	// applyCloseThroughInvalidation must have cleared ProtectedLowSwingID
	// during that EvaluateRange call above -- before the BOS candle
	// below ever runs, not as a side effect of it.
	afterOB, err := store.GetState(ctx, instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState (after order block candle): %v", err)
	}
	if afterOB.ProtectedLowSwingID != nil {
		t.Fatalf("ProtectedLowSwingID = %v, want nil: close-through invalidation (close 80 < P.Price 100) did not clear it", *afterOB.ProtectedLowSwingID)
	}

	// SL (minute 18, price 110, confirmed minute 20): the structural-
	// liquidity candidate. Pivots AFTER the order block's own dip
	// (minute 15) specifically so its own intactness scan -- which
	// starts at its confirmation, not its pivot -- never sees that
	// dip; this is a different swing from P on purpose; reusing P
	// itself as structural liquidity (as a first draft of this fixture
	// did) fails for the opposite reason: the dip breaches ITS
	// intactness instead, which is not the gap this fixture is for.
	seedSwing(t, store, instrumentID, SwingLow, 18, 110, []int64{17, 18, 19, 20, 21})

	// H2 (minute 25, price 210): SL's following opposite swing AND
	// the reference high the break below targets.
	h2 := seedSwing(t, store, instrumentID, SwingHigh, 25, 210, []int64{24, 25, 26, 27, 28})
	// Built from afterOB, not a fresh literal: ReferenceSwingHighID is
	// the only field this step adds. ProtectedLowSwingID stays nil --
	// carrying forward the close-through clear above, not resetting it,
	// is the whole point of this fixture now.
	afterOB.ReferenceSwingHighID = &h2.ID
	if err := store.SetState(ctx, afterOB, time.Now().UTC()); err != nil {
		t.Fatalf("SetState (reference high): %v", err)
	}
	// B (minute 30): closes above H2 (210) -- a BOS, but no longer
	// sweep-validated: ProtectedLowSwingID is nil going in, so
	// decideSweepValidation has nothing to validate against.
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{30, 225, 215, 220},
	})
	if err := e.EvaluateRange(ctx, instrumentID, "1m", et(30), et(31)); err != nil {
		t.Fatalf("EvaluateRange (BOS candle): %v", err)
	}

	// as-of 31, not 30: the trigger candle's own close_time is 31 (a
	// 1-minute candle opening at 30 closes at 31), and
	// LatestSweepValidatedEventKnownAsOf (ResolveOrderBlock's own
	// precondition) bounds itself by close_time <= asOf, the same
	// point-in-time discipline every other resolver in this package
	// uses -- asOf 30 found nothing yet on a first pass at this
	// fixture, for exactly that reason.
	asOf := et(31)
	state, err := store.GetState(ctx, instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}

	// The BOS is no longer sweep-validated (ProtectedLowSwingID was
	// cleared above, before this candle ran), so ResolveOrderBlock's own
	// precondition -- a sweep-validated event known as of asOf -- is no
	// longer met. This is the fix in effect, not a precondition the
	// fixture happens to still satisfy: before batch 8 this call found
	// the pathological [78,92] zone.
	ob, obOK, obErr := ResolveOrderBlock(ctx, store, candles, state, asOf)
	if obErr != nil {
		t.Fatalf("ResolveOrderBlock: unexpected error: %v", obErr)
	}
	if obOK {
		t.Fatalf("ResolveOrderBlock: ok = true, status = %v (want not found: the BOS is no longer sweep-validated)", ob.Status)
	}

	poi, poiErr := ResolvePOI(ctx, store, candles, state, asOf, time.Minute, DefaultMinLegSpan)
	if poiErr != nil {
		t.Fatalf("ResolvePOI: %v", poiErr)
	}
	if poi.Type != POINone {
		t.Fatalf("poi.Type = %v, want POINone (no sweep-validated event to search from)", poi.Type)
	}

	// The fix this fixture now proves: no invariant violation, a
	// legitimate PlanNone (there is no POI at all, let alone one with
	// entry below stop).
	plan, ok, err := ResolveTradePlan(ctx, store, candles, state, asOf, time.Minute, DefaultMinLegSpan, 1)
	if err != nil {
		t.Fatalf("ResolveTradePlan: unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("ResolveTradePlan: ok = false, want true (close-through should have pre-empted the stale anchor entirely); plan = %+v", plan)
	}
	if plan.Status != PlanNone {
		t.Fatalf("plan.Status = %v, want PlanNone; plan = %+v", plan.Status, plan)
	}
}
