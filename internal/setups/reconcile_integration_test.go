//go:build integration

package setups

import (
	"context"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
	"github.com/chibuike-kt/argus/internal/quant"
)

// Batch 7 golden fixtures, strategy-spec.md v0.11.0 section 15: setup
// lifecycle, identity, and persistence.

// TestIntegration_Reconcile_DetectedActionableTriggered proves the real
// end-to-end wiring (analysis.Engine -> ResolveTradePlan ->
// setups.Reconcile, the same chain cmd/argus-worker/processing.go
// drives) through a fresh setup's first three states, reusing the
// bullish order-block base shape from
// analysis.TestIntegration_TradePlan_PointInTime_ActionableThenTriggered
// (order block zone [80,90], never swept -- POIOrderBlock, not QMR;
// protected low 70 as the stop; entry 90).
func TestIntegration_Reconcile_DetectedActionableTriggered(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	analysisStore := analysis.NewStore(db)
	setupsStore := NewStore(db)
	candles := newTestCandleSource(db)
	ctx := context.Background()

	seedBullishEstablished(t, analysisStore, instrumentID)
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95}, // sweep + protected low (70)
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 88, 90, 80, 82}, // order block, zone [80,90], never swept
		{24, 100, 120, 95, 115},
		{25, 130, 135, 120, 130}, // BOS: close 130 > reference high (125)
	})
	if err := e.EvaluateRange(ctx, instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 105, 93, 100}, // structural-liquidity candidate low (93)
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 160, 95, 100}, // later confirmed swing high (160)
		{49, 100, 95, 98},
		{50, 100, 95, 98},
	})
	if err := e.EvaluateRange(ctx, instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange (structural liquidity): %v", err)
	}

	state, err := analysisStore.GetState(ctx, instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}

	// Step 1 (asOf=51, right as the POI exists): TradePlan.Status is
	// already Actionable on this very first step. Creation state (spec
	// section 15.2, batch 8): Reconcile records detected then, in the
	// same call, the mapped state -- here Actionable -- so the setup
	// is never persisted disagreeing with the plan it was created from.
	// The returned event is the last one appended (Actionable); the
	// full two-event history is checked at the end of this test.
	plan1, ok, err := analysis.ResolveTradePlan(ctx, analysisStore, candles, state, et(51), time.Minute, analysis.DefaultMinLegSpan, quant.Price(1))
	if err != nil || !ok {
		t.Fatalf("ResolveTradePlan (asOf=51): ok=%v err=%v", ok, err)
	}
	if plan1.Status != analysis.PlanActionable || plan1.POI.Type != analysis.POIOrderBlock {
		t.Fatalf("plan1 = %+v, want Status=PlanActionable POI.Type=POIOrderBlock", plan1)
	}

	setup1, event1, err := Reconcile(ctx, setupsStore, instrumentID, "1m", plan1, testVersion, et(51), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (step 1): %v", err)
	}
	if event1 == nil || event1.Type != EventActionable {
		t.Fatalf("Reconcile (step 1): event = %+v, want EventActionable", event1)
	}
	if setup1.State != StateActionable {
		t.Fatalf("setup1.State = %v, want StateActionable", setup1.State)
	}

	// Step 2 (asOf=52, same underlying Status=Actionable, nothing in
	// the market changed): the setup is already Actionable as of step
	// 1's own creation, so this step is a pure no-op -- no event, same
	// state, same row.
	plan2, ok, err := analysis.ResolveTradePlan(ctx, analysisStore, candles, state, et(52), time.Minute, analysis.DefaultMinLegSpan, quant.Price(1))
	if err != nil || !ok {
		t.Fatalf("ResolveTradePlan (asOf=52): ok=%v err=%v", ok, err)
	}
	setup2, event2, err := Reconcile(ctx, setupsStore, instrumentID, "1m", plan2, testVersion, et(52), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (step 2): %v", err)
	}
	if setup2.ID != setup1.ID {
		t.Fatalf("setup2.ID = %d, want %d (same identity, same row)", setup2.ID, setup1.ID)
	}
	if event2 != nil {
		t.Fatalf("Reconcile (step 2): event = %+v, want nil (no-op)", event2)
	}
	if setup2.State != StateActionable {
		t.Fatalf("setup2.State = %v, want StateActionable", setup2.State)
	}

	// A candle that reaches entry (90) by wick only while closing at
	// minute 61 -- its own close has not happened yet as of asOf=60.
	feedCloseCandles(t, db, instrumentID, []closeBar{{60, 97, 85, 95}})
	if err := e.EvaluateRange(ctx, instrumentID, "1m", et(60), et(61)); err != nil {
		t.Fatalf("EvaluateRange (reach-entry candle): %v", err)
	}

	// Step 3 (asOf=60): status is still Actionable (the reach-entry
	// candle has not closed from this vantage point) -- Reconcile must
	// be a pure no-op: no event, same state, same row.
	plan3, ok, err := analysis.ResolveTradePlan(ctx, analysisStore, candles, state, et(60), time.Minute, analysis.DefaultMinLegSpan, quant.Price(1))
	if err != nil || !ok {
		t.Fatalf("ResolveTradePlan (asOf=60): ok=%v err=%v", ok, err)
	}
	if plan3.Status != analysis.PlanActionable {
		t.Fatalf("plan3.Status = %v, want PlanActionable", plan3.Status)
	}
	setup3, event3, err := Reconcile(ctx, setupsStore, instrumentID, "1m", plan3, testVersion, et(60), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (step 3): %v", err)
	}
	if event3 != nil {
		t.Fatalf("Reconcile (step 3): event = %+v, want nil (no-op)", event3)
	}
	if setup3.State != StateActionable {
		t.Fatalf("setup3.State = %v, want StateActionable (unchanged)", setup3.State)
	}

	// Step 4 (asOf=61): the reach-entry candle has now closed ->
	// Triggered.
	plan4, ok, err := analysis.ResolveTradePlan(ctx, analysisStore, candles, state, et(61), time.Minute, analysis.DefaultMinLegSpan, quant.Price(1))
	if err != nil || !ok {
		t.Fatalf("ResolveTradePlan (asOf=61): ok=%v err=%v", ok, err)
	}
	if plan4.Status != analysis.PlanTriggered {
		t.Fatalf("plan4.Status = %v, want PlanTriggered", plan4.Status)
	}
	setup4, event4, err := Reconcile(ctx, setupsStore, instrumentID, "1m", plan4, testVersion, et(61), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (step 4): %v", err)
	}
	if event4 == nil || event4.Type != EventTriggered {
		t.Fatalf("Reconcile (step 4): event = %+v, want EventTriggered", event4)
	}
	if setup4.State != StateTriggered {
		t.Fatalf("setup4.State = %v, want StateTriggered", setup4.State)
	}

	// The full history is reconstructable (ADR-011) and in order:
	// detected, actionable, triggered -- step 3's no-op left no row.
	events, err := setupsStore.ListEvents(ctx, setup4.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	wantTypes := []EventType{EventDetected, EventActionable, EventTriggered}
	if len(events) != len(wantTypes) {
		t.Fatalf("ListEvents: got %d events, want %d", len(events), len(wantTypes))
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("events[%d].Type = %v, want %v", i, events[i].Type, want)
		}
	}
}

