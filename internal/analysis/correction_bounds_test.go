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

// These two fixtures target the exact bug class reconcileSwing's
// retraction fallback already guards against, in the two places that
// missed it: during a correction replay, a "latest current swing (or
// swing-pair)" query can return a row the walk has not reconciled yet
// in THIS pass -- a stale survivor from before the correction that is
// only still "current" because the replay has not reached its position
// to supersede or retract it. Both are built so the wrong behavior
// produces a visibly wrong verdict (a BOS that should fire does not,
// or a direction establishes a step earlier than it legitimately can),
// not merely a missing assertion.

// --- bug 1: reconstructStateAsOf must bound by confirmed_at, not
// pivot_time. A swing with pivot_time just before the resume point but
// confirmed_at at or after it was not actually known as a reference at
// that instant. ---

func TestIntegration_CorrectionAsOf_ExcludesSwingConfirmedAfterResumePoint(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	mdStore := marketdata.NewStore(db, clock.Real{})

	// The real reference: confirmed well before the resume point,
	// price 100.
	s0High := seedSwing(t, store, instrumentID, SwingHigh, 2, 100, []int64{1, 2, 3, 4, 5})
	s0Low := seedSwing(t, store, instrumentID, SwingLow, 3, 10, []int64{2, 3, 4, 5, 6})

	// The trap: pivot at minute 10, so it occurs (pivot_time=10) before
	// affectedFrom=11, but it does not CONFIRM until minute 13
	// (confirmed_at = 10 + (RightBars+1) = 13) -- strictly after the
	// resume point. A buggy pivot_time-bound reconstruction would wrongly
	// seed this as the reference; the correct confirmed_at bound must
	// exclude it.
	trap := seedSwing(t, store, instrumentID, SwingHigh, 10, 150, []int64{7, 8, 9, 10, 11})

	establishTriggerID := feedCandles(t, db, instrumentID, []bar{{1, 60, 55}})[1]
	if _, err := store.InsertEvent(context.Background(), NewStructureEvent{
		InstrumentID: instrumentID, Timeframe: "1m", Type: EventDirectionEstablished,
		DirectionBefore: DirectionUndetermined, DirectionAfter: DirectionBullish,
		TriggerCandleID: establishTriggerID, ReferenceSwingIDs: []int64{s0High.ID, s0High.ID, s0Low.ID, s0Low.ID},
		EvaluationTime: clockEvalTime(1), AlgorithmVersion: testAlgorithmVersion,
	}); err != nil {
		t.Fatalf("seed establishment event: %v", err)
	}

	// Boring filler everywhere except minute 11, the candle under test:
	// its close (120) is strictly above the REAL reference (100) but
	// strictly below the TRAP (150) -- a stale-trap bug would wrongly
	// see no break; the fix must see a BOS.
	feedCandles(t, db, instrumentID, []bar{
		{7, 60, 55}, {8, 60, 55}, {9, 60, 55}, {10, 60, 55},
	})
	feedCloseCandles(t, db, instrumentID, []closeBar{{11, 140, 100, 120}})
	feedCandles(t, db, instrumentID, []bar{{12, 60, 55}})
	feedCloseCandles(t, db, instrumentID, []closeBar{{13, 60, 55, 58}})

	// Correct minute 13 (trivially, same values) purely to drive
	// affectedFrom = 13 - RightBars = 11, landing strictly between
	// trap's pivot_time (10) and its confirmed_at (13).
	_, err := mdStore.SupersedeCandle(context.Background(), instrumentID, clockEventTime(13), marketdata.Correction{
		Open: 58, High: 60, Low: 55, Close: 58, Volume: 1, Provider: "test", IngestedAt: clock.NewIngestionTime(time.Now()),
	})
	if err != nil {
		t.Fatalf("SupersedeCandle (trivial correction at minute 13): %v", err)
	}
	if err := e.HandleCandleCorrection(context.Background(), instrumentID, "1m", et(13)); err != nil {
		t.Fatalf("HandleCandleCorrection: %v", err)
	}
	_ = trap // kept only to document intent; its row is not otherwise referenced

	triggerID := mustCandleIDAt(t, db, instrumentID, 11)
	event, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", triggerID)
	if err != nil {
		t.Fatalf("GetCurrentEvent at minute 11: %v (want a BOS: close=120 breaks the real reference 100; a stale-trap bug using 150 would see nothing)", err)
	}
	if event.Type != EventBOS || event.DirectionAfter != DirectionBullish {
		t.Fatalf("event at minute 11 = %+v, want BOS, stays Bullish", event)
	}
}

