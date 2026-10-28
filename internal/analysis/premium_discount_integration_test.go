//go:build integration

package analysis

import (
	"context"
	"testing"

	"github.com/chibuike-kt/argus/internal/clock"
)

// Batch 3 golden fixtures, strategy-spec.md v0.5 section 8: every
// bullet in the spec's batch-3 golden-fixtures list. Range resolution
// loads swings and state from the real store, so these run against
// real Postgres (ADR-009, ADR-019).

func TestIntegration_PremiumDiscount_ValidBullishRange(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)

	protectedLow := seedSwing(t, store, instrumentID, SwingLow, 2, 100, []int64{1, 2, 3, 4, 5})
	referenceHigh := seedSwing(t, store, instrumentID, SwingHigh, 7, 130, []int64{6, 7, 8, 9, 10})
	if err := store.SetState(context.Background(), StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
		ProtectedLowSwingID: &protectedLow.ID, ReferenceSwingHighID: &referenceHigh.ID,
	}, nowUTC()); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	r, ok, err := ResolveActiveRange(context.Background(), store, state)
	if err != nil {
		t.Fatalf("ResolveActiveRange: %v", err)
	}
	if !ok {
		t.Fatal("ResolveActiveRange ok = false, want true: a valid bullish structure")
	}
	if r.Low != 100 || r.High != 130 {
		t.Fatalf("range = %+v, want Low=100 High=130 ([protected low, latest confirmed swing high])", r)
	}
	if got := r.Equilibrium(); got != 115 {
		t.Fatalf("Equilibrium() = %d, want 115", got)
	}

	if got := r.Classify(110); got != ZoneDiscount {
		t.Fatalf("Classify(110) = %v, want Discount (the buy zone, in a bullish structure)", got)
	}
	if got := r.Classify(115); got != ZoneEquilibrium {
		t.Fatalf("Classify(115) = %v, want Equilibrium", got)
	}
	if got := r.Classify(120); got != ZonePremium {
		t.Fatalf("Classify(120) = %v, want Premium", got)
	}

	inDiscount, ok, err := InDiscount(context.Background(), store, state, 110)
	if err != nil || !ok {
		t.Fatalf("InDiscount(110): ok=%v err=%v, want ok=true", ok, err)
	}
	if !inDiscount {
		t.Fatal("InDiscount(110) = false, want true")
	}
}

func TestIntegration_PremiumDiscount_ValidBearishRange(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)

	referenceLow := seedSwing(t, store, instrumentID, SwingLow, 2, 170, []int64{1, 2, 3, 4, 5})
	protectedHigh := seedSwing(t, store, instrumentID, SwingHigh, 7, 230, []int64{6, 7, 8, 9, 10})
	if err := store.SetState(context.Background(), StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBearish,
		ReferenceSwingLowID: &referenceLow.ID, ProtectedHighSwingID: &protectedHigh.ID,
	}, nowUTC()); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	r, ok, err := ResolveActiveRange(context.Background(), store, state)
	if err != nil {
		t.Fatalf("ResolveActiveRange: %v", err)
	}
	if !ok {
		t.Fatal("ResolveActiveRange ok = false, want true: a valid bearish structure")
	}
	if r.Low != 170 || r.High != 230 {
		t.Fatalf("range = %+v, want Low=170 High=230 ([latest confirmed swing low, protected high])", r)
	}
	if got := r.Equilibrium(); got != 200 {
		t.Fatalf("Equilibrium() = %d, want 200", got)
	}

	if got := r.Classify(190); got != ZoneDiscount {
		t.Fatalf("Classify(190) = %v, want Discount", got)
	}
	if got := r.Classify(200); got != ZoneEquilibrium {
		t.Fatalf("Classify(200) = %v, want Equilibrium", got)
	}
	if got := r.Classify(210); got != ZonePremium {
		t.Fatalf("Classify(210) = %v, want Premium (the sell zone, in a bearish structure)", got)
	}
}

func TestIntegration_PremiumDiscount_UndefinedWhenDirectionUndetermined(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)

	// Cold start: no state row exists yet. GetState returns the
	// Undetermined default.
	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.Direction != DirectionUndetermined {
		t.Fatalf("precondition: Direction = %v, want Undetermined", state.Direction)
	}

	_, ok, err := ResolveActiveRange(context.Background(), store, state)
	if err != nil {
		t.Fatalf("ResolveActiveRange: %v", err)
	}
	if ok {
		t.Fatal("ResolveActiveRange ok = true, want false: direction is Undetermined")
	}
}

// TestIntegration_PremiumDiscount_UndefinedWhenProtectedAnchorAbsent is
// the explicit regression guard the spec calls out: even though a
// confirmed opposing swing low exists and could, if the engine were
// buggy, be mistaken for the missing protected anchor, the range must
// come back undefined -- never computed from that fallback.
func TestIntegration_PremiumDiscount_UndefinedWhenProtectedAnchorAbsent(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)

	// A real, confirmed swing low exists -- but it is NOT referenced as
	// the protected low in state. If ResolveActiveRange ever fell back
	// to "the latest confirmed low" when the anchor is nil, it would
	// wrongly pick this up.
	decoyLow := seedSwing(t, store, instrumentID, SwingLow, 2, 100, []int64{1, 2, 3, 4, 5})
	referenceHigh := seedSwing(t, store, instrumentID, SwingHigh, 7, 130, []int64{6, 7, 8, 9, 10})
	if err := store.SetState(context.Background(), StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
		ReferenceSwingHighID: &referenceHigh.ID, // ProtectedLowSwingID deliberately left nil
	}, nowUTC()); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.ProtectedLowSwingID != nil {
		t.Fatalf("precondition: ProtectedLowSwingID = %v, want nil", state.ProtectedLowSwingID)
	}

	r, ok, err := ResolveActiveRange(context.Background(), store, state)
	if err != nil {
		t.Fatalf("ResolveActiveRange: %v", err)
	}
	if ok {
		t.Fatalf("ResolveActiveRange ok = true (range %+v), want false: the protected low is absent and must never fall back to the decoy swing low (id %d, price 100)", r, decoyLow.ID)
	}
}

