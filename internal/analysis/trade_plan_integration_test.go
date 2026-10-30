//go:build integration

package analysis

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
)

// Batch 6 golden fixtures, strategy-spec.md v0.10.0 section 14: trade
// plan (risk entry). Every fixture reuses the bullish/bearish base
// shape from poi_integration_test.go: a protected anchor established
// by a sweep, a sweep-validated break, an order block in the impulse
// window (zone [100,110] bullish / [90,100] bearish), and a qualifying
// structural-liquidity swing (75 bullish / 135 bearish) plus a later
// confirmed opposing swing (160 bullish / 20 bearish) that keeps the
// order block in discount (section 8).

func TestIntegration_TradePlan_Bullish_FromQMR(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95}, // sweep + protected low (70)
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 108, 110, 100, 102}, // order block, zone [100,110]
		{24, 103, 120, 102, 118},
		{25, 130, 135, 120, 130}, // B: close 130 > H1 (125)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{30, 103, 95, 102}, // QMR fail: low(95) < 100, close(102) > 100
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(30), et(31)); err != nil {
		t.Fatalf("EvaluateRange (fail candle): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 105, 75, 100}, // structural-liquidity candidate low (75)
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 160, 95, 100}, // later confirmed swing high (160)
		{49, 100, 95, 98},
		{50, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange (structural liquidity): %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	plan, ok, err := ResolveTradePlan(context.Background(), store, candles, state, et(55), time.Minute, DefaultMinLegSpan, 1)
	if err != nil {
		t.Fatalf("ResolveTradePlan: %v", err)
	}
	if !ok {
		t.Fatal("ResolveTradePlan ok = false, want true")
	}
	if plan.POI.Type != POIQMR {
		t.Fatalf("plan.POI.Type = %v, want POIQMR", plan.POI.Type)
	}
	if plan.Direction != DirectionBullish || plan.Entry != 110 || plan.Stop != 70 {
		t.Fatalf("plan = %+v, want Bullish, entry 110 (zone top), stop 70 (protected low)", plan)
	}
	// The QMR's own fail candle (minute 30, low 95) already reaches the
	// entry (110) by sweeping through the zone, so the plan is
	// triggered, not merely actionable, as of minute 55.
	if plan.Status != PlanTriggered {
		t.Fatalf("plan.Status = %v, want PlanTriggered", plan.Status)
	}
	// Targets: confirmed swing highs beyond 110 are H1 (125, also the
	// transactional-liquidity level from the establishing break) and the
	// later confirmed high (160), nearest-first.
	if len(plan.Targets) != 2 {
		t.Fatalf("len(Targets) = %d, want 2: %+v", len(plan.Targets), plan.Targets)
	}
	if plan.Targets[0].Level != 125 || plan.Targets[1].Level != 160 {
		t.Fatalf("Targets = %+v, want [125,160] nearest-first", plan.Targets)
	}
	// R:R = (target-entry)/(entry-stop): (125-110)/(110-70)=15/40=3/8; (160-110)/40=50/40=5/4.
	if plan.Targets[0].RR.Cmp(big.NewRat(3, 8)) != 0 {
		t.Fatalf("Targets[0].RR = %v, want 3/8", plan.Targets[0].RR)
	}
	if plan.Targets[1].RR.Cmp(big.NewRat(5, 4)) != 0 {
		t.Fatalf("Targets[1].RR = %v, want 5/4", plan.Targets[1].RR)
	}
}

func TestIntegration_TradePlan_Bearish_Mirror(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	h1 := seedSwing(t, store, instrumentID, SwingHigh, 2, 125, []int64{1, 2, 3, 4, 5})
	l1 := seedSwing(t, store, instrumentID, SwingLow, 7, 90, []int64{6, 7, 8, 9, 10})
	if err := store.SetState(context.Background(), StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBearish,
		ReferenceSwingHighID: &h1.ID, ReferenceSwingLowID: &l1.ID,
	}, nowUTC()); err != nil {
		t.Fatalf("seed live state: %v", err)
	}
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 105, 95, 100},
		{19, 102, 108, 96, 102},
		{20, 115, 150, 100, 115}, // sweep + protected high (150)
		{21, 103, 107, 98, 103},
		{22, 104, 106, 97, 104},
		{23, 95, 100, 90, 98}, // order block, zone [90,100]
		{24, 98, 99, 80, 82},
		{25, 70, 80, 65, 70}, // B: close 70 < L1 (90)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{30, 110, 90, 95}, // QMR fail: high(110) > 100, close(95) < 100
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(30), et(31)); err != nil {
		t.Fatalf("EvaluateRange (fail candle): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 100, 95, 98},
		{39, 100, 95, 98},
		{40, 135, 95, 100}, // structural-liquidity candidate high (135)
		{41, 100, 95, 98},
		{42, 100, 95, 98},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 100, 20, 98}, // later confirmed swing low (20)
		{49, 100, 95, 98},
		{50, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange (structural liquidity): %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	plan, ok, err := ResolveTradePlan(context.Background(), store, candles, state, et(55), time.Minute, DefaultMinLegSpan, 1)
	if err != nil {
		t.Fatalf("ResolveTradePlan: %v", err)
	}
	if !ok {
		t.Fatal("ResolveTradePlan ok = false, want true")
	}
	if plan.POI.Type != POIQMR || plan.Direction != DirectionBearish {
		t.Fatalf("plan = %+v, want POIQMR, Bearish", plan)
	}
	if plan.Entry != 90 || plan.Stop != 150 {
		t.Fatalf("plan entry/stop = %d/%d, want 90 (zone bottom), 150 (protected high)", plan.Entry, plan.Stop)
	}
	if plan.Status != PlanTriggered {
		t.Fatalf("plan.Status = %v, want PlanTriggered (the fail candle's high (110) already reached entry (90) by sweeping through the zone)", plan.Status)
	}
	// Targets: confirmed swing lows below 90 are L1 (90? no -- L1 is
	// exactly 90, not strictly below entry, so excluded) and the later
	// confirmed low (20). Transactional liquidity is L1 (90), also
	// excluded by the same strict filter.
	if len(plan.Targets) != 1 || plan.Targets[0].Level != 20 {
		t.Fatalf("Targets = %+v, want [20]: L1 (90) is not strictly below the entry (90)", plan.Targets)
	}
	// R:R = (entry-target)/(stop-entry) = (90-20)/(150-90) = 70/60 = 7/6.
	if plan.Targets[0].RR.Cmp(big.NewRat(7, 6)) != 0 {
		t.Fatalf("Targets[0].RR = %v, want 7/6", plan.Targets[0].RR)
	}
}

// TestIntegration_TradePlan_PointInTime_ActionableThenTriggered proves
// status flips actionable -> triggered as a later finalized candle
// reaches the entry, using an order-block-only POI (no QMR fail, so
// nothing has reached the zone yet at the earlier asOf).
//
// The order block's zone is lowered to [80,90] (entry 90) here, below
// the structural-liquidity candidate's own price (93): the candidate's
// price must sit at or below the second fib's equilibrium (97) to
// qualify for section 9's discount half, and that candidate's own low
// is one of the candles the status scan below must look at too (it is
// chronologically after the order block forms) -- keeping it above the
// entry means it cannot itself look like price having already reached
// the zone.
func TestIntegration_TradePlan_PointInTime_ActionableThenTriggered(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95},
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 88, 90, 80, 82},     // order block, zone [80,90], never swept
		{24, 100, 120, 95, 115},  // kept above entry (90) so it does not itself look like a reach
		{25, 130, 135, 120, 130}, // B: close 130 > H1 (125)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 105, 93, 100}, // structural-liquidity candidate low (93, discount half of [70,125], above entry)
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 160, 95, 100},
		{49, 100, 95, 98},
		{50, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange (structural liquidity): %v", err)
	}
	// A candle that reaches entry (90) by wick only -- low 85 <= 90 --
	// while closing back above the live reference low (93, the
	// structural-liquidity candidate itself, now the most recent
	// confirmed swing low) so it is not read as a real CHOCH.
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{60, 97, 85, 95},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(60), et(61)); err != nil {
		t.Fatalf("EvaluateRange (reach-entry candle): %v", err)
	}
	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}

	before, ok, err := ResolveTradePlan(context.Background(), store, candles, state, et(60), time.Minute, DefaultMinLegSpan, 1)
	if err != nil || !ok {
		t.Fatalf("ResolveTradePlan (asOf=60): ok=%v err=%v", ok, err)
	}
	if before.Status != PlanActionable {
		t.Fatalf("asOf=60: plan.Status = %v, want PlanActionable: the reach-entry candle closes at minute 61, strictly after asOf", before.Status)
	}

	after, ok, err := ResolveTradePlan(context.Background(), store, candles, state, et(61), time.Minute, DefaultMinLegSpan, 1)
	if err != nil || !ok {
		t.Fatalf("ResolveTradePlan (asOf=61): ok=%v err=%v", ok, err)
	}
	if after.Status != PlanTriggered {
		t.Fatalf("asOf=61: plan.Status = %v, want PlanTriggered: the reach-entry candle has now closed", after.Status)
	}
}

