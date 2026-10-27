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

// Batch 2 golden fixtures (strategy-spec.md v0.2 sections 6-7): every
// bullet in the spec's "Golden fixtures required / Batch 2" list, each
// built so the wrong behavior produces a visibly wrong verdict.

func TestIntegration_Sweep_BuySide(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	target := seedSwing(t, store, instrumentID, SwingLow, 2, 100, []int64{1, 2, 3, 4, 5})
	feedCloseCandles(t, db, instrumentID, []closeBar{{10, 110, 95, 102}})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(10), et(11)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	sweep, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(10), SweepBuy)
	if err != nil {
		t.Fatalf("GetCurrentSweepByTrigger: %v", err)
	}
	if sweep.SweptSwingID != target.ID || sweep.PenetrationExtreme != 95 || sweep.ReclaimClose != 102 {
		t.Fatalf("sweep = %+v, want swept=%d penetration=95 reclaim=102", sweep, target.ID)
	}
}

func TestIntegration_Sweep_SellSide(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	target := seedSwing(t, store, instrumentID, SwingHigh, 2, 200, []int64{1, 2, 3, 4, 5})
	feedCloseCandles(t, db, instrumentID, []closeBar{{10, 210, 190, 198}})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(10), et(11)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	sweep, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(10), SweepSell)
	if err != nil {
		t.Fatalf("GetCurrentSweepByTrigger: %v", err)
	}
	if sweep.SweptSwingID != target.ID || sweep.PenetrationExtreme != 210 || sweep.ReclaimClose != 198 {
		t.Fatalf("sweep = %+v, want swept=%d penetration=210 reclaim=198", sweep, target.ID)
	}
}

func TestIntegration_Sweep_TouchOnlyIsNotASweep(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	seedSwing(t, store, instrumentID, SwingLow, 2, 100, []int64{1, 2, 3, 4, 5})
	// low == level exactly: a touch, not a strict penetration.
	feedCloseCandles(t, db, instrumentID, []closeBar{{10, 110, 100, 105}})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(10), et(11)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}
	if _, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(10), SweepBuy); !errors.Is(err, ErrSweepNotFound) {
		t.Fatalf("GetCurrentSweepByTrigger = %v, want ErrSweepNotFound: a touch (low == level) must not register as a sweep", err)
	}
}

func TestIntegration_Sweep_PenetratesWithoutReclaimIsNotASweep(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	seedSwing(t, store, instrumentID, SwingLow, 2, 100, []int64{1, 2, 3, 4, 5})
	// Penetrates (low < 100) but the close never reclaims above it.
	feedCloseCandles(t, db, instrumentID, []closeBar{{10, 100, 90, 95}})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(10), et(11)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}
	if _, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(10), SweepBuy); !errors.Is(err, ErrSweepNotFound) {
		t.Fatalf("GetCurrentSweepByTrigger = %v, want ErrSweepNotFound: penetration without a reclaiming close must not register as a sweep", err)
	}
}

func TestIntegration_Sweep_TwoCandleReclaimIsNotASweep(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	seedSwing(t, store, instrumentID, SwingLow, 2, 100, []int64{1, 2, 3, 4, 5})
	// Candle A penetrates and closes below (no reclaim this candle).
	// Candle B's own low never goes below the level -- it only closes
	// back above -- so it does not independently sweep either. Same-
	// candle-only (spec section 6) means neither candle registers.
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{10, 98, 85, 92},
		{11, 108, 102, 105},
	})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(10), et(12)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}
	if _, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(10), SweepBuy); !errors.Is(err, ErrSweepNotFound) {
		t.Fatalf("candle A: GetCurrentSweepByTrigger = %v, want ErrSweepNotFound", err)
	}
	if _, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(11), SweepBuy); !errors.Is(err, ErrSweepNotFound) {
		t.Fatalf("candle B: GetCurrentSweepByTrigger = %v, want ErrSweepNotFound: reclaim must be on the same candle as the penetration, a delayed multi-candle reclaim is not a sweep in this version", err)
	}
}

