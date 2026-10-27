//go:build integration

package analysis

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

// Batch 2 golden fixtures, strategy-spec.md v0.3 "Reading B" (sections
// 6-7): every bullet in the spec's batch-2 golden-fixtures list, each
// built so the wrong behavior produces a visibly wrong verdict.
//
// Reading B's central correction over the discarded reading: a break
// is sweep-validated by a valid OPPOSING-side protected anchor that
// exists AS OF the break -- never by the broken swing itself having
// been swept. The sweep and the break it validates are on opposite
// sides and are generally different candles and different levels.
// TestIntegration_SweepValidation_ExplicitNegative_SameSwingIsNotEnough
// is the fixture that specifically proves the discarded reading fails.

func TestIntegration_Sweep_SellSideTakesALow(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	target := seedSwing(t, store, instrumentID, SwingLow, 2, 100, []int64{1, 2, 3, 4, 5})
	feedCloseCandles(t, db, instrumentID, []closeBar{{10, 110, 95, 102}})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(10), et(11)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	sweep, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(10), SweepLow)
	if err != nil {
		t.Fatalf("GetCurrentSweepByTrigger: %v", err)
	}
	if sweep.SweptSwingID != target.ID || sweep.PenetrationExtreme != 95 || sweep.ReclaimClose != 102 {
		t.Fatalf("sweep = %+v, want swept=%d penetration=95 reclaim=102", sweep, target.ID)
	}
	if sweep.Side.String() != "sell" {
		t.Fatalf(`sweep.Side.String() = %q, want "sell": a sweep of a LOW is sell-side per the section 6 glossary`, sweep.Side.String())
	}
}

func TestIntegration_Sweep_BuySideTakesAHigh(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	target := seedSwing(t, store, instrumentID, SwingHigh, 2, 200, []int64{1, 2, 3, 4, 5})
	feedCloseCandles(t, db, instrumentID, []closeBar{{10, 210, 190, 198}})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(10), et(11)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	sweep, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(10), SweepHigh)
	if err != nil {
		t.Fatalf("GetCurrentSweepByTrigger: %v", err)
	}
	if sweep.SweptSwingID != target.ID || sweep.PenetrationExtreme != 210 || sweep.ReclaimClose != 198 {
		t.Fatalf("sweep = %+v, want swept=%d penetration=210 reclaim=198", sweep, target.ID)
	}
	if sweep.Side.String() != "buy" {
		t.Fatalf(`sweep.Side.String() = %q, want "buy": a sweep of a HIGH is buy-side per the section 6 glossary`, sweep.Side.String())
	}
}

func TestIntegration_Sweep_TouchOnlyIsNotASweep(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	seedSwing(t, store, instrumentID, SwingLow, 2, 100, []int64{1, 2, 3, 4, 5})
	feedCloseCandles(t, db, instrumentID, []closeBar{{10, 110, 100, 105}})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(10), et(11)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}
	if _, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(10), SweepLow); !errors.Is(err, ErrSweepNotFound) {
		t.Fatalf("GetCurrentSweepByTrigger = %v, want ErrSweepNotFound: a touch (low == level) must not register as a sweep", err)
	}
}

func TestIntegration_Sweep_PenetratesWithoutReclaimIsNotASweep(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	seedSwing(t, store, instrumentID, SwingLow, 2, 100, []int64{1, 2, 3, 4, 5})
	feedCloseCandles(t, db, instrumentID, []closeBar{{10, 100, 90, 95}})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(10), et(11)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}
	if _, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(10), SweepLow); !errors.Is(err, ErrSweepNotFound) {
		t.Fatalf("GetCurrentSweepByTrigger = %v, want ErrSweepNotFound: penetration without a reclaiming close must not register as a sweep", err)
	}
}

func TestIntegration_Sweep_TwoCandleReclaimIsNotASweep(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	seedSwing(t, store, instrumentID, SwingLow, 2, 100, []int64{1, 2, 3, 4, 5})
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{10, 98, 85, 92},
		{11, 108, 102, 105},
	})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(10), et(12)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}
	if _, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(10), SweepLow); !errors.Is(err, ErrSweepNotFound) {
		t.Fatalf("candle A: GetCurrentSweepByTrigger = %v, want ErrSweepNotFound", err)
	}
	if _, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(11), SweepLow); !errors.Is(err, ErrSweepNotFound) {
		t.Fatalf("candle B: GetCurrentSweepByTrigger = %v, want ErrSweepNotFound: reclaim must be on the same candle as the penetration", err)
	}
}