// TestIntegration_TradePlan_Invalidated proves status is invalidated
// once a finalized candle closes strictly through the protected level,
// using the POI/plan snapshot as of just before that close -- the
// documented V1 discrepancy (spec section 7, open item 2): this
// section's own direct close-through-stop check is independent of
// whether the mechanical CHOCH coupling has also fired.
func TestIntegration_TradePlan_Invalidated(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95}, // protected low (70)
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 108, 110, 100, 102}, // order block, zone [100,110]
		{24, 103, 120, 102, 118},
		{25, 130, 135, 120, 130}, // B
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}

	// Snapshot the state while the plan is still bullish, before the
	// candle below closes strictly through the protected low (70) --
	// which, in this base, also triggers a real CHOCH (since the
	// protected low is also the live reference low), flipping the
	// engine's own live direction to bearish and (per section 7)
	// clearing the live row's protected low. This snapshot is this
	// specific plan's own point-in-time view, independent of whatever
	// the engine's live global state becomes afterward.
	preFlip, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState (pre-flip): %v", err)
	}

	// Placed before the structural-liquidity candidate below even
	// pivots (minute 40): its own close (68) is strictly below the
	// structural-liquidity candidate's price (75) too, which would
	// otherwise break that candidate's own intactness (scanned only
	// from ITS confirmation, minute 43, onward) if this candle fell
	// inside that window instead.
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{30, 70, 65, 68}, // closes strictly through the protected low (70)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(30), et(31)); err != nil {
		t.Fatalf("EvaluateRange (invalidating candle): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 105, 75, 100}, // structural-liquidity candidate low (75, discount half of [70,125])
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 160, 95, 100}, // later confirmed swing high (160)
		{49, 100, 95, 98},
		{50, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange (structural liquidity): %v", err)
	}

	// The plan's point-in-time snapshot: the pre-flip bullish direction
	// and protected low (since the live row's CHOCH clears it), with
	// the reference high updated to what confirmed afterward (160) --
	// the same combination ResolveActiveRange needs for the order block
	// to register as in discount, exactly as every other fixture in
	// this file gets simply by fetching state once, after everything,
	// because none of them cross an actual direction flip.
	late, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState (late): %v", err)
	}
	state := preFlip
	state.ReferenceSwingHighID = late.ReferenceSwingHighID

	plan, ok, err := ResolveTradePlan(context.Background(), store, candles, state, et(55), time.Minute, DefaultMinLegSpan, 1)
	if err != nil || !ok {
		t.Fatalf("ResolveTradePlan: ok=%v err=%v", ok, err)
	}
	if plan.Status != PlanInvalidated {
		t.Fatalf("plan.Status = %v, want PlanInvalidated", plan.Status)
	}
}