// TestIntegration_Reconcile_CreationState proves spec section 15.2's
// batch 8 "creation state" rule directly, for each of the three
// non-Detected statuses a brand-new identity's first evaluation can
// report: the setup ends in the mapped state, never left at Detected
// disagreeing with its own plan_snapshot, and its history is exactly
// two events (detected, then the mapped one) -- never an invented
// intermediate state the plan was never actually observed in.
func TestIntegration_Reconcile_CreationState(t *testing.T) {
	cases := []struct {
		name      string
		status    analysis.PlanStatus
		wantState State
		wantEvent EventType
	}{
		{"first seen actionable", analysis.PlanActionable, StateActionable, EventActionable},
		{"first seen triggered", analysis.PlanTriggered, StateTriggered, EventTriggered},
		{"first seen invalidated", analysis.PlanInvalidated, StateInvalidated, EventInvalidated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			instrumentID := testInstrument(t, db)
			store := NewStore(db)
			ctx := context.Background()
			candleID := oneCandle(t, db, instrumentID, 1, 85)

			setup, event, err := Reconcile(ctx, store, instrumentID, "1m", bullishPlan(tc.status, candleID, 1), testVersion, et(1), time.Now())
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if setup.State != tc.wantState {
				t.Fatalf("setup.State = %v, want %v", setup.State, tc.wantState)
			}
			if event == nil || event.Type != tc.wantEvent {
				t.Fatalf("event = %+v, want %v", event, tc.wantEvent)
			}

			events, err := store.ListEvents(ctx, setup.ID)
			if err != nil {
				t.Fatalf("ListEvents: %v", err)
			}
			wantTypes := []EventType{EventDetected, tc.wantEvent}
			if len(events) != len(wantTypes) {
				t.Fatalf("ListEvents = %+v, want types %v", events, wantTypes)
			}
			for i, want := range wantTypes {
				if events[i].Type != want {
					t.Fatalf("events[%d].Type = %v, want %v", i, events[i].Type, want)
				}
			}
		})
	}
}