func TestIntegration_SweepValidatedBOS_Bullish_OppositeSideDifferentLevel(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	highID, _ := seedBullishEstablished(t, store, instrumentID)

	// 20 is a sell-side sweep of the seeded low (90) whose reversal-
	// origin (70) is ALSO an isolated fractal low: the protected low.
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep: low 70 < 90, close 95 > 90
		{21, 128, 93, 102},
		{22, 126, 94, 103},
		// 25 breaks above H1 (125) -- the HIGH side, entirely unrelated
		// to the sweep on the LOW side. This is Reading B's point: the
		// sweep and the break are on opposite sides and different levels.
		{25, 135, 120, 130},
	})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	sweep, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(20), SweepLow)
	if err != nil {
		t.Fatalf("GetCurrentSweepByTrigger: %v", err)
	}
	protected, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(20)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (protected low): %v", err)
	}
	if protected.Price != 70 {
		t.Fatalf("protected low price = %v, want 70", protected.Price)
	}

	event, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", mustCandleIDAt(t, db, instrumentID, 25))
	if err != nil {
		t.Fatalf("GetCurrentEvent at minute 25: %v", err)
	}
	if event.Type != EventBOS || event.DirectionAfter != DirectionBullish {
		t.Fatalf("event = %+v, want BOS, stays Bullish", event)
	}
	if len(event.ReferenceSwingIDs) != 1 || event.ReferenceSwingIDs[0] != highID {
		t.Fatalf("event.ReferenceSwingIDs = %v, want [%d] (H1, the break target -- unrelated to the sweep)", event.ReferenceSwingIDs, highID)
	}
	if !event.SweepValidated {
		t.Fatal("event.SweepValidated = false, want true: a valid protected low exists on the opposite side from this bullish break")
	}
	if event.SweepEventID == nil || *event.SweepEventID != sweep.ID {
		t.Fatalf("event.SweepEventID = %v, want %d", event.SweepEventID, sweep.ID)
	}
	if event.ProtectedSwingID == nil || *event.ProtectedSwingID != protected.ID {
		t.Fatalf("event.ProtectedSwingID = %v, want %d (the protected low, not H1)", event.ProtectedSwingID, protected.ID)
	}
}

func TestIntegration_SweepValidatedBOS_Bearish_OppositeSideDifferentLevel(t *testing.T) {
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

	// 20 is a buy-side sweep of H1 (125) whose reversal-origin (150) is
	// an isolated fractal high: the protected high. 25 breaks below L1
	// (90) -- the LOW side, unrelated to the sweep.
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 105, 95, 100},
		{19, 108, 96, 102},
		{20, 150, 100, 115}, // sweep: high 150 > 125, close 115 < 125
		{21, 107, 98, 103},
		{22, 106, 97, 104},
		{25, 80, 65, 70}, // close 70 < L1 (90): BOS, stays bearish
	})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	sweep, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(20), SweepHigh)
	if err != nil {
		t.Fatalf("GetCurrentSweepByTrigger: %v", err)
	}
	protected, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingHigh, clock.NewEventTime(et(20)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (protected high): %v", err)
	}
	if protected.Price != 150 {
		t.Fatalf("protected high price = %v, want 150", protected.Price)
	}

	event, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", mustCandleIDAt(t, db, instrumentID, 25))
	if err != nil {
		t.Fatalf("GetCurrentEvent at minute 25: %v", err)
	}
	if event.Type != EventBOS || event.DirectionAfter != DirectionBearish {
		t.Fatalf("event = %+v, want BOS, stays Bearish", event)
	}
	if len(event.ReferenceSwingIDs) != 1 || event.ReferenceSwingIDs[0] != l1.ID {
		t.Fatalf("event.ReferenceSwingIDs = %v, want [%d] (L1, the break target)", event.ReferenceSwingIDs, l1.ID)
	}
	if !event.SweepValidated {
		t.Fatal("event.SweepValidated = false, want true: a valid protected high exists on the opposite side from this bearish break")
	}
	if event.SweepEventID == nil || *event.SweepEventID != sweep.ID {
		t.Fatalf("event.SweepEventID = %v, want %d", event.SweepEventID, sweep.ID)
	}
	if event.ProtectedSwingID == nil || *event.ProtectedSwingID != protected.ID {
		t.Fatalf("event.ProtectedSwingID = %v, want %d (the protected high, not L1)", event.ProtectedSwingID, protected.ID)
	}
}