func TestIntegration_SweepValidatedCHOCH(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	seedBullishEstablished(t, store, instrumentID)
	// 20 is a buy-side sweep of L1 (seeded, price 90) that is ALSO an
	// isolated fractal low (70, strictly below all four neighbors) --
	// the reversal-origin protected low. 25 closes below it: a CHOCH.
	ids := feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep: low 70 < 90, close 95 > 90
		{21, 128, 93, 102},
		{22, 126, 94, 103},
		{25, 90, 60, 65}, // close 65 < protected low 70: CHOCH, bullish -> bearish
	})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	sweep, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(20), SweepBuy)
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

	event, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", ids[25])
	if err != nil {
		t.Fatalf("GetCurrentEvent at minute 25: %v", err)
	}
	if event.Type != EventCHOCH || event.DirectionAfter != DirectionBearish {
		t.Fatalf("event = %+v, want CHOCH -> Bearish", event)
	}
	if !event.SweepValidated {
		t.Fatal("event.SweepValidated = false, want true: the broken low was created by the buy-side sweep at minute 20")
	}
	if event.SweepEventID == nil || *event.SweepEventID != sweep.ID {
		t.Fatalf("event.SweepEventID = %v, want %d", event.SweepEventID, sweep.ID)
	}
	if event.ProtectedSwingID == nil || *event.ProtectedSwingID != protected.ID {
		t.Fatalf("event.ProtectedSwingID = %v, want %d", event.ProtectedSwingID, protected.ID)
	}
}

func TestIntegration_SweepValidatedBOS(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	seedBullishEstablished(t, store, instrumentID)
	// 20 is a sell-side sweep of H1 (seeded, price 125) that is ALSO an
	// isolated fractal high (140) -- the protected high. 25 closes
	// above it while direction is still bullish: a continuation BOS.
	ids := feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 105, 95, 100},
		{19, 108, 96, 102},
		{20, 140, 100, 115}, // sweep: high 140 > 125, close 115 < 125
		{21, 107, 98, 103},
		{22, 106, 97, 104},
		{25, 155, 145, 150}, // close 150 > protected high 140: BOS, stays bullish
	})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	sweep, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(20), SweepSell)
	if err != nil {
		t.Fatalf("GetCurrentSweepByTrigger: %v", err)
	}
	protected, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingHigh, clock.NewEventTime(et(20)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (protected high): %v", err)
	}
	if protected.Price != 140 {
		t.Fatalf("protected high price = %v, want 140", protected.Price)
	}

	event, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", ids[25])
	if err != nil {
		t.Fatalf("GetCurrentEvent at minute 25: %v", err)
	}
	if event.Type != EventBOS || event.DirectionAfter != DirectionBullish {
		t.Fatalf("event = %+v, want BOS, stays Bullish", event)
	}
	if !event.SweepValidated {
		t.Fatal("event.SweepValidated = false, want true: the broken high was created by the sell-side sweep at minute 20")
	}
	if event.SweepEventID == nil || *event.SweepEventID != sweep.ID {
		t.Fatalf("event.SweepEventID = %v, want %d", event.SweepEventID, sweep.ID)
	}
	if event.ProtectedSwingID == nil || *event.ProtectedSwingID != protected.ID {
		t.Fatalf("event.ProtectedSwingID = %v, want %d", event.ProtectedSwingID, protected.ID)
	}
}

func TestIntegration_BOS_NotSweepValidated_DirectionStillUpdates(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	seedBullishEstablished(t, store, instrumentID)
	// A clean breakout with no sweep anywhere near either reference level.
	ids := feedCloseCandles(t, db, instrumentID, []closeBar{{30, 135, 120, 130}})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(30), et(31)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	event, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", ids[30])
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

func TestIntegration_ProtectedLevel_RequiresFractalConfirmation(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	seedSwing(t, store, instrumentID, SwingLow, 2, 90, []int64{1, 2, 3, 4, 5})
	// 20 sweeps the seeded low (70 < 90, close 95 > 90) but 21's own low
	// (65) is lower still, so 20 is strictly disqualified as a fractal
	// low -- the raw sweep extreme never confirms as a swing.
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 105, 95, 100},
		{19, 108, 96, 101},
		{20, 100, 70, 95},
		{21, 108, 65, 72}, // lower low disqualifies 20's fractal status
		{22, 106, 97, 103},
	})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(23)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	sweep, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(20), SweepBuy)
	if err != nil {
		t.Fatalf("GetCurrentSweepByTrigger: %v, want the sweep itself to exist as evidence regardless of fractal confirmation", err)
	}
	if sweep.PenetrationExtreme != 70 || sweep.ReclaimClose != 95 {
		t.Fatalf("sweep = %+v, want penetration=70 reclaim=95", sweep)
	}

	if _, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(20))); !errors.Is(err, ErrSwingNotFound) {
		t.Fatalf("GetCurrentSwing at the sweep's pivot position = %v, want ErrSwingNotFound: a raw sweep extreme that never confirms as a 2/2 fractal must not become a protected level", err)
	}
}