// TestIntegration_Reconcile_CreationThenTriggeredNextStep proves a
// setup first seen as Actionable (detected then actionable, two
// events) that reaches PlanTriggered on a LATER evaluation step ends
// Triggered with exactly three events -- the ordinary transition path
// (reconcileExisting), unaffected by creation state, composing
// correctly with it.
func TestIntegration_Reconcile_CreationThenTriggeredNextStep(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)
	ctx := context.Background()
	candleID := oneCandle(t, db, instrumentID, 1, 85)

	created, event, err := Reconcile(ctx, store, instrumentID, "1m", bullishPlan(analysis.PlanActionable, candleID, 1), testVersion, et(1), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (detected+actionable): %v", err)
	}
	if created.State != StateActionable || event == nil || event.Type != EventActionable {
		t.Fatalf("Reconcile (detected+actionable): setup=%+v event=%+v", created, event)
	}

	updated, event2, err := Reconcile(ctx, store, instrumentID, "1m", bullishPlan(analysis.PlanTriggered, candleID, 1), testVersion, et(2), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (triggered): %v", err)
	}
	if updated.State != StateTriggered || event2 == nil || event2.Type != EventTriggered {
		t.Fatalf("Reconcile (triggered): setup=%+v event=%+v", updated, event2)
	}

	events, err := store.ListEvents(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	wantTypes := []EventType{EventDetected, EventActionable, EventTriggered}
	if len(events) != len(wantTypes) {
		t.Fatalf("ListEvents = %+v, want types %v", events, wantTypes)
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("events[%d].Type = %v, want %v", i, events[i].Type, want)
		}
	}
}

// bullishPlan is a hand-built, minimal but well-formed bullish
// analysis.TradePlan -- used by the fixtures below that exercise
// Reconcile's own identity/amendment/terminal-guard logic directly,
// rather than the full organic engine pipeline (already proven above).
// orderBlockCandleID must be a real candles row id (setups' own
// poi_source_candle_id is a real foreign key); nothing else needs to
// be real, since protected_swing_id and structural_swing_id are
// embedded in plan_snapshot's JSONB, not referenced by any constraint.
func bullishPlan(status analysis.PlanStatus, orderBlockCandleID int64, revision int32) analysis.TradePlan {
	return analysis.TradePlan{
		Status:    status,
		Direction: analysis.DirectionBullish,
		Entry:     quant.Price(90),
		Stop:      quant.Price(70),
		POI: analysis.POI{
			Type:                     analysis.POIOrderBlock,
			Direction:                analysis.DirectionBullish,
			Low:                      quant.Price(80),
			High:                     quant.Price(90),
			OrderBlockCandleID:       orderBlockCandleID,
			OrderBlockCandleRevision: revision,
			InDiscount:               true,
		},
	}
}