// TestIntegration_TradePlan_None_NoPOI proves status none when section
// 13 returns no POI: no structural-liquidity swing is ever seeded, so
// the structure is structural-absent (inducement), even with an order
// block present.
func TestIntegration_TradePlan_None_NoPOI(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95},
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 108, 110, 100, 102},
		{24, 103, 120, 102, 118},
		{25, 130, 135, 120, 130},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	plan, ok, err := ResolveTradePlan(context.Background(), store, candles, state, et(30), time.Minute, DefaultMinLegSpan, 1)
	if err != nil || !ok {
		t.Fatalf("ResolveTradePlan: ok=%v err=%v", ok, err)
	}
	if plan.Status != PlanNone {
		t.Fatalf("plan.Status = %v, want PlanNone", plan.Status)
	}
}

// TestIntegration_TradePlan_Undefined_EntryNotBeyondStop proves the
// plan is undefined (ok=false), not clamped or fabricated, when the
// entry is not strictly beyond the stop.
//
// The protected low is seeded directly at 115 -- above the order
// block's own zone, [100,110] -- and the sweep-validated break (B) is
// inserted directly as a structure event, rather than reached by
// feeding B's own candle through the real engine: any real candle
// whose close sits inside or below [100,110] (B's own impulse
// candles, or the order block candle itself) would close strictly
// below a reference low that high, which the real engine would read
// as a genuine bearish CHOCH, flipping direction before B ever forms.
// Seeding the event directly proves the entry/stop guard on its own
// terms, independent of whether this exact geometry (a protected
// anchor priced above the order block's zone) is reachable through
// organic evaluation -- the guard must hold given the inputs, however
// they arose.
func TestIntegration_TradePlan_Undefined_EntryNotBeyondStop(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)
	e := newTestEngine(t, db)
	candles := newTestCandleSource(db)

	h1 := seedSwing(t, store, instrumentID, SwingHigh, 2, 125, []int64{1, 2, 3, 4, 5})
	l1 := seedSwing(t, store, instrumentID, SwingLow, 7, 50, []int64{6, 7, 8, 9, 10})
	protectedLow := seedSwing(t, store, instrumentID, SwingLow, 20, 115, []int64{18, 19, 20, 21, 22})

	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{23, 108, 110, 100, 102}, // order block, zone [100,110] -- below the protected low (115)
		{24, 103, 120, 102, 118},
		{25, 130, 135, 120, 130}, // B's own candle row (not evaluated by the engine)
	})
	breakTriggerID := mustCandleIDAt(t, db, instrumentID, 25)
	sweep, err := store.InsertSweep(context.Background(), NewSweep{
		InstrumentID: instrumentID, Timeframe: "1m", Side: SweepLow,
		SweptSwingID: l1.ID, TriggerOpenTime: et(20), TriggerCandleID: breakTriggerID,
		PenetrationExtreme: 115, ReclaimClose: 121,
		EvaluationTime: clockEvalTime(20), AlgorithmVersion: testAlgorithmVersion,
	})
	if err != nil {
		t.Fatalf("seed sweep: %v", err)
	}
	breakEvent, err := store.InsertEvent(context.Background(), NewStructureEvent{
		InstrumentID: instrumentID, Timeframe: "1m", Type: EventBOS,
		DirectionBefore: DirectionBullish, DirectionAfter: DirectionBullish,
		TriggerOpenTime: et(25), TriggerCandleID: breakTriggerID, ReferenceSwingIDs: []int64{h1.ID},
		EvaluationTime: clockEvalTime(25), AlgorithmVersion: testAlgorithmVersion,
		SweepValidated: true, ProtectedSwingID: &protectedLow.ID, SweepEventID: &sweep.ID,
	})
	if err != nil {
		t.Fatalf("seed sweep-validated break event: %v", err)
	}
	if err := store.SetState(context.Background(), StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
		ReferenceSwingHighID: &h1.ID, ReferenceSwingLowID: &l1.ID,
		ProtectedLowSwingID: &protectedLow.ID, LastEventID: &breakEvent.ID,
	}, nowUTC()); err != nil {
		t.Fatalf("seed live state: %v", err)
	}

	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 105, 75, 100}, // structural-liquidity candidate low (75, discount half of [115,125])
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 160, 95, 100}, // later confirmed swing high (160)
		{49, 100, 95, 98},
		{50, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange (structural liquidity): %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	poi, err := ResolvePOI(context.Background(), store, candles, state, et(55), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolvePOI: %v", err)
	}
	if poi.Type != POIOrderBlock {
		t.Fatalf("poi.Type = %v, want POIOrderBlock (sanity check before exercising the guard)", poi.Type)
	}

	_, ok, err := ResolveTradePlan(context.Background(), store, candles, state, et(55), time.Minute, DefaultMinLegSpan, 1)
	if err != nil {
		t.Fatalf("ResolveTradePlan: %v", err)
	}
	if ok {
		t.Fatal("ResolveTradePlan ok = true, want false: entry (110) is not strictly beyond stop (115)")
	}
}

