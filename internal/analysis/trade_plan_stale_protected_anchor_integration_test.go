//go:build integration

package analysis

import (
	"context"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
)

// TestIntegration_TradePlan_UndefinedWhenOrderBlockBelowStaleProtectedAnchor
// is a minimal golden fixture for a real gap found while investigating
// cmd/argus-worker's "trade plan undefined" warnings against the dev
// fixture dataset (worker-hardening brief, Part A).
//
// Section 7's two protected anchors are deliberately independent and
// are cleared only by invalidateProtectedAnchor, on a CHOCH, for the
// side the CHOCH flips AWAY from -- never for the side a CHOCH flips
// TO. So the moment direction flips bullish, ProtectedLowSwingID is
// whatever it already was, which may predate this exact bullish leg
// entirely and have no price relationship to it. ResolveOrderBlock's
// own search window is bounded by that anchor's PivotTime, a time
// bound, not by its Price -- nothing stops the order block the search
// finds from sitting, in price, below that anchor's own level, even
// though the anchor is conceptually supposed to be the structure's
// floor. When that happens, section 14's entry (the order block's own
// high) ends up below stop (the anchor's price), which
// ResolveTradePlan's own doc comment already calls out as an invariant
// violation it refuses to guess around (ok == false) rather than
// clamp or fabricate.
//
// This is real section 7/12/14 interaction, not a dataset artifact:
// any real CHOCH followed by a fresh order block before a new sweep
// re-establishes that side's own anchor would hit it. Reported per
// CLAUDE.md rule 3 -- not fixed here; a spec/engine change is an
// architect decision.
func TestIntegration_TradePlan_UndefinedWhenOrderBlockBelowStaleProtectedAnchor(t *testing.T) {
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
	if err := store.SetState(ctx, StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
		ReferenceSwingHighID: &h2.ID, ReferenceSwingLowID: &p.ID, ProtectedLowSwingID: &p.ID,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("SetState (reference high): %v", err)
	}
	// B (minute 30): closes above H2 (210) -- a sweep-validated BOS
	// (ProtectedLowSwingID is set), the break ResolveOrderBlock anchors
	// its search to.
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

	// Preconditions, asserted explicitly so a future break in either
	// one fails here with a clear signal, rather than surfacing only
	// as "ResolveTradePlan.ok was unexpectedly true" below.
	ob, obOK, obErr := ResolveOrderBlock(ctx, store, candles, state, asOf)
	if obErr != nil || !obOK || ob.Status != OrderBlockPresent {
		t.Fatalf("ResolveOrderBlock: ok=%v err=%v status=%v (want present)", obOK, obErr, ob.Status)
	}
	sl, slOK, slErr := ResolveStructuralLiquidity(ctx, store, candles, state, asOf, time.Minute, DefaultMinLegSpan)
	if slErr != nil || !slOK || sl.Status != StructuralPresent {
		t.Fatalf("ResolveStructuralLiquidity: ok=%v err=%v status=%v (want present)", slOK, slErr, sl.Status)
	}

	poi, poiErr := ResolvePOI(ctx, store, candles, state, asOf, time.Minute, DefaultMinLegSpan)
	if poiErr != nil {
		t.Fatalf("ResolvePOI: %v", poiErr)
	}
	if poi.Type != POIOrderBlock {
		t.Fatalf("poi.Type = %v, want POIOrderBlock (got the pathological zone)", poi.Type)
	}
	if poi.High != 92 {
		t.Fatalf("poi.High = %v, want 92 (the [78,92] order block)", poi.High)
	}
	// The failure condition this fixture exists to pin down: entry
	// (the order block's own high, the bullish proximal edge) sits
	// BELOW stop (the stale protected anchor's price) -- the exact
	// "entry not strictly beyond stop" case ResolveTradePlan's own doc
	// comment calls an invariant violation, not a legitimate outcome.
	if !(poi.High < p.Price) {
		t.Fatalf("poi.High = %v, p.Price = %v: fixture no longer reproduces entry < stop", poi.High, p.Price)
	}

	plan, ok, err := ResolveTradePlan(ctx, store, candles, state, asOf, time.Minute, DefaultMinLegSpan, 1)
	if err != nil {
		t.Fatalf("ResolveTradePlan: unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("ResolveTradePlan: ok = true, want false (entry %v not beyond stop %v); plan = %+v", plan.Entry, plan.Stop, plan)
	}
}
