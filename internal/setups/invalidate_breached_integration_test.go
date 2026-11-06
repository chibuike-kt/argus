//go:build integration

package setups

import (
	"context"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
	"github.com/chibuike-kt/argus/internal/quant"
)

// TestIntegration_InvalidateBreached_TriggeredSetupInvalidatedOnStopBreach_Bullish
// proves spec section 15.2's stop-breach invalidation (v0.12.1) end to
// end for the exact regression the brief describes: a real, organic
// setup (same fixture shape as
// TestIntegration_Reconcile_DetectedActionableTriggered) reaches
// Triggered, then a later candle closes strictly below its own
// recorded stop (70). That same close clears the live protected low
// anchor via v0.12.0's close-through invalidation, so the live plan
// for this structure is PlanNone at this asOf -- Reconcile alone would
// never see this breach -- yet InvalidateBreached still moves the
// setup to invalidated, from its own persisted snapshot.
func TestIntegration_InvalidateBreached_TriggeredSetupInvalidatedOnStopBreach_Bullish(t *testing.T) {
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
		{40, 105, 93, 100},
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 160, 95, 100},
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
	plan1, ok, err := analysis.ResolveTradePlan(ctx, analysisStore, candles, state, et(51), time.Minute, analysis.DefaultMinLegSpan, quant.Price(1))
	if err != nil || !ok {
		t.Fatalf("ResolveTradePlan (asOf=51): ok=%v err=%v", ok, err)
	}
	setup, _, err := Reconcile(ctx, setupsStore, instrumentID, "1m", plan1, testVersion, et(51), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (create): %v", err)
	}
	if setup.Plan.Stop != 70 {
		t.Fatalf("setup.Plan.Stop = %v, want 70", setup.Plan.Stop)
	}

	// Reach-entry candle (entry=90): wick to 85 closing at 95, minute
	// 60, then the close itself at minute 61 -- same two-step shape as
	// TestIntegration_Reconcile_DetectedActionableTriggered, to reach
	// Triggered through the real chain, not a hand-built plan.
	feedCloseCandles(t, db, instrumentID, []closeBar{{60, 97, 85, 95}})
	if err := e.EvaluateRange(ctx, instrumentID, "1m", et(60), et(61)); err != nil {
		t.Fatalf("EvaluateRange (reach-entry candle): %v", err)
	}
	state, err = analysisStore.GetState(ctx, instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	plan2, ok, err := analysis.ResolveTradePlan(ctx, analysisStore, candles, state, et(61), time.Minute, analysis.DefaultMinLegSpan, quant.Price(1))
	if err != nil || !ok {
		t.Fatalf("ResolveTradePlan (asOf=61): ok=%v err=%v", ok, err)
	}
	if plan2.Status != analysis.PlanTriggered {
		t.Fatalf("plan2.Status = %v, want PlanTriggered", plan2.Status)
	}
	setup, _, err = Reconcile(ctx, setupsStore, instrumentID, "1m", plan2, testVersion, et(61), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (trigger): %v", err)
	}
	if setup.State != StateTriggered {
		t.Fatalf("setup.State = %v, want StateTriggered", setup.State)
	}

	// The breach: minute 70 closes at 65, strictly below the stop (70).
	// This same close clears the live protected low (v0.12.0's own
	// close-through invalidation) -- confirmed below, not assumed.
	feedCloseCandles(t, db, instrumentID, []closeBar{{70, 72, 60, 65}})
	if err := e.EvaluateRange(ctx, instrumentID, "1m", et(70), et(71)); err != nil {
		t.Fatalf("EvaluateRange (breach candle): %v", err)
	}
	state, err = analysisStore.GetState(ctx, instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.ProtectedLowSwingID != nil {
		t.Fatalf("state.ProtectedLowSwingID = %v, want nil: the breach candle should have close-through-cleared it", *state.ProtectedLowSwingID)
	}
	livePlan, planOK, err := analysis.ResolveTradePlan(ctx, analysisStore, candles, state, et(71), time.Minute, analysis.DefaultMinLegSpan, quant.Price(1))
	if err != nil {
		t.Fatalf("ResolveTradePlan (asOf=71): %v", err)
	}
	if !planOK || livePlan.Status != analysis.PlanNone {
		t.Fatalf("live plan = (ok=%v, status=%v), want (true, PlanNone): the regression this brief exists to close", planOK, livePlan.Status)
	}

	invalidatedCount, err := InvalidateBreached(ctx, db, setupsStore, instrumentID, "1m", et(70), quant.Price(65), testVersion, et(71), time.Now())
	if err != nil {
		t.Fatalf("InvalidateBreached: %v", err)
	}
	if invalidatedCount != 1 {
		t.Fatalf("InvalidateBreached: invalidated %d setups, want 1", invalidatedCount)
	}

	got, err := setupsStore.GetByID(ctx, setup.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.State != StateInvalidated {
		t.Fatalf("got.State = %v, want StateInvalidated", got.State)
	}

	events, err := setupsStore.ListEvents(ctx, setup.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	wantTypes := []EventType{EventDetected, EventActionable, EventTriggered, EventInvalidated}
	if len(events) != len(wantTypes) {
		t.Fatalf("ListEvents = %+v, want types %v", events, wantTypes)
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("events[%d].Type = %v, want %v", i, events[i].Type, want)
		}
	}
}

// TestIntegration_InvalidateBreached_TriggeredSetupInvalidatedOnStopBreach_Bearish
// is the bearish mirror of the bullish fixture above: a real,
// organic setup reaches Triggered, a later candle closes strictly
// above its own recorded stop (the protected high), that same close
// clears the live protected high via close-through, the live plan
// goes to PlanNone, and InvalidateBreached still moves the setup to
// invalidated from its own persisted snapshot.
func TestIntegration_InvalidateBreached_TriggeredSetupInvalidatedOnStopBreach_Bearish(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	analysisStore := analysis.NewStore(db)
	setupsStore := NewStore(db)
	candles := newTestCandleSource(db)
	ctx := context.Background()

	h1 := seedSwing(t, analysisStore, instrumentID, analysis.SwingHigh, 2, 110, []int64{1, 2, 3, 4, 5})
	l1 := seedSwing(t, analysisStore, instrumentID, analysis.SwingLow, 7, 75, []int64{6, 7, 8, 9, 10})
	if err := analysisStore.SetState(ctx, analysis.StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: analysis.DirectionBearish,
		ReferenceSwingHighID: &h1.ID, ReferenceSwingLowID: &l1.ID,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 108, 92, 100},
		{19, 101, 107, 91, 101},
		{20, 105, 130, 100, 105}, // sweep + protected high (130)
		{21, 102, 108, 93, 102},
		{22, 103, 106, 94, 103},
		{23, 110, 120, 108, 118}, // order block, zone [108,120], bullish-coloured, never swept
		{24, 100, 105, 90, 95},   // high stays below entry (108): must not "reach entry" yet
		{25, 95, 98, 60, 65},     // BOS: close 65 < reference low (75)
	})
	if err := e.EvaluateRange(ctx, instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 106, 95, 100}, // structural-liquidity candidate high (106, premium half of [65,130]); stays below entry (108)
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 105, 95, 100},
		{47, 105, 95, 100},
		{48, 100, 40, 60}, // later confirmed swing low (40)
		{49, 105, 95, 100},
		{50, 105, 95, 100},
	})
	if err := e.EvaluateRange(ctx, instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange (structural liquidity): %v", err)
	}

	state, err := analysisStore.GetState(ctx, instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	plan1, ok, err := analysis.ResolveTradePlan(ctx, analysisStore, candles, state, et(51), time.Minute, analysis.DefaultMinLegSpan, quant.Price(1))
	if err != nil || !ok {
		t.Fatalf("ResolveTradePlan (asOf=51): ok=%v err=%v", ok, err)
	}
	if plan1.Status != analysis.PlanActionable || plan1.Stop != 130 {
		t.Fatalf("plan1 = %+v, want Status=PlanActionable Stop=130", plan1)
	}
	setup, _, err := Reconcile(ctx, setupsStore, instrumentID, "1m", plan1, testVersion, et(51), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (create): %v", err)
	}

	// Reach-entry candle (entry=108, the zone's own low): wick to 112
	// (above entry, the bearish proximal edge direction) closing at
	// 100, minute 60, then the close itself read at minute 61.
	feedCloseCandles(t, db, instrumentID, []closeBar{{60, 112, 98, 100}})
	if err := e.EvaluateRange(ctx, instrumentID, "1m", et(60), et(61)); err != nil {
		t.Fatalf("EvaluateRange (reach-entry candle): %v", err)
	}
	state, err = analysisStore.GetState(ctx, instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	plan2, ok, err := analysis.ResolveTradePlan(ctx, analysisStore, candles, state, et(61), time.Minute, analysis.DefaultMinLegSpan, quant.Price(1))
	if err != nil || !ok {
		t.Fatalf("ResolveTradePlan (asOf=61): ok=%v err=%v", ok, err)
	}
	if plan2.Status != analysis.PlanTriggered {
		t.Fatalf("plan2.Status = %v, want PlanTriggered", plan2.Status)
	}
	setup, _, err = Reconcile(ctx, setupsStore, instrumentID, "1m", plan2, testVersion, et(61), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (trigger): %v", err)
	}
	if setup.State != StateTriggered {
		t.Fatalf("setup.State = %v, want StateTriggered", setup.State)
	}

	// The breach: minute 70 closes at 135, strictly above the stop
	// (130). This same close clears the live protected high.
	feedCloseCandles(t, db, instrumentID, []closeBar{{70, 140, 125, 135}})
	if err := e.EvaluateRange(ctx, instrumentID, "1m", et(70), et(71)); err != nil {
		t.Fatalf("EvaluateRange (breach candle): %v", err)
	}
	state, err = analysisStore.GetState(ctx, instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.ProtectedHighSwingID != nil {
		t.Fatalf("state.ProtectedHighSwingID = %v, want nil: the breach candle should have close-through-cleared it", *state.ProtectedHighSwingID)
	}
	livePlan, planOK, err := analysis.ResolveTradePlan(ctx, analysisStore, candles, state, et(71), time.Minute, analysis.DefaultMinLegSpan, quant.Price(1))
	if err != nil {
		t.Fatalf("ResolveTradePlan (asOf=71): %v", err)
	}
	if !planOK || livePlan.Status != analysis.PlanNone {
		t.Fatalf("live plan = (ok=%v, status=%v), want (true, PlanNone)", planOK, livePlan.Status)
	}

	invalidatedCount, err := InvalidateBreached(ctx, db, setupsStore, instrumentID, "1m", et(70), quant.Price(135), testVersion, et(71), time.Now())
	if err != nil {
		t.Fatalf("InvalidateBreached: %v", err)
	}
	if invalidatedCount != 1 {
		t.Fatalf("InvalidateBreached: invalidated %d setups, want 1", invalidatedCount)
	}

	got, err := setupsStore.GetByID(ctx, setup.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.State != StateInvalidated {
		t.Fatalf("got.State = %v, want StateInvalidated", got.State)
	}
}

// TestIntegration_InvalidateBreached_WickOnlyDoesNotInvalidate proves
// "strictly closes through," not "wicks through": a candle whose low
// pierces the stop but closes back above it leaves a triggered setup
// untouched.
func TestIntegration_InvalidateBreached_WickOnlyDoesNotInvalidate(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	setupsStore := NewStore(db)
	ctx := context.Background()
	candleID := oneCandle(t, db, instrumentID, 1, 85)

	created, _, err := Reconcile(ctx, setupsStore, instrumentID, "1m", bullishPlan(analysis.PlanTriggered, candleID, 1), testVersion, et(1), time.Now())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if created.State != StateTriggered {
		t.Fatalf("created.State = %v, want StateTriggered", created.State)
	}

	// bullishPlan's own stop is 70 (see reconcile_integration_test.go).
	// The candle wicks to 65 (below it) but closes at 72 (above it).
	n, err := InvalidateBreached(ctx, db, setupsStore, instrumentID, "1m", et(10), quant.Price(72), testVersion, et(11), time.Now())
	if err != nil {
		t.Fatalf("InvalidateBreached: %v", err)
	}
	if n != 0 {
		t.Fatalf("InvalidateBreached: invalidated %d setups, want 0 (wick-only)", n)
	}

	got, err := setupsStore.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.State != StateTriggered {
		t.Fatalf("got.State = %v, want unchanged StateTriggered", got.State)
	}
}

// TestIntegration_InvalidateBreached_ExactEqualityDoesNotInvalidate
// proves "strictly beyond," not "at or beyond": a close exactly at the
// stop leaves the setup untouched.
func TestIntegration_InvalidateBreached_ExactEqualityDoesNotInvalidate(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	setupsStore := NewStore(db)
	ctx := context.Background()
	candleID := oneCandle(t, db, instrumentID, 1, 85)

	created, _, err := Reconcile(ctx, setupsStore, instrumentID, "1m", bullishPlan(analysis.PlanTriggered, candleID, 1), testVersion, et(1), time.Now())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	n, err := InvalidateBreached(ctx, db, setupsStore, instrumentID, "1m", et(10), quant.Price(70), testVersion, et(11), time.Now())
	if err != nil {
		t.Fatalf("InvalidateBreached: %v", err)
	}
	if n != 0 {
		t.Fatalf("InvalidateBreached: invalidated %d setups, want 0 (close == stop)", n)
	}

	got, err := setupsStore.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.State != StateTriggered {
		t.Fatalf("got.State = %v, want unchanged StateTriggered", got.State)
	}
}

// TestIntegration_InvalidateBreached_SupersededSetupStillInvalidated
// proves InvalidateBreached evaluates every non-terminal setup of the
// instrument/timeframe, not just whichever identity Reconcile would
// currently treat as the live POI: a first setup (candle A), left
// non-terminal, and a second, different identity (candle B) that would
// be "the current one" if Reconcile ran again -- a breach invalidates
// the first regardless.
func TestIntegration_InvalidateBreached_SupersededSetupStillInvalidated(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	setupsStore := NewStore(db)
	ctx := context.Background()
	candleA := oneCandle(t, db, instrumentID, 1, 85)
	candleB := oneCandle(t, db, instrumentID, 2, 95)

	first, _, err := Reconcile(ctx, setupsStore, instrumentID, "1m", bullishPlan(analysis.PlanTriggered, candleA, 1), testVersion, et(1), time.Now())
	if err != nil {
		t.Fatalf("Reconcile (first): %v", err)
	}
	// A different identity (different source candle) -- the "current"
	// one, in the sense that a real Reconcile call today would only
	// ever evaluate this one, never go back and look at first again.
	// Its own stop (50) is deliberately safe from the breach below (65
	// is strictly above 50, not strictly below it), so this test shows
	// the superseded identity alone is affected, not that every
	// non-terminal setup always is.
	secondPlan := bullishPlan(analysis.PlanActionable, candleB, 1)
	secondPlan.Stop = quant.Price(50)
	if _, _, err := Reconcile(ctx, setupsStore, instrumentID, "1m", secondPlan, testVersion, et(2), time.Now()); err != nil {
		t.Fatalf("Reconcile (second, superseding): %v", err)
	}

	n, err := InvalidateBreached(ctx, db, setupsStore, instrumentID, "1m", et(10), quant.Price(65), testVersion, et(11), time.Now())
	if err != nil {
		t.Fatalf("InvalidateBreached: %v", err)
	}
	if n != 1 {
		t.Fatalf("InvalidateBreached: invalidated %d setups, want 1 (the superseded first identity)", n)
	}

	got, err := setupsStore.GetByID(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.State != StateInvalidated {
		t.Fatalf("got.State = %v, want StateInvalidated: a superseded setup must still be invalidated by its own stop breach", got.State)
	}
}

// TestIntegration_InvalidateBreached_TerminalStaysTerminal proves the
// terminal guard (ListNonTerminalForBreachCheck's own exclusion, not a
// second check here) makes re-processing idempotent: once invalidated,
// further breaching candles -- including a re-run over the exact same
// candle -- append no further events.
func TestIntegration_InvalidateBreached_TerminalStaysTerminal(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	setupsStore := NewStore(db)
	ctx := context.Background()
	candleID := oneCandle(t, db, instrumentID, 1, 85)

	created, _, err := Reconcile(ctx, setupsStore, instrumentID, "1m", bullishPlan(analysis.PlanTriggered, candleID, 1), testVersion, et(1), time.Now())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	n, err := InvalidateBreached(ctx, db, setupsStore, instrumentID, "1m", et(10), quant.Price(65), testVersion, et(11), time.Now())
	if err != nil {
		t.Fatalf("InvalidateBreached (first breach): %v", err)
	}
	if n != 1 {
		t.Fatalf("InvalidateBreached (first breach): invalidated %d, want 1", n)
	}

	// Re-processing the SAME candle again (a replay, or a retried
	// tick) must add nothing.
	n, err = InvalidateBreached(ctx, db, setupsStore, instrumentID, "1m", et(10), quant.Price(65), testVersion, et(11), time.Now())
	if err != nil {
		t.Fatalf("InvalidateBreached (replay of same candle): %v", err)
	}
	if n != 0 {
		t.Fatalf("InvalidateBreached (replay of same candle): invalidated %d, want 0 (already terminal)", n)
	}

	// A further, later candle that ALSO breaches must add nothing
	// either -- terminal means terminal.
	n, err = InvalidateBreached(ctx, db, setupsStore, instrumentID, "1m", et(20), quant.Price(50), testVersion, et(21), time.Now())
	if err != nil {
		t.Fatalf("InvalidateBreached (later candle): %v", err)
	}
	if n != 0 {
		t.Fatalf("InvalidateBreached (later candle): invalidated %d, want 0 (already terminal)", n)
	}

	events, err := setupsStore.ListEvents(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	wantTypes := []EventType{EventDetected, EventTriggered, EventInvalidated}
	if len(events) != len(wantTypes) {
		t.Fatalf("ListEvents = %+v, want exactly %v (no duplicate invalidation events)", events, wantTypes)
	}
}