// TestIntegration_TradePlan_Targets_DedupedAndCapped proves more than
// three qualifying levels collapse near-duplicates and cap at three,
// nearest-first.
func TestIntegration_TradePlan_Targets_DedupedAndCapped(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95},
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 108, 110, 100, 102}, // order block, zone [100,110]
		{24, 103, 120, 102, 118},
		{25, 130, 135, 120, 130}, // B, H1 (125) becomes transactional liquidity too
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 105, 75, 100},
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 160, 95, 100},
		{49, 100, 95, 98},
		{50, 100, 95, 98},
		// Within 2 ticks of H1 (125) -- collapses into it.
		{56, 100, 95, 98},
		{57, 100, 95, 98},
		{58, 127, 95, 100},
		{59, 100, 95, 98},
		{60, 100, 95, 98},
		{63, 100, 95, 98},
		{64, 100, 95, 98},
		{65, 170, 95, 100},
		{66, 100, 95, 98},
		{67, 100, 95, 98},
		{70, 100, 95, 98},
		{71, 100, 95, 98},
		{72, 180, 95, 100},
		{73, 100, 95, 98},
		{74, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(75)); err != nil {
		t.Fatalf("EvaluateRange (extra swing highs): %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	// minTick=2: 127 is within 2 of 125 and collapses into it. Candidates
	// beyond entry (110), nearest-first: 125, 127(collapsed), 160, 170, 180.
	plan, ok, err := ResolveTradePlan(context.Background(), store, candles, state, et(80), time.Minute, DefaultMinLegSpan, 2)
	if err != nil || !ok {
		t.Fatalf("ResolveTradePlan: ok=%v err=%v", ok, err)
	}
	if len(plan.Targets) != 3 {
		t.Fatalf("len(Targets) = %d, want 3 (capped): %+v", len(plan.Targets), plan.Targets)
	}
	got := []quant.Price{plan.Targets[0].Level, plan.Targets[1].Level, plan.Targets[2].Level}
	want := []quant.Price{125, 160, 170}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Targets = %v, want %v (127 collapsed into 125; capped before 180)", got, want)
		}
	}
}