// TestIntegration_SweepValidation_ExplicitNegative_SameSwingIsNotEnough
// is the discarded-reading regression guard the spec explicitly calls
// out: a break of the swing that was ITSELF the sweep's reversal-origin
// must NOT be sweep-validated when no valid OPPOSITE-side anchor
// exists. The discarded same-swing reading would wrongly validate this
// (the broken swing literally carries a sweep); Reading B correctly
// does not.
func TestIntegration_SweepValidation_ExplicitNegative_SameSwingIsNotEnough(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	seedBullishEstablished(t, store, instrumentID)

	// 20 establishes a protected low (70) via a sell-side sweep, exactly
	// as the positive fixture. 25 closes BELOW that same protected low
	// (70) -- a CHOCH whose own broken swing is the sweep's reversal-
	// origin. No protected HIGH exists anywhere in this fixture, so this
	// CHOCH (resulting in Bearish) must not validate.
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep + protected low
		{21, 128, 93, 102},
		{22, 126, 94, 103},
	})
	// Evaluated in two stages so the protected low's own id is captured
	// here, before the CHOCH below retracts it.
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(23)); err != nil {
		t.Fatalf("EvaluateRange (stage 1): %v", err)
	}
	protected, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(20)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (protected low): %v", err)
	}

	feedCloseCandles(t, db, instrumentID, []closeBar{
		{25, 75, 60, 65}, // close 65 < 70: CHOCH through the protected low itself
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(23), et(26)); err != nil {
		t.Fatalf("EvaluateRange (stage 2): %v", err)
	}

	event, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", mustCandleIDAt(t, db, instrumentID, 25))
	if err != nil {
		t.Fatalf("GetCurrentEvent at minute 25: %v", err)
	}
	if event.Type != EventCHOCH || event.DirectionAfter != DirectionBearish {
		t.Fatalf("event = %+v, want CHOCH -> Bearish", event)
	}
	if len(event.ReferenceSwingIDs) != 1 || event.ReferenceSwingIDs[0] != protected.ID {
		// The broken swing IS the sweep's reversal-origin -- the whole
		// point of this fixture.
		t.Fatalf("event.ReferenceSwingIDs = %v, want [%d] (the broken swing is the sweep's own reversal-origin)", event.ReferenceSwingIDs, protected.ID)
	}
	if event.SweepValidated {
		t.Fatalf("event.SweepValidated = true, want false: the discarded same-swing reading would wrongly validate here (the broken swing carries a sweep), but no opposite-side (HIGH) protected anchor exists -- Reading B must not validate")
	}
	if event.SweepEventID != nil || event.ProtectedSwingID != nil {
		t.Fatalf("event sweep references = (sweepID=%v, protectedID=%v), want (nil, nil)", event.SweepEventID, event.ProtectedSwingID)
	}
}

func TestIntegration_BOS_NotSweepValidated_DirectionStillUpdates(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	seedBullishEstablished(t, store, instrumentID)
	feedCloseCandles(t, db, instrumentID, []closeBar{{30, 135, 120, 130}})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(30), et(31)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	event, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", mustCandleIDAt(t, db, instrumentID, 30))
	if err != nil {
		t.Fatalf("GetCurrentEvent: %v", err)
	}
	if event.Type != EventBOS || event.DirectionAfter != DirectionBullish {
		t.Fatalf("event = %+v, want BOS, direction still updated to stay Bullish", event)
	}
	if event.SweepValidated || event.SweepEventID != nil || event.ProtectedSwingID != nil {
		t.Fatalf("event sweep tag = (validated=%v, sweepID=%v, protectedID=%v), want (false, nil, nil): no qualifying sweep exists",
			event.SweepValidated, event.SweepEventID, event.ProtectedSwingID)
	}
}