// TestIntegration_Reconcile_InvalidatedStaysTerminal proves spec
// section 15.3's terminal guard directly: once an identity reaches
// Invalidated, a later TradePlan reporting a DIFFERENT status for the
// very same identity must not move it anywhere else -- not implicitly
// by every caller remembering never to call Reconcile again, but
// because Reconcile itself refuses the transition.
func TestIntegration_Reconcile_InvalidatedStaysTerminal(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)
	ctx := context.Background()

	candleID := oneCandle(t, db, instrumentID, 1, 85)

	// Creation state (spec section 15.2, batch 8): Status is Actionable
	// on the first evaluation, so this already lands on StateActionable
	// (detected then actionable, same call), not StateDetected.
	setup1, event1, err := Reconcile(ctx, store, instrumentID, "1m", bullishPlan(analysis.PlanActionable, candleID, 1), testVersion, et(1), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (detected+actionable): %v", err)
	}
	if event1 == nil || event1.Type != EventActionable || setup1.State != StateActionable {
		t.Fatalf("Reconcile (detected+actionable): setup=%+v event=%+v", setup1, event1)
	}

	setup2, event2, err := Reconcile(ctx, store, instrumentID, "1m", bullishPlan(analysis.PlanInvalidated, candleID, 1), testVersion, et(2), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (invalidate): %v", err)
	}
	if event2 == nil || event2.Type != EventInvalidated || setup2.State != StateInvalidated {
		t.Fatalf("Reconcile (invalidate): setup=%+v event=%+v", setup2, event2)
	}

	// A later step reporting PlanActionable again for the SAME
	// identity (a hypothetical the real resolver chain should not
	// produce per spec section 15.3, but Reconcile must not trust that
	// and must guard it explicitly) must not revive the setup.
	setup3, event3, err := Reconcile(ctx, store, instrumentID, "1m", bullishPlan(analysis.PlanActionable, candleID, 1), testVersion, et(3), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (post-terminal): %v", err)
	}
	if event3 != nil {
		t.Fatalf("Reconcile (post-terminal): event = %+v, want nil", event3)
	}
	if setup3.ID != setup2.ID || setup3.State != StateInvalidated {
		t.Fatalf("setup3 = %+v, want id=%d state=StateInvalidated (unchanged)", setup3, setup2.ID)
	}
}

// TestIntegration_Reconcile_ReEntry proves spec section 15.3's re-entry
// rule: a later POI resolving to a DIFFERENT source candle, same
// instrument/timeframe/direction/strategy_version, is a genuinely new
// identity -- a new row -- never a revival of the first one, even
// after it has reached Invalidated.
func TestIntegration_Reconcile_ReEntry(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)
	ctx := context.Background()

	firstCandleID := oneCandle(t, db, instrumentID, 1, 85)
	secondCandleID := oneCandle(t, db, instrumentID, 2, 95)

	first, _, err := Reconcile(ctx, store, instrumentID, "1m", bullishPlan(analysis.PlanActionable, firstCandleID, 1), testVersion, et(1), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (first, detected): %v", err)
	}
	first, _, err = Reconcile(ctx, store, instrumentID, "1m", bullishPlan(analysis.PlanInvalidated, firstCandleID, 1), testVersion, et(2), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (first, invalidated): %v", err)
	}
	if first.State != StateInvalidated {
		t.Fatalf("first.State = %v, want StateInvalidated", first.State)
	}

	second, event, err := Reconcile(ctx, store, instrumentID, "1m", bullishPlan(analysis.PlanActionable, secondCandleID, 1), testVersion, et(3), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (second, detected): %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("second.ID = %d, want a different row than first (%d): re-entry must not reuse the invalidated identity", second.ID, first.ID)
	}
	if second.POISourceCandleID != secondCandleID {
		t.Fatalf("second.POISourceCandleID = %d, want %d", second.POISourceCandleID, secondCandleID)
	}
	// Creation state (spec section 15.2, batch 8): same as the first
	// identity, Status is Actionable on this first evaluation, so the
	// re-entered identity also lands directly on StateActionable.
	if event == nil || event.Type != EventActionable || second.State != StateActionable {
		t.Fatalf("Reconcile (second, detected+actionable): setup=%+v event=%+v", second, event)
	}

	// Both identities independently persist and are listable -- the
	// first did not get overwritten or removed by the second existing.
	all, err := store.ListByInstrument(ctx, instrumentID, "1m")
	if err != nil {
		t.Fatalf("ListByInstrument: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListByInstrument: got %d setups, want 2", len(all))
	}
}

