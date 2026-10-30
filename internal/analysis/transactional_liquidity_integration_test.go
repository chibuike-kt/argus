//go:build integration

package analysis

import (
	"context"
	"testing"

	"github.com/chibuike-kt/argus/internal/clock"
)

// Batch 4 golden fixtures, strategy-spec.md v0.7.1 section 10:
// transactional liquidity. Breaks and their reference swings are
// loaded from real Postgres (ADR-009, ADR-019).

func TestIntegration_TransactionalLiquidity_BullishReferenceHigh(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	highID, _ := seedBullishEstablished(t, store, instrumentID)
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

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	swing, ok, err := ResolveTransactionalLiquidity(context.Background(), store, state, et(30))
	if err != nil {
		t.Fatalf("ResolveTransactionalLiquidity: %v", err)
	}
	if !ok {
		t.Fatal("ResolveTransactionalLiquidity ok = false, want true")
	}
	if swing.ID != highID || swing.Price != 125 {
		t.Fatalf("swing = %+v, want id %d price 125 (H1, taken by the sweep-validated bullish break)", swing, highID)
	}
}

func TestIntegration_TransactionalLiquidity_BearishReferenceLowMirror(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

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

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	swing, ok, err := ResolveTransactionalLiquidity(context.Background(), store, state, et(30))
	if err != nil {
		t.Fatalf("ResolveTransactionalLiquidity: %v", err)
	}
	if !ok {
		t.Fatal("ResolveTransactionalLiquidity ok = false, want true")
	}
	if swing.ID != l1.ID || swing.Price != 90 {
		t.Fatalf("swing = %+v, want id %d price 90 (L1, taken by the sweep-validated bearish break)", swing, l1.ID)
	}
}

// TestIntegration_TransactionalLiquidity_TracksMoreRecentValidatedBreak
// is the spec's own "110-then-118" example: the establishing break
// (minute 25, taking out H1/125) and a later validated break (minute
// 35, taking out a brand-new, higher swing high/140) both validate
// against the SAME surviving protected low. Transactional liquidity
// tracks the later one; the second fib's top (section 9) stays pinned
// to the establishing break -- proving the two diverge.
func TestIntegration_TransactionalLiquidity_TracksMoreRecentValidatedBreak(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	seedBullishEstablished(t, store, instrumentID)
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep + protected low (70)
		{21, 128, 93, 102},
		{22, 126, 94, 103},
		{25, 135, 120, 130}, // establishing break: close 130 > H1 (125)
		{28, 132, 125, 128},
		{29, 134, 126, 129},
		{30, 140, 127, 131}, // a brand-new, higher swing high (140)
		{31, 133, 128, 130},
		{32, 131, 129, 130},
		{35, 148, 138, 145}, // later validated break: close 145 > the new high (140)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(36)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	newHigh, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingHigh, clock.NewEventTime(et(30)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (new high): %v", err)
	}
	if newHigh.Price != 140 {
		t.Fatalf("new high price = %v, want 140", newHigh.Price)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}

	transactional, ok, err := ResolveTransactionalLiquidity(context.Background(), store, state, et(40))
	if err != nil || !ok {
		t.Fatalf("ResolveTransactionalLiquidity: ok=%v err=%v", ok, err)
	}
	if transactional.ID != newHigh.ID || transactional.Price != 140 {
		t.Fatalf("transactional liquidity = %+v, want id %d price 140 (the later validated break's reference, not the establishing one)", transactional, newHigh.ID)
	}

	secondFib, ok, err := ResolveSecondFib(context.Background(), store, state)
	if err != nil || !ok {
		t.Fatalf("ResolveSecondFib: ok=%v err=%v", ok, err)
	}
	if secondFib.High != 125 {
		t.Fatalf("second fib top = %v, want 125 (pinned to the establishing break, unlike transactional liquidity)", secondFib.High)
	}
	if transactional.Price == secondFib.High {
		t.Fatalf("transactional liquidity (%v) and the second fib top (%v) coincide; this fixture requires them to diverge", transactional.Price, secondFib.High)
	}
}