func TestIntegration_CorrectionRecompute_SweepAndProtectedTag_PreservesPriorEvidence(t *testing.T) {
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
	// InsertEvent only appends to the event log, which is what the later
	// correction's as-of reconstruction reads. The initial plain
	// EvaluateRange below reads the LIVE structure_state projection
	// instead, so it must be seeded too, exactly as HandleCandleCorrection
	// would have left it after processing this same establishment.
	if err := store.SetState(context.Background(), StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
		ReferenceSwingHighID: &h1.ID, ReferenceSwingLowID: &l1.ID, LastEventID: &establishEvent.ID,
	}, nowUTC()); err != nil {
		t.Fatalf("seed live state: %v", err)
	}

	ids := feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep + protected low, before correction
		{21, 128, 93, 102},
		{22, 126, 94, 103},
		{25, 90, 60, 65}, // CHOCH referencing the protected low
	})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("initial EvaluateRange: %v", err)
	}

	// Preconditions: sweep-validated CHOCH referencing the protected low.
	oldSweep, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(20), SweepBuy)
	if err != nil {
		t.Fatalf("precondition GetCurrentSweepByTrigger: %v", err)
	}
	oldProtected, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(20)))
	if err != nil {
		t.Fatalf("precondition GetCurrentSwing: %v", err)
	}
	oldEvent, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", ids[25])
	if err != nil {
		t.Fatalf("precondition GetCurrentEvent: %v", err)
	}
	if !oldEvent.SweepValidated || oldEvent.ProtectedSwingID == nil || *oldEvent.ProtectedSwingID != oldProtected.ID {
		t.Fatalf("precondition: oldEvent = %+v, want sweep-validated referencing protected low %d", oldEvent, oldProtected.ID)
	}

	// Correct minute 20: raise its low from 70 to 91 -- it no longer
	// penetrates below L1 (90), so it no longer sweeps, and 91 ties
	// neighbor 19's low (91), so equality disqualifies it as a fractal
	// low too. Both the sweep and its dependent protected swing must
	// retract; the CHOCH must recompute against whatever reference is
	// left (L1 again), losing its sweep validation.
	_, err = mdStore.SupersedeCandle(context.Background(), instrumentID, clockEventTime(20), marketdata.Correction{
		Open: 95, High: 100, Low: 91, Close: 95, Volume: 1, Provider: "test", IngestedAt: clock.NewIngestionTime(time.Now()),
	})
	if err != nil {
		t.Fatalf("SupersedeCandle: %v", err)
	}
	if err := e.HandleCandleCorrection(context.Background(), instrumentID, "1m", et(20)); err != nil {
		t.Fatalf("HandleCandleCorrection: %v", err)
	}

	// The sweep and the protected low are both gone from "current".
	if _, err := store.GetCurrentSweepByTrigger(context.Background(), instrumentID, "1m", et(20), SweepBuy); !errors.Is(err, ErrSweepNotFound) {
		t.Fatalf("GetCurrentSweepByTrigger after correction = %v, want ErrSweepNotFound: the corrected candle no longer sweeps", err)
	}
	if _, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(20))); !errors.Is(err, ErrSwingNotFound) {
		t.Fatalf("GetCurrentSwing after correction = %v, want ErrSwingNotFound: the dependent protected swing must retract with its sweep", err)
	}

	// The OLD CHOCH is preserved, exactly as it was, superseded by a new one.
	preserved, err := getEventByID(context.Background(), db, oldEvent.ID)
	if err != nil {
		t.Fatalf("getEventByID (old, preserved): %v", err)
	}
	if !preserved.SweepValidated || preserved.SupersededAt == nil || preserved.SupersededBy == nil {
		t.Fatalf("preserved old event = %+v, want SweepValidated=true (unchanged) and superseded with a replacement", preserved)
	}

	// The CURRENT CHOCH at minute 25 now references L1 again and has
	// lost sweep validation -- L1 was seeded directly, so its own
	// pivot candle (a fake id) can never match a current sweep.
	newEvent, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", ids[25])
	if err != nil {
		t.Fatalf("GetCurrentEvent after correction: %v", err)
	}
	if newEvent.ID == oldEvent.ID {
		t.Fatal("current event after correction is still the old row, want a new superseding row")
	}
	if newEvent.SweepValidated {
		t.Fatalf("newEvent.SweepValidated = true, want false: its reference swing (L1) was not created by a current sweep")
	}
	if len(newEvent.ReferenceSwingIDs) != 1 || newEvent.ReferenceSwingIDs[0] != l1.ID {
		t.Fatalf("newEvent.ReferenceSwingIDs = %v, want [%d] (L1, the only remaining confirmed low)", newEvent.ReferenceSwingIDs, l1.ID)
	}
	_ = oldSweep // kept only to document intent; retraction is proven by ErrSweepNotFound above
}