// TestIntegration_Reconcile_Amendment proves spec sections 15.3-15.4: a
// correction to the same identity's POI source-candle revision appends
// an amendment event and updates the stored revision/snapshot, with no
// lifecycle-state change -- never a new identity, never a silent
// overwrite of the prior evidence row.
func TestIntegration_Reconcile_Amendment(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)
	ctx := context.Background()

	candleID := oneCandle(t, db, instrumentID, 1, 85)

	// Creation state (spec section 15.2, batch 8): Status is Actionable
	// on this first evaluation, so this lands directly on StateActionable
	// (detected then actionable, same call) -- no separate step is
	// needed to reach Actionable before the amendment below, unlike
	// before batch 8.
	created, event, err := Reconcile(ctx, store, instrumentID, "1m", bullishPlan(analysis.PlanActionable, candleID, 1), testVersion, et(1), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (detected+actionable, revision=1): %v", err)
	}
	if event == nil || event.Type != EventActionable || created.State != StateActionable {
		t.Fatalf("Reconcile (detected+actionable): setup=%+v event=%+v", created, event)
	}

	// The same source candle, corrected (revision 1 -> 2), same
	// Status: identity is unchanged (revision is deliberately excluded
	// from it, spec section 15.3), so this must amend the existing row,
	// not create a second one, and must not change State.
	amended, event2, err := Reconcile(ctx, store, instrumentID, "1m", bullishPlan(analysis.PlanActionable, candleID, 2), testVersion, et(2), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (amendment): %v", err)
	}
	if amended.ID != created.ID {
		t.Fatalf("amended.ID = %d, want %d (same identity, same row)", amended.ID, created.ID)
	}
	if event2 == nil || event2.Type != EventAmendment {
		t.Fatalf("Reconcile (amendment): event = %+v, want EventAmendment", event2)
	}
	if amended.State != StateActionable {
		t.Fatalf("amended.State = %v, want StateActionable (unchanged by an amendment)", amended.State)
	}
	if amended.POISourceCandleRevision != 2 {
		t.Fatalf("amended.POISourceCandleRevision = %d, want 2", amended.POISourceCandleRevision)
	}

	events, err := store.ListEvents(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	wantTypes := []EventType{EventDetected, EventActionable, EventAmendment}
	if len(events) != len(wantTypes) {
		t.Fatalf("ListEvents = %+v, want types %v", events, wantTypes)
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("events[%d].Type = %v, want %v", i, events[i].Type, want)
		}
	}
	if events[2].POISourceCandleRevision != 2 {
		t.Fatalf("events[2].POISourceCandleRevision = %d, want 2", events[2].POISourceCandleRevision)
	}
}

// TestIntegration_Reconcile_AmendmentAndTransitionTogether proves the
// case TestIntegration_Reconcile_Amendment's own fixture surfaced while
// being built: a single evaluation step whose POI revision AND mapped
// lifecycle state both changed from what is stored appends BOTH an
// amendment and a transition event, not just one silently winning over
// the other -- ADR-011's "nothing silently rewritten" applied to two
// independent kinds of change landing in the same step.
func TestIntegration_Reconcile_AmendmentAndTransitionTogether(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)
	ctx := context.Background()

	candleID := oneCandle(t, db, instrumentID, 1, 85)

	// Creation state (spec section 15.2, batch 8) already lands this on
	// StateActionable (detected then actionable, same call), so the
	// combined amendment+transition step below must use a DIFFERENT
	// target status (Triggered) to still exercise reconcileExisting's
	// "both in the same step" path -- creation absorbed the
	// Detected->Actionable jump this test used to rely on.
	created, _, err := Reconcile(ctx, store, instrumentID, "1m", bullishPlan(analysis.PlanActionable, candleID, 1), testVersion, et(1), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (detected+actionable, revision=1): %v", err)
	}

	// Revision 1 -> 2 AND the mapped state Actionable -> Triggered, in
	// the same call.
	updated, lastEvent, err := Reconcile(ctx, store, instrumentID, "1m", bullishPlan(analysis.PlanTriggered, candleID, 2), testVersion, et(2), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (amendment + transition): %v", err)
	}
	if updated.State != StateTriggered {
		t.Fatalf("updated.State = %v, want StateTriggered", updated.State)
	}
	if updated.POISourceCandleRevision != 2 {
		t.Fatalf("updated.POISourceCandleRevision = %d, want 2", updated.POISourceCandleRevision)
	}
	// The returned event is whichever of the two was appended last
	// (reconcileExisting's own documented order: amendment, then
	// transition) -- the full, ordered history below is what proves
	// both actually happened, not just the last one.
	if lastEvent == nil || lastEvent.Type != EventTriggered {
		t.Fatalf("Reconcile (amendment + transition): last event = %+v, want EventTriggered", lastEvent)
	}

	events, err := store.ListEvents(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	// Creation's own two events (Detected, Actionable), then the second
	// call's two (Amendment, Triggered) -- four events total, one
	// EventType count this brief's own batch 8 change actually grew
	// (from three before creation state existed).
	wantTypes := []EventType{EventDetected, EventActionable, EventAmendment, EventTriggered}
	if len(events) != len(wantTypes) {
		t.Fatalf("ListEvents = %+v, want types %v", events, wantTypes)
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("events[%d].Type = %v, want %v", i, events[i].Type, want)
		}
	}
}
