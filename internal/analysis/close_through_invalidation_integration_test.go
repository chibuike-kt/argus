//go:build integration

package analysis

import (
	"context"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
)

// TestIntegration_CloseThroughInvalidation_WickOnlyDoesNotClear proves
// spec section 7, v0.12.0's own wording: "A wick through the level
// never invalidates, only a close." A candle whose high pierces the
// protected high but whose close stays at or below it must leave the
// anchor exactly as it was.
func TestIntegration_CloseThroughInvalidation_WickOnlyDoesNotClear(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	ctx := context.Background()

	high := seedSwing(t, store, instrumentID, SwingHigh, 0, 200, []int64{1})
	if err := store.SetState(ctx, StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
		ProtectedHighSwingID: &high.ID,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	// Wick to 210 (above the 200 anchor), closes at 195 (below it).
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{{10, 198, 210, 195, 195}})
	if err := e.EvaluateRange(ctx, instrumentID, "1m", et(10), et(11)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	state, err := store.GetState(ctx, instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.ProtectedHighSwingID == nil || *state.ProtectedHighSwingID != high.ID {
		t.Fatalf("ProtectedHighSwingID = %v, want unchanged at %d (a wick never invalidates)", state.ProtectedHighSwingID, high.ID)
	}
}

// TestIntegration_CloseThroughInvalidation_ClosesClearOnlyTheirOwnSide
// proves the close-through rule fires on an actual close beyond the
// level, and that each side is cleared independently -- clearing the
// high must never touch the low, and vice versa.
func TestIntegration_CloseThroughInvalidation_ClosesClearOnlyTheirOwnSide(t *testing.T) {
	t.Run("close above protected high clears only the high", func(t *testing.T) {
		db := testDB(t)
		instrumentID := testInstrument(t, db)
		e := newTestEngine(t, db)
		store := NewStore(db)
		ctx := context.Background()

		high := seedSwing(t, store, instrumentID, SwingHigh, 0, 200, []int64{1})
		low := seedSwing(t, store, instrumentID, SwingLow, 0, 100, []int64{2})
		if err := store.SetState(ctx, StateSnapshot{
			InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
			ProtectedHighSwingID: &high.ID, ProtectedLowSwingID: &low.ID,
		}, time.Now().UTC()); err != nil {
			t.Fatalf("SetState: %v", err)
		}

		feedCloseCandles(t, db, instrumentID, []closeBar{{10, 215, 205, 210}}) // closes at 210, strictly above 200
		if err := e.EvaluateRange(ctx, instrumentID, "1m", et(10), et(11)); err != nil {
			t.Fatalf("EvaluateRange: %v", err)
		}

		state, err := store.GetState(ctx, instrumentID, "1m")
		if err != nil {
			t.Fatalf("GetState: %v", err)
		}
		if state.ProtectedHighSwingID != nil {
			t.Fatalf("ProtectedHighSwingID = %v, want nil (close 210 > 200)", *state.ProtectedHighSwingID)
		}
		if state.ProtectedLowSwingID == nil || *state.ProtectedLowSwingID != low.ID {
			t.Fatalf("ProtectedLowSwingID = %v, want unchanged at %d (the low's own close-through never fired)", state.ProtectedLowSwingID, low.ID)
		}
	})

	t.Run("close below protected low clears only the low", func(t *testing.T) {
		db := testDB(t)
		instrumentID := testInstrument(t, db)
		e := newTestEngine(t, db)
		store := NewStore(db)
		ctx := context.Background()

		high := seedSwing(t, store, instrumentID, SwingHigh, 0, 200, []int64{1})
		low := seedSwing(t, store, instrumentID, SwingLow, 0, 100, []int64{2})
		if err := store.SetState(ctx, StateSnapshot{
			InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
			ProtectedHighSwingID: &high.ID, ProtectedLowSwingID: &low.ID,
		}, time.Now().UTC()); err != nil {
			t.Fatalf("SetState: %v", err)
		}

		feedCloseCandles(t, db, instrumentID, []closeBar{{10, 95, 85, 90}}) // closes at 90, strictly below 100
		if err := e.EvaluateRange(ctx, instrumentID, "1m", et(10), et(11)); err != nil {
			t.Fatalf("EvaluateRange: %v", err)
		}

		state, err := store.GetState(ctx, instrumentID, "1m")
		if err != nil {
			t.Fatalf("GetState: %v", err)
		}
		if state.ProtectedLowSwingID != nil {
			t.Fatalf("ProtectedLowSwingID = %v, want nil (close 90 < 100)", *state.ProtectedLowSwingID)
		}
		if state.ProtectedHighSwingID == nil || *state.ProtectedHighSwingID != high.ID {
			t.Fatalf("ProtectedHighSwingID = %v, want unchanged at %d (the high's own close-through never fired)", state.ProtectedHighSwingID, high.ID)
		}
	})
}

// TestIntegration_CloseThroughInvalidation_ExactEqualityDoesNotClear
// proves "strictly below"/"strictly above" is enforced literally: a
// close exactly at the anchor's own price leaves it in place.
func TestIntegration_CloseThroughInvalidation_ExactEqualityDoesNotClear(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	ctx := context.Background()

	low := seedSwing(t, store, instrumentID, SwingLow, 0, 100, []int64{1})
	if err := store.SetState(ctx, StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
		ProtectedLowSwingID: &low.ID,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	feedCloseCandles(t, db, instrumentID, []closeBar{{10, 105, 95, 100}}) // closes exactly at 100
	if err := e.EvaluateRange(ctx, instrumentID, "1m", et(10), et(11)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	state, err := store.GetState(ctx, instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.ProtectedLowSwingID == nil || *state.ProtectedLowSwingID != low.ID {
		t.Fatalf("ProtectedLowSwingID = %v, want unchanged at %d (close == anchor price is not \"strictly below\")", state.ProtectedLowSwingID, low.ID)
	}
}

// TestIntegration_CloseThroughInvalidation_CorrectionReplayEquivalence
// proves reconstructProtectedAnchorAsOf's new closedThroughAnchorBefore
// check rebuilds the same anchor state a clean forward walk already
// reached, not just the CHOCH-based half it checked before batch 8.
// The anchor is cleared by an ordinary close-through at minute 20; a
// later correction whose own affectedFrom (minute 28, RightBars=2
// behind the corrected candle at minute 30) lands strictly after that
// clearing candle must reconstruct ProtectedLowSwingID as nil at that
// resume point, not resurrect the stale anchor.
func TestIntegration_CloseThroughInvalidation_CorrectionReplayEquivalence(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	ctx := context.Background()

	// P (minute 0, price 100), with a real sweep at its own pivot_time
	// so LatestQualifyingProtectedAnchorBefore (the replay path's own
	// candidate lookup, store.go) can find it -- SetState alone, as the
	// other tests in this file use, is enough for the forward walk but
	// not for the store-driven reconstruction this test exercises.
	ids := feedCloseCandles(t, db, instrumentID, []closeBar{{0, 105, 95, 100}})
	p := seedSwing(t, store, instrumentID, SwingLow, 0, 100, []int64{ids[0]})
	if _, err := store.InsertSweep(ctx, NewSweep{
		InstrumentID: instrumentID, Timeframe: "1m", Side: SweepLow, SweptSwingID: p.ID,
		TriggerOpenTime: p.PivotTime.Time(), TriggerCandleID: ids[0],
		PenetrationExtreme: 95, ReclaimClose: 105,
		EvaluationTime: clock.NewEvaluationTime(time.Now().UTC()), AlgorithmVersion: testAlgorithmVersion,
	}); err != nil {
		t.Fatalf("InsertSweep: %v", err)
	}
	if err := store.SetState(ctx, StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
		ProtectedLowSwingID: &p.ID,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	// Minute 10: closes at 150, well above P -- no clear.
	feedCloseCandles(t, db, instrumentID, []closeBar{{10, 155, 145, 150}})
	if err := e.EvaluateRange(ctx, instrumentID, "1m", et(10), et(11)); err != nil {
		t.Fatalf("EvaluateRange (minute 10): %v", err)
	}
	// Minute 20: closes at 50, strictly below P's 100 -- clears it.
	feedCloseCandles(t, db, instrumentID, []closeBar{{20, 55, 45, 50}})
	if err := e.EvaluateRange(ctx, instrumentID, "1m", et(20), et(21)); err != nil {
		t.Fatalf("EvaluateRange (minute 20): %v", err)
	}
	// Minute 30: an otherwise ordinary candle, present so the
	// correction below has something to replay forward through.
	feedCloseCandles(t, db, instrumentID, []closeBar{{30, 65, 55, 60}})
	if err := e.EvaluateRange(ctx, instrumentID, "1m", et(30), et(31)); err != nil {
		t.Fatalf("EvaluateRange (minute 30): %v", err)
	}

	clean, err := store.GetState(ctx, instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState (clean run): %v", err)
	}
	if clean.ProtectedLowSwingID != nil {
		t.Fatalf("clean run: ProtectedLowSwingID = %v, want nil (close-through at minute 20 should have cleared it)", *clean.ProtectedLowSwingID)
	}

	// Trigger a correction-replay resume at minute 30 -- affectedFrom
	// (minute 30 - RightBars*Interval = minute 28) lands strictly after
	// minute 20's own close-through, so reconstructStateAsOf must
	// rediscover the clear from the store, not from in-memory state.
	if err := e.HandleCandleCorrection(ctx, instrumentID, "1m", et(30)); err != nil {
		t.Fatalf("HandleCandleCorrection: %v", err)
	}

	replayed, err := store.GetState(ctx, instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState (after correction replay): %v", err)
	}
	if replayed.ProtectedLowSwingID != nil {
		t.Fatalf("after correction replay: ProtectedLowSwingID = %v, want nil -- the replay resurrected a close-through-cleared anchor", *replayed.ProtectedLowSwingID)
	}
}