// TestIntegration_ProtectedAnchor_PersistsAcrossTrend proves the anchor
// is a designated anchor, not "the latest confirmed opposing swing":
// a LATER same-direction BOS, built from a brand-new, higher swing high
// that confirms long after the anchor, is still validated by -- and
// references -- the SAME ORIGINAL protected low.
func TestIntegration_ProtectedAnchor_PersistsAcrossTrend(t *testing.T) {
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
		{25, 135, 120, 130}, // BOS #1: close 130 > H1 (125)
		{28, 132, 125, 128},
		{29, 134, 126, 129},
		{30, 140, 127, 131}, // a brand-new, higher swing high (140)
		{31, 133, 128, 130},
		{32, 131, 129, 130},
		{35, 148, 138, 145}, // BOS #2: close 145 > the NEW high (140)
	})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(36)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	protected, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(20)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (protected low): %v", err)
	}

	newHigh, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingHigh, clock.NewEventTime(et(30)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (new high at minute 30): %v", err)
	}
	if newHigh.Price != 140 {
		t.Fatalf("new high price = %v, want 140", newHigh.Price)
	}

	bos1, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", mustCandleIDAt(t, db, instrumentID, 25))
	if err != nil {
		t.Fatalf("GetCurrentEvent (BOS #1): %v", err)
	}
	bos2, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", mustCandleIDAt(t, db, instrumentID, 35))
	if err != nil {
		t.Fatalf("GetCurrentEvent (BOS #2): %v", err)
	}

	if bos2.Type != EventBOS || bos2.DirectionAfter != DirectionBullish {
		t.Fatalf("BOS #2 = %+v, want BOS, stays Bullish", bos2)
	}
	if len(bos2.ReferenceSwingIDs) != 1 || bos2.ReferenceSwingIDs[0] != newHigh.ID {
		t.Fatalf("BOS #2.ReferenceSwingIDs = %v, want [%d] (the new high it broke)", bos2.ReferenceSwingIDs, newHigh.ID)
	}
	if !bos2.SweepValidated {
		t.Fatal("BOS #2.SweepValidated = false, want true: the original protected low still stands")
	}
	if bos1.ProtectedSwingID == nil || bos2.ProtectedSwingID == nil || *bos1.ProtectedSwingID != *bos2.ProtectedSwingID {
		t.Fatalf("BOS #1 protected=%v, BOS #2 protected=%v, want the SAME original anchor on both, not 'the latest confirmed opposing swing'", bos1.ProtectedSwingID, bos2.ProtectedSwingID)
	}
	if *bos2.ProtectedSwingID != protected.ID {
		t.Fatalf("BOS #2.ProtectedSwingID = %d, want %d (the original protected low from minute 20)", *bos2.ProtectedSwingID, protected.ID)
	}
}

