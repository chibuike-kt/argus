//go:build integration

package analysis

import (
	"context"
	"testing"
	"time"
)

// Batch 4 golden fixtures, strategy-spec.md v0.7.1 section 11:
// inducement. ResolveInducementLiquidity adds no new detection -- it
// relabels ResolveStructuralLiquidity's own result -- so these fixtures
// reuse structural_liquidity_integration_test.go's exact bullish/bearish
// structural-absent and structural-present shapes.

func TestIntegration_Inducement_StructuralAbsentYieldsProtectedLowBullish(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep + protected low (70)
		{21, 128, 93, 102},
		{22, 126, 94, 103},
		{25, 135, 120, 130}, // sweep-validated BOS above H1 (125)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	protectedLow, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clockEventTime(20))
	if err != nil {
		t.Fatalf("GetCurrentSwing (protected low): %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}

	// Precondition: no swing high has confirmed after either current
	// swing low yet, so section 9 classifies this structural-absent
	// (TestIntegration_StructuralLiquidity_StructuralAbsent's exact shape).
	structural, ok, err := ResolveStructuralLiquidity(context.Background(), store, candles, state, et(30), time.Minute, DefaultMinLegSpan)
	if err != nil || !ok {
		t.Fatalf("precondition ResolveStructuralLiquidity: ok=%v err=%v", ok, err)
	}
	if structural.Status != StructuralAbsent {
		t.Fatalf("precondition: structural.Status = %v, want StructuralAbsent", structural.Status)
	}

	inducement, ok, err := ResolveInducementLiquidity(context.Background(), store, candles, state, et(30), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolveInducementLiquidity: %v", err)
	}
	if !ok {
		t.Fatal("ResolveInducementLiquidity ok = false, want true: a valid second fib exists")
	}
	if inducement.Status != InducementPresent || inducement.Swing.ID != protectedLow.ID {
		t.Fatalf("inducement = %+v, want InducementPresent with swing id %d (the protected low, 70)", inducement, protectedLow.ID)
	}
}

func TestIntegration_Inducement_StructuralAbsentYieldsProtectedHighBearishMirror(t *testing.T) {
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
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 105, 95, 100},
		{19, 108, 96, 102},
		{20, 150, 100, 115}, // sweep + protected high (150)
		{21, 107, 98, 103},
		{22, 106, 97, 104},
		{25, 80, 65, 70}, // sweep-validated BOS below L1 (90)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	protectedHigh, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingHigh, clockEventTime(20))
	if err != nil {
		t.Fatalf("GetCurrentSwing (protected high): %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}

	structural, ok, err := ResolveStructuralLiquidity(context.Background(), store, candles, state, et(30), time.Minute, DefaultMinLegSpan)
	if err != nil || !ok {
		t.Fatalf("precondition ResolveStructuralLiquidity: ok=%v err=%v", ok, err)
	}
	if structural.Status != StructuralAbsent {
		t.Fatalf("precondition: structural.Status = %v, want StructuralAbsent", structural.Status)
	}

	inducement, ok, err := ResolveInducementLiquidity(context.Background(), store, candles, state, et(30), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolveInducementLiquidity: %v", err)
	}
	if !ok {
		t.Fatal("ResolveInducementLiquidity ok = false, want true: a valid second fib exists")
	}
	if inducement.Status != InducementPresent || inducement.Swing.ID != protectedHigh.ID {
		t.Fatalf("inducement = %+v, want InducementPresent with swing id %d (the protected high, 150)", inducement, protectedHigh.ID)
	}
}

// TestIntegration_Inducement_StructuralPresentIsNotInducement proves
// mutual exclusivity: the exact structural-present fixture from
// structural_liquidity_integration_test.go yields InducementAbsent.
func TestIntegration_Inducement_StructuralPresentIsNotInducement(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep + protected low (70)
		{21, 128, 93, 102},
		{22, 126, 94, 103},
		{25, 135, 120, 130}, // sweep-validated BOS above H1 (125)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}

	// Second fib [70, 125], eq=97. A new swing low (80, discount half)
	// with a following swing high (110) giving it a shape-qualifying
	// up-leg -- the exact structural-present shape.
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 105, 80, 100}, // candidate swing low (80)
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 110, 95, 100}, // following swing high (110)
		{49, 100, 95, 98},
		{50, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}

	structural, ok, err := ResolveStructuralLiquidity(context.Background(), store, candles, state, et(60), time.Minute, DefaultMinLegSpan)
	if err != nil || !ok {
		t.Fatalf("precondition ResolveStructuralLiquidity: ok=%v err=%v", ok, err)
	}
	if structural.Status != StructuralPresent {
		t.Fatalf("precondition: structural.Status = %v, want StructuralPresent", structural.Status)
	}

	inducement, ok, err := ResolveInducementLiquidity(context.Background(), store, candles, state, et(60), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolveInducementLiquidity: %v", err)
	}
	if !ok {
		t.Fatal("ResolveInducementLiquidity ok = false, want true")
	}
	if inducement.Status != InducementAbsent {
		t.Fatalf("inducement = %+v, want InducementAbsent: the structure has structural liquidity", inducement)
	}
}

func TestIntegration_Inducement_UndefinedWhenNoActiveStructure(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.Direction != DirectionUndetermined {
		t.Fatalf("precondition: Direction = %v, want Undetermined", state.Direction)
	}

	_, ok, err := ResolveInducementLiquidity(context.Background(), store, candles, state, et(10), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolveInducementLiquidity: %v", err)
	}
	if ok {
		t.Fatal("ResolveInducementLiquidity ok = true, want false: no active structure, no second fib")
	}
}