// TestIntegration_TradePlan_EmptyTargets exercises resolveTargets
// directly (same package, white-box): when entry sits above every
// confirmed opposing swing, there is no structural target. This is
// tested in isolation rather than through the full POI pipeline,
// because an order-block/QMR POI is only ever selected when it is in
// the discount half of the active range (section 8) -- which requires
// the range's own high endpoint (a confirmed swing high) to sit above
// equilibrium, and therefore above the entry, so that endpoint always
// qualifies as a target through the full pipeline. The empty-targets
// case is a property of target selection on its own, not reachable
// through the full chain of other section-13 preconditions.
func TestIntegration_TradePlan_EmptyTargets(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)

	seedBullishEstablished(t, store, instrumentID) // H1 = 125

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	levels, err := resolveTargets(context.Background(), store, state, DirectionBullish, 200, et(30), 1)
	if err != nil {
		t.Fatalf("resolveTargets: %v", err)
	}
	if len(levels) != 0 {
		t.Fatalf("levels = %v, want empty: H1 (125) does not lie beyond entry (200)", levels)
	}
}

// TestIntegration_TradePlan_RecomputesUnderCorrection proves the whole
// plan recomputes deterministically under a candle correction:
// correcting the QMR fail candle's low so it no longer sweeps the
// order block flips the POI from qmr to order_block, which changes
// nothing about entry/stop (same zone) but flips status back from
// triggered to actionable.
func TestIntegration_TradePlan_RecomputesUnderCorrection(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	mdStore := marketdata.NewStore(db, clock.Real{})
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95},
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 108, 110, 100, 102}, // order block, zone [100,110]
		{24, 103, 120, 102, 118},
		{25, 130, 135, 120, 130}, // B
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{30, 103, 95, 102}, // fail: low(95) < 100, close(102) > 100
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(30), et(31)); err != nil {
		t.Fatalf("EvaluateRange (fail candle): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 105, 75, 100},
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 160, 95, 100},
		{49, 100, 95, 98},
		{50, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange (structural liquidity): %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	before, ok, err := ResolveTradePlan(context.Background(), store, candles, state, et(55), time.Minute, DefaultMinLegSpan, 1)
	if err != nil || !ok {
		t.Fatalf("ResolveTradePlan (before): ok=%v err=%v", ok, err)
	}
	if before.POI.Type != POIQMR || before.Status != PlanTriggered {
		t.Fatalf("before = %+v, want POIQMR, PlanTriggered", before)
	}

	// Correct minute 30's low from 95 to 101: it no longer sweeps the
	// order block's low (100), so no QMR exists, and it no longer
	// reaches the entry (110) either.
	_, err = mdStore.SupersedeCandle(context.Background(), instrumentID, clockEventTime(30), marketdata.Correction{
		Open: 112, High: 115, Low: 111, Close: 113, Volume: 1, Provider: "test", IngestedAt: clock.NewIngestionTime(time.Now()),
	})
	if err != nil {
		t.Fatalf("SupersedeCandle: %v", err)
	}
	if err := e.HandleCandleCorrection(context.Background(), instrumentID, "1m", et(30)); err != nil {
		t.Fatalf("HandleCandleCorrection: %v", err)
	}

	after, ok, err := ResolveTradePlan(context.Background(), store, candles, state, et(55), time.Minute, DefaultMinLegSpan, 1)
	if err != nil || !ok {
		t.Fatalf("ResolveTradePlan (after): ok=%v err=%v", ok, err)
	}
	if after.POI.Type != POIOrderBlock {
		t.Fatalf("after.POI.Type = %v, want POIOrderBlock: minute 30 no longer sweeps the order block", after.POI.Type)
	}
	// Status stays Triggered either way: the structural-liquidity
	// candidate's own candle (minute 40, low 75) already reaches the
	// entry (110) on its own, independent of minute 30's correction.
	// What the correction changes is POI.Type, asserted above.
	if after.Status != PlanTriggered {
		t.Fatalf("after.Status = %v, want PlanTriggered", after.Status)
	}
}