// TestIntegration_ProtectedAnchor_SupersessionGuardrail proves an
// incidental penetration that never produces a confirmed reversal-
// origin swing does not reset the anchor: the sweep is recorded as
// evidence, but since it never confirms as a fractal, the original
// anchor established earlier must survive untouched.
func TestIntegration_ProtectedAnchor_SupersessionGuardrail(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	seedBullishEstablished(t, store, instrumentID)

	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep + protected low (70), the original anchor
		{21, 128, 93, 102},
		{22, 126, 94, 103},
		{23, 100, 83, 92},
		{24, 100, 82, 91},
		{25, 100, 60, 75}, // incidental sweep: low 60 < 70 (current ref low), close 75 > 70
		{26, 100, 55, 80}, // lower low disqualifies 25's fractal status
		{27, 100, 81, 85},
	})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(28)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	original, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(20)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (original protected low): %v", err)
	}

	// The incidental sweep is recorded as evidence regardless of fractal status.
	if _, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(25), SweepLow); err != nil {
		t.Fatalf("GetCurrentSweepByTrigger (incidental sweep at minute 25): %v, want it to exist as evidence", err)
	}
	// But it never confirms as a fractal.
	if _, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(25))); !errors.Is(err, ErrSwingNotFound) {
		t.Fatalf("GetCurrentSwing at minute 25 = %v, want ErrSwingNotFound: a lower low at 26 disqualifies it", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.ProtectedSwingID == nil || *state.ProtectedSwingID != original.ID {
		t.Fatalf("state.ProtectedSwingID = %v, want %d (the ORIGINAL anchor from minute 20, untouched by the incidental, non-confirming sweep at 25)", state.ProtectedSwingID, original.ID)
	}
	if state.ProtectedSwingSide == nil || *state.ProtectedSwingSide != ProtectedLow {
		t.Fatalf("state.ProtectedSwingSide = %v, want ProtectedLow", state.ProtectedSwingSide)
	}
}

// TestIntegration_ProtectedAnchor_InvalidatedByCloseThrough proves a
// finalized close through the protected swing ends its eligibility: the
// CHOCH that does it is also the direction flip (spec section 3/7), and
// afterward the live state shows no anchor at all.
func TestIntegration_ProtectedAnchor_InvalidatedByCloseThrough(t *testing.T) {
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
		{25, 75, 60, 65}, // close 65 < 70: closes strictly through the protected low
	})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	event, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", mustCandleIDAt(t, db, instrumentID, 25))
	if err != nil {
		t.Fatalf("GetCurrentEvent at minute 25: %v", err)
	}
	if event.Type != EventCHOCH || event.DirectionBefore != DirectionBullish || event.DirectionAfter != DirectionBearish {
		t.Fatalf("event = %+v, want CHOCH, Bullish -> Bearish: the close through the protected swing IS the CHOCH", event)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.ProtectedSwingID != nil || state.ProtectedSwingSide != nil {
		t.Fatalf("state protected anchor = (id=%v, side=%v), want (nil, nil): invalidation must end the anchor's eligibility", state.ProtectedSwingID, state.ProtectedSwingSide)
	}
}