// TestIntegration_TransactionalLiquidity_UnvalidatedBreaksDoNotUpdateIt
// proves that only a sweep-validated break updates the level: after
// the only validated break (minute 25, referencing H1/125), the
// protected low is invalidated (CHOCH to bearish) and then an
// unvalidated CHOCH flips back to bullish, followed by an unvalidated
// BOS against a brand-new high. Neither later break is sweep-validated
// (no protected low survives or is re-established), so the current
// transactional liquidity must remain exactly what the first validated
// break set.
func TestIntegration_TransactionalLiquidity_UnvalidatedBreaksDoNotUpdateIt(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	highID, _ := seedBullishEstablished(t, store, instrumentID)
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep + protected low (70)
		{21, 128, 93, 102},
		{22, 126, 94, 103},
		{25, 135, 120, 130}, // validated BOS: close 130 > H1 (125)
		{30, 75, 60, 65},    // CHOCH to bearish: close 65 < 70, invalidates the protected low
		{35, 130, 100, 128}, // unvalidated CHOCH back to bullish: close 128 > H1 (125), no protected low exists
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 140, 95, 100}, // a brand-new swing high confirms (140)
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{45, 145, 135, 142}, // unvalidated BOS: close 142 > 140, still no protected low
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(46)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	// Preconditions: both later breaks exist but are unvalidated.
	choch, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", et(35))
	if err != nil {
		t.Fatalf("GetCurrentEvent (minute 35): %v", err)
	}
	if choch.Type != EventCHOCH || choch.DirectionAfter != DirectionBullish || choch.SweepValidated {
		t.Fatalf("event at minute 35 = %+v, want CHOCH -> Bullish, unvalidated", choch)
	}
	bos, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", et(45))
	if err != nil {
		t.Fatalf("GetCurrentEvent (minute 45): %v", err)
	}
	if bos.Type != EventBOS || bos.DirectionAfter != DirectionBullish || bos.SweepValidated {
		t.Fatalf("event at minute 45 = %+v, want BOS, Bullish, unvalidated", bos)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	transactional, ok, err := ResolveTransactionalLiquidity(context.Background(), store, state, et(50))
	if err != nil || !ok {
		t.Fatalf("ResolveTransactionalLiquidity: ok=%v err=%v", ok, err)
	}
	if transactional.ID != highID || transactional.Price != 125 {
		t.Fatalf("transactional liquidity = %+v, want id %d price 125 (the only validated break, unchanged by the two later unvalidated ones)", transactional, highID)
	}
}

// TestIntegration_TransactionalLiquidity_PointInTimeSafe proves asOf
// governs which validated break is used: before the later validated
// break's trigger candle (minute 35) has closed, transactional
// liquidity is still the establishing break's level (125); once it has
// closed, it updates to the later break's level (140).
func TestIntegration_TransactionalLiquidity_PointInTimeSafe(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	highID, _ := seedBullishEstablished(t, store, instrumentID)
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep + protected low (70)
		{21, 128, 93, 102},
		{22, 126, 94, 103},
		{25, 135, 120, 130}, // establishing validated break: close 130 > H1 (125)
		{28, 132, 125, 128},
		{29, 134, 126, 129},
		{30, 140, 127, 131}, // a brand-new, higher swing high (140)
		{31, 133, 128, 130},
		{32, 131, 129, 130},
		{35, 148, 138, 145}, // later validated break, trigger candle closes at minute 36
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(36)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	newHigh, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingHigh, clock.NewEventTime(et(30)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (new high): %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}

	before, ok, err := ResolveTransactionalLiquidity(context.Background(), store, state, et(35))
	if err != nil || !ok {
		t.Fatalf("ResolveTransactionalLiquidity (asOf=35): ok=%v err=%v", ok, err)
	}
	if before.ID != highID || before.Price != 125 {
		t.Fatalf("asOf=35: transactional liquidity = %+v, want id %d price 125: minute 35's trigger candle closes at minute 36, strictly after asOf -- not yet known", before, highID)
	}

	after, ok, err := ResolveTransactionalLiquidity(context.Background(), store, state, et(36))
	if err != nil || !ok {
		t.Fatalf("ResolveTransactionalLiquidity (asOf=36): ok=%v err=%v", ok, err)
	}
	if after.ID != newHigh.ID || after.Price != 140 {
		t.Fatalf("asOf=36: transactional liquidity = %+v, want id %d price 140: minute 35's trigger candle has now closed", after, newHigh.ID)
	}
}

func TestIntegration_TransactionalLiquidity_UndefinedWhenNoValidatedBreakYet(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)

	seedBullishEstablished(t, store, instrumentID)

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	_, ok, err := ResolveTransactionalLiquidity(context.Background(), store, state, et(10))
	if err != nil {
		t.Fatalf("ResolveTransactionalLiquidity: %v", err)
	}
	if ok {
		t.Fatal("ResolveTransactionalLiquidity ok = true, want false: no sweep-validated break has occurred yet")
	}
}

func TestIntegration_TransactionalLiquidity_UndefinedWhenDirectionUndetermined(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.Direction != DirectionUndetermined {
		t.Fatalf("precondition: Direction = %v, want Undetermined", state.Direction)
	}
	_, ok, err := ResolveTransactionalLiquidity(context.Background(), store, state, et(10))
	if err != nil {
		t.Fatalf("ResolveTransactionalLiquidity: %v", err)
	}
	if ok {
		t.Fatal("ResolveTransactionalLiquidity ok = true, want false: no active structure")
	}
}