// --- bug 2: decideEstablishmentCandidate must bound its swing queries
// by what is confirmable by the candle being processed, not read the
// "latest current" rows unbounded. During a correction replay that
// lands back in Undetermined, an unbounded query can see a later
// swing pair the walk has not reconciled yet THIS pass and establish a
// direction a step earlier than is legitimate. ---

func TestIntegration_CorrectionReplay_EstablishmentWaitsForLaterSwingToBeReconciled(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	mdStore := marketdata.NewStore(db, clock.Real{})

	// Two early, real, unaffected swings.
	hA := seedSwing(t, store, instrumentID, SwingHigh, 2, 100, []int64{1, 2, 3, 4, 5})
	lA := seedSwing(t, store, instrumentID, SwingLow, 3, 50, []int64{2, 3, 4, 5, 6})

	// Candles 18-23 produce a real, organically-detectable high at
	// minute 20 (price 200) and a real low at minute 21 (price 80) --
	// not seeded, because the replay WILL re-derive these positions and
	// they must match what is actually in the candle store.
	feedCandles(t, db, instrumentID, []bar{
		{18, 170, 140}, {19, 185, 145}, {20, 200, 150}, {21, 160, 80}, {22, 165, 90},
	})
	feedCloseCandles(t, db, instrumentID, []closeBar{{23, 150, 85, 85}})
	// Boring filler through the correction target and beyond.
	feedCandles(t, db, instrumentID, []bar{{24, 120, 110}, {25, 120, 110}, {26, 120, 110}})

	// Seed hB/lB matching what candles 18-23 actually produce, so the
	// pre-correction "before" state has them as already-confirmed
	// current swings -- exactly the stale-survivor scenario: they exist
	// in the table before the replay starts, and the replay will not
	// reconcile their positions (20 and 21) until it actually reaches
	// candles 22 and 23 respectively.
	hB := seedSwing(t, store, instrumentID, SwingHigh, 20, 200, []int64{1, 1, 1, 1, 1})
	lB := seedSwing(t, store, instrumentID, SwingLow, 21, 80, []int64{1, 1, 1, 1, 1})

	establishTriggerID := mustCandleIDAt(t, db, instrumentID, 24)
	if _, err := store.InsertEvent(context.Background(), NewStructureEvent{
		InstrumentID: instrumentID, Timeframe: "1m", Type: EventDirectionEstablished,
		DirectionBefore: DirectionUndetermined, DirectionAfter: DirectionBullish,
		TriggerCandleID: establishTriggerID, ReferenceSwingIDs: []int64{hA.ID, hB.ID, lA.ID, lB.ID},
		EvaluationTime: clockEvalTime(24), AlgorithmVersion: testAlgorithmVersion,
	}); err != nil {
		t.Fatalf("seed establishment event: %v", err)
	}

	// Correct minute 24 (trivially) to drive affectedFrom = 22,
	// strictly before lB's own reconciling candle (23). hB's position
	// (20) IS reconciled by the replay's first candle (22)'s own step
	// 1, so it is legitimately visible immediately; lB's position (21)
	// is not reconciled until candle 23.
	_, err := mdStore.SupersedeCandle(context.Background(), instrumentID, clockEventTime(24), marketdata.Correction{
		Open: 115, High: 120, Low: 110, Close: 115, Volume: 1, Provider: "test", IngestedAt: clock.NewIngestionTime(time.Now()),
	})
	if err != nil {
		t.Fatalf("SupersedeCandle (trivial correction at minute 24): %v", err)
	}
	if err := e.HandleCandleCorrection(context.Background(), instrumentID, "1m", et(24)); err != nil {
		t.Fatalf("HandleCandleCorrection: %v", err)
	}

	// The establishment must NOT fire at minute 22: at that point lB
	// (pivot 21) has not been reconciled by this replay pass yet. An
	// unbounded ListLatestSwings would wrongly see it anyway and
	// establish Bullish here, a full step early.
	trigger22 := mustCandleIDAt(t, db, instrumentID, 22)
	if _, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", trigger22); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("GetCurrentEvent at minute 22 = %v, want ErrEventNotFound: establishment must wait until lB (pivot 21) is actually reconciled by this replay, not read as a stale survivor", err)
	}

	// It must fire at minute 23, the candle whose own step 1 legitimately
	// reconciles lB.
	trigger23 := mustCandleIDAt(t, db, instrumentID, 23)
	event, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", trigger23)
	if err != nil {
		t.Fatalf("GetCurrentEvent at minute 23: %v, want a direction-establishment event here", err)
	}
	if event.Type != EventDirectionEstablished || event.DirectionAfter != DirectionBullish {
		t.Fatalf("event at minute 23 = %+v, want DirectionEstablished -> Bullish", event)
	}
}