func TestIntegration_ProtectedAnchor_RequiresFractalConfirmation(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	seedSwing(t, store, instrumentID, SwingLow, 2, 90, []int64{1, 2, 3, 4, 5})
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 105, 95, 100},
		{19, 108, 96, 101},
		{20, 100, 70, 95}, // sweeps the seeded low, but...
		{21, 108, 65, 72}, // ...a lower low disqualifies 20's fractal status
		{22, 106, 97, 103},
	})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(23)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	sweep, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(20), SweepLow)
	if err != nil {
		t.Fatalf("GetCurrentSweepByTrigger: %v, want the sweep itself to exist as evidence regardless of fractal confirmation", err)
	}
	if sweep.PenetrationExtreme != 70 || sweep.ReclaimClose != 95 {
		t.Fatalf("sweep = %+v, want penetration=70 reclaim=95", sweep)
	}

	if _, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(20))); !errors.Is(err, ErrSwingNotFound) {
		t.Fatalf("GetCurrentSwing at the sweep's pivot position = %v, want ErrSwingNotFound", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.ProtectedSwingID != nil {
		t.Fatalf("state.ProtectedSwingID = %v, want nil: a raw sweep extreme that never confirms must never become a protected anchor", state.ProtectedSwingID)
	}
}

func TestIntegration_CorrectionRecompute_SweepAnchorAndDependentTag_PreservesPriorEvidence(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	mdStore := marketdata.NewStore(db, clock.Real{})

	h1 := seedSwing(t, store, instrumentID, SwingHigh, 2, 125, []int64{1, 2, 3, 4, 5})
	l1 := seedSwing(t, store, instrumentID, SwingLow, 3, 90, []int64{2, 3, 4, 5, 6})
	establishTriggerID := feedCloseCandles(t, db, instrumentID, []closeBar{{1, 105, 95, 100}})[1]
	establishEvent, err := store.InsertEvent(context.Background(), NewStructureEvent{
		InstrumentID: instrumentID, Timeframe: "1m", Type: EventDirectionEstablished,
		DirectionBefore: DirectionUndetermined, DirectionAfter: DirectionBullish,
		TriggerCandleID: establishTriggerID, ReferenceSwingIDs: []int64{h1.ID, h1.ID, l1.ID, l1.ID},
		EvaluationTime: clockEvalTime(1), AlgorithmVersion: testAlgorithmVersion,
	})
	if err != nil {
		t.Fatalf("seed establishment event: %v", err)
	}
	if err := store.SetState(context.Background(), StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
		ReferenceSwingHighID: &h1.ID, ReferenceSwingLowID: &l1.ID, LastEventID: &establishEvent.ID,
	}, nowUTC()); err != nil {
		t.Fatalf("seed live state: %v", err)
	}

	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep + protected low, before correction
		{21, 128, 93, 102},
		{22, 126, 94, 103},
		{25, 135, 120, 130}, // BOS above H1, validated by the opposite-side anchor
	})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("initial EvaluateRange: %v", err)
	}

	oldEvent, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", mustCandleIDAt(t, db, instrumentID, 25))
	if err != nil {
		t.Fatalf("precondition GetCurrentEvent: %v", err)
	}
	if !oldEvent.SweepValidated {
		t.Fatalf("precondition: oldEvent = %+v, want sweep-validated", oldEvent)
	}

	// Correct minute 20: raise its low from 70 to 91 -- it no longer
	// sweeps L1 (90), and 91 ties neighbor 19's low (91), so equality
	// disqualifies it as a fractal low too. Both the sweep and the
	// protected low it created must retract; the BOS must recompute
	// with its tag now false (no anchor survives to validate it).
	_, err = mdStore.SupersedeCandle(context.Background(), instrumentID, clockEventTime(20), marketdata.Correction{
		Open: 95, High: 100, Low: 91, Close: 95, Volume: 1, Provider: "test", IngestedAt: clock.NewIngestionTime(time.Now()),
	})
	if err != nil {
		t.Fatalf("SupersedeCandle: %v", err)
	}
	if err := e.HandleCandleCorrection(context.Background(), instrumentID, "1m", et(20)); err != nil {
		t.Fatalf("HandleCandleCorrection: %v", err)
	}

	if _, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(20), SweepLow); !errors.Is(err, ErrSweepNotFound) {
		t.Fatalf("GetCurrentSweepByTrigger after correction = %v, want ErrSweepNotFound", err)
	}
	if _, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(20))); !errors.Is(err, ErrSwingNotFound) {
		t.Fatalf("GetCurrentSwing after correction = %v, want ErrSwingNotFound", err)
	}

	preserved, err := getEventByID(context.Background(), db, oldEvent.ID)
	if err != nil {
		t.Fatalf("getEventByID (old, preserved): %v", err)
	}
	if !preserved.SweepValidated || preserved.SupersededAt == nil || preserved.SupersededBy == nil {
		t.Fatalf("preserved old event = %+v, want SweepValidated=true (unchanged) and superseded with a replacement", preserved)
	}

	newEvent, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", mustCandleIDAt(t, db, instrumentID, 25))
	if err != nil {
		t.Fatalf("GetCurrentEvent after correction: %v", err)
	}
	if newEvent.ID == oldEvent.ID {
		t.Fatal("current event after correction is still the old row, want a new superseding row")
	}
	if newEvent.SweepValidated {
		t.Fatal("newEvent.SweepValidated = true, want false: no protected anchor survives the correction to validate it")
	}
	if newEvent.Type != EventBOS || newEvent.DirectionAfter != DirectionBullish {
		t.Fatalf("newEvent = %+v, want BOS, stays Bullish (the break itself is unaffected by the correction)", newEvent)
	}
}