func TestIntegration_PremiumDiscount_UndefinedWhenRangeNotPositive(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)

	// Inverted: the "low" endpoint is actually priced above the "high"
	// endpoint. High > Low fails.
	protectedLow := seedSwing(t, store, instrumentID, SwingLow, 2, 130, []int64{1, 2, 3, 4, 5})
	referenceHigh := seedSwing(t, store, instrumentID, SwingHigh, 7, 100, []int64{6, 7, 8, 9, 10})
	if err := store.SetState(context.Background(), StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
		ProtectedLowSwingID: &protectedLow.ID, ReferenceSwingHighID: &referenceHigh.ID,
	}, nowUTC()); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if _, ok, err := ResolveActiveRange(context.Background(), store, state); err != nil || ok {
		t.Fatalf("ResolveActiveRange: ok=%v err=%v, want ok=false (high 100 is not strictly greater than low 130)", ok, err)
	}
}

func TestIntegration_PremiumDiscount_UndefinedWhenRangeExactlyFlat(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)

	// Equal endpoints: High > Low fails at the boundary (not just when inverted).
	protectedLow := seedSwing(t, store, instrumentID, SwingLow, 2, 100, []int64{1, 2, 3, 4, 5})
	referenceHigh := seedSwing(t, store, instrumentID, SwingHigh, 7, 100, []int64{6, 7, 8, 9, 10})
	if err := store.SetState(context.Background(), StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
		ProtectedLowSwingID: &protectedLow.ID, ReferenceSwingHighID: &referenceHigh.ID,
	}, nowUTC()); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if _, ok, err := ResolveActiveRange(context.Background(), store, state); err != nil || ok {
		t.Fatalf("ResolveActiveRange: ok=%v err=%v, want ok=false (high == low is not a positive range)", ok, err)
	}
}

// TestIntegration_PremiumDiscount_RangeTopTracksNewSwingHigh proves the
// range top moves with newly confirmed highs while the protected low
// stays fixed (spec section 8): run the engine forward so a new, higher
// swing high organically confirms, and show equilibrium shifts
// accordingly without the protected low changing at all.
func TestIntegration_PremiumDiscount_RangeTopTracksNewSwingHigh(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	// seedBullishEstablished sets only the batch-1 reference swings, not
	// a protected anchor, so the protected low is seeded directly here.
	protectedLowSwing := seedSwing(t, store, instrumentID, SwingLow, 7, 90, []int64{6, 7, 8, 9, 10})
	referenceHigh := seedSwing(t, store, instrumentID, SwingHigh, 2, 125, []int64{1, 2, 3, 4, 5})
	if err := store.SetState(context.Background(), StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
		ProtectedLowSwingID: &protectedLowSwing.ID, ReferenceSwingHighID: &referenceHigh.ID,
	}, nowUTC()); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	protectedLow := protectedLowSwing.ID

	stateBefore, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState (before): %v", err)
	}
	rangeBefore, ok, err := ResolveActiveRange(context.Background(), store, stateBefore)
	if err != nil || !ok {
		t.Fatalf("ResolveActiveRange (before): ok=%v err=%v", ok, err)
	}
	if rangeBefore.High != 125 {
		t.Fatalf("range before = %+v, want High=125 (H1)", rangeBefore)
	}
	eqBefore := rangeBefore.Equilibrium()

	// A brand-new, higher swing high confirms organically.
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{20, 132, 125, 128},
		{21, 134, 126, 129},
		{22, 140, 127, 131}, // the new, higher swing high (140)
		{23, 133, 128, 130},
		{24, 131, 129, 130},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(20), et(25)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	newHigh, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingHigh, clock.NewEventTime(et(22)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (new high): %v", err)
	}
	if newHigh.Price != 140 {
		t.Fatalf("new high price = %v, want 140", newHigh.Price)
	}

	stateAfter, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState (after): %v", err)
	}
	if stateAfter.ProtectedLowSwingID == nil || *stateAfter.ProtectedLowSwingID != protectedLow {
		t.Fatalf("stateAfter.ProtectedLowSwingID = %v, want %d (unchanged -- the protected low stays fixed)", stateAfter.ProtectedLowSwingID, protectedLow)
	}
	if stateAfter.ReferenceSwingHighID == nil || *stateAfter.ReferenceSwingHighID != newHigh.ID {
		t.Fatalf("stateAfter.ReferenceSwingHighID = %v, want %d (the newly confirmed high)", stateAfter.ReferenceSwingHighID, newHigh.ID)
	}

	rangeAfter, ok, err := ResolveActiveRange(context.Background(), store, stateAfter)
	if err != nil || !ok {
		t.Fatalf("ResolveActiveRange (after): ok=%v err=%v", ok, err)
	}
	if rangeAfter.Low != rangeBefore.Low {
		t.Fatalf("range after Low = %v, want unchanged %v", rangeAfter.Low, rangeBefore.Low)
	}
	if rangeAfter.High != 140 {
		t.Fatalf("range after High = %v, want 140 (the new swing high)", rangeAfter.High)
	}
	eqAfter := rangeAfter.Equilibrium()
	if eqAfter == eqBefore {
		t.Fatalf("equilibrium did not shift: before=%d after=%d, want it to move with the new range top", eqBefore, eqAfter)
	}
}