// TestIntegration_CorrectionReplay_ReconstructsProtectedAnchorAsOf is
// the dedicated fixture for the subtlety the correction path's
// as-of reconstruction has to handle: a correction whose affected range
// starts strictly AFTER the protected anchor's own establishment must
// not re-derive the anchor from scratch (nothing in the replayed range
// touches it) -- it must recover it from reconstructProtectedAnchorAsOf.
// If reconstruction were wrong (returning no anchor), the replay would
// start blind and the later BOS would come back wrongly unvalidated
// with a brand-new row; this fixture is built so that failure mode is
// visible as a changed event id, not merely a changed boolean.
func TestIntegration_CorrectionReplay_ReconstructsProtectedAnchorAsOf(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	mdStore := marketdata.NewStore(db, clock.Real{})

	h1 := seedSwing(t, store, instrumentID, SwingHigh, 2, 125, []int64{1, 2, 3, 4, 5})
	l1 := seedSwing(t, store, instrumentID, SwingLow, 3, 90, []int64{2, 3, 4, 5, 6})
	establishTriggerID := feedCloseCandles(t, db, instrumentID, []closeBar{{1, 105, 95, 100}})[1]
	establishEvent, err := store.InsertEvent(context.Background(), NewStructureEvent{
		InstrumentID: instrumentID, Timeframe: "1m", Type: EventDirectionEstablished,
		DirectionBefore: DirectionUndetermined, DirectionAfter: DirectionBullish,
		TriggerCandleID: establishTriggerID, ReferenceSwingIDs: []int64{h1.ID, h1.ID, l1.ID, l1.ID},
		EvaluationTime: clockEvalTime(1), AlgorithmVersion: testAlgorithmVersion,
	})
	if err != nil {
		t.Fatalf("seed establishment event: %v", err)
	}
	if err := store.SetState(context.Background(), StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
		ReferenceSwingHighID: &h1.ID, ReferenceSwingLowID: &l1.ID, LastEventID: &establishEvent.ID,
	}, nowUTC()); err != nil {
		t.Fatalf("seed live state: %v", err)
	}

	// 20 establishes the protected low (confirmed_at = 23). Everything
	// from 23 through 29 is boring filler, placed so the correction
	// target (30) yields affectedFrom = 28 -- strictly after 23, so the
	// replay NEVER touches position 20 again. 35 is a BOS above H1,
	// which this anchor validates.
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep + protected low (70)
		{21, 128, 93, 102},
		{22, 126, 94, 103},
		{23, 105, 95, 100},
		{24, 105, 95, 100},
		{25, 105, 95, 100},
		{26, 105, 95, 100},
		{27, 105, 95, 100},
		{28, 105, 95, 100},
		{29, 105, 95, 100},
		{30, 108, 96, 102},  // the correction target -- unrelated to the anchor
		{35, 135, 120, 130}, // BOS: close 130 > H1 (125)
	})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(36)); err != nil {
		t.Fatalf("initial EvaluateRange: %v", err)
	}

	protected, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(20)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (protected low): %v", err)
	}
	beforeEvent, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", mustCandleIDAt(t, db, instrumentID, 35))
	if err != nil {
		t.Fatalf("precondition GetCurrentEvent: %v", err)
	}
	if !beforeEvent.SweepValidated || beforeEvent.ProtectedSwingID == nil || *beforeEvent.ProtectedSwingID != protected.ID {
		t.Fatalf("precondition: beforeEvent = %+v, want sweep-validated referencing protected low %d", beforeEvent, protected.ID)
	}

	// Trivial correction to minute 30 -- same values, just a fresh
	// revision -- purely to drive affectedFrom = 28 via HandleCandleCorrection.
	_, err = mdStore.SupersedeCandle(context.Background(), instrumentID, clockEventTime(30), marketdata.Correction{
		Open: 102, High: 108, Low: 96, Close: 103, Volume: 1, Provider: "test", IngestedAt: clock.NewIngestionTime(time.Now()),
	})
	if err != nil {
		t.Fatalf("SupersedeCandle: %v", err)
	}
	if err := e.HandleCandleCorrection(context.Background(), instrumentID, "1m", et(30)); err != nil {
		t.Fatalf("HandleCandleCorrection: %v", err)
	}

	afterEvent, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", mustCandleIDAt(t, db, instrumentID, 35))
	if err != nil {
		t.Fatalf("GetCurrentEvent after correction: %v", err)
	}
	if afterEvent.ID != beforeEvent.ID {
		t.Fatalf("event id changed from %d to %d: the replay re-evaluated minute 35 with a DIFFERENT (wrongly reconstructed) anchor instead of recognizing identical evidence and skipping", beforeEvent.ID, afterEvent.ID)
	}
	if !afterEvent.SweepValidated || afterEvent.ProtectedSwingID == nil || *afterEvent.ProtectedSwingID != protected.ID {
		t.Fatalf("afterEvent = %+v, want still sweep-validated referencing the SAME protected low %d, reconstructed as-of the resume point rather than re-derived", afterEvent, protected.ID)
	}
}
