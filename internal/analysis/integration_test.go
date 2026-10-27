//go:build integration

package analysis

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

// --- spec fixture: a valid confirmed swing high and low, and a
// candidate disqualified by an equal neighbor ---

func TestIntegration_ConfirmedSwingHighAndLow(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	ids := feedCandles(t, db, instrumentID, []bar{
		{0, 100, 90}, {1, 105, 85}, {2, 110, 80}, {3, 104, 86}, {4, 99, 91},
	})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(0), et(5)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	high, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingHigh, clockEventTime(2))
	if err != nil {
		t.Fatalf("GetCurrentSwing(high, minute 2): %v", err)
	}
	if high.Price != 110 {
		t.Errorf("confirmed high price = %d, want 110", high.Price)
	}
	wantIDs := []int64{ids[0], ids[1], ids[2], ids[3], ids[4]}
	assertSourceIDs(t, high.SourceCandleIDs, wantIDs)
	if !high.ConfirmedAt.Time().Equal(et(5)) {
		t.Errorf("high ConfirmedAt = %s, want %s (close_time of the 2nd right-hand bar)", high.ConfirmedAt, et(5))
	}
	if high.AlgorithmVersion != testAlgorithmVersion {
		t.Errorf("AlgorithmVersion = %q, want %q", high.AlgorithmVersion, testAlgorithmVersion)
	}

	low, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clockEventTime(2))
	if err != nil {
		t.Fatalf("GetCurrentSwing(low, minute 2): %v", err)
	}
	if low.Price != 80 {
		t.Errorf("confirmed low price = %d, want 80", low.Price)
	}
}

func TestIntegration_EqualNeighborDisqualifies(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	// minute0's high (110) equals minute2's high: disqualifies minute2
	// as a swing high (spec section 1: equality disqualifies).
	feedCandles(t, db, instrumentID, []bar{
		{0, 110, 90}, {1, 105, 85}, {2, 110, 80}, {3, 104, 86}, {4, 99, 91},
	})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(0), et(5)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	_, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingHigh, clockEventTime(2))
	if !errors.Is(err, ErrSwingNotFound) {
		t.Fatalf("GetCurrentSwing(high, minute 2) = %v, want ErrSwingNotFound: an equal neighbor must disqualify", err)
	}

	// The low is unaffected and must still confirm.
	low, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clockEventTime(2))
	if err != nil {
		t.Fatalf("GetCurrentSwing(low, minute 2): %v", err)
	}
	if low.Price != 80 {
		t.Errorf("confirmed low price = %d, want 80", low.Price)
	}
}

// --- spec fixture: pivot confirmation timing ---

func TestIntegration_PivotConfirmationTiming(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	// Only 4 bars: the pivot candidate at minute 2 has only ONE
	// confirmed right-hand neighbor (minute 3); minute 4 (its second
	// right-hand bar) has not closed yet.
	feedCandles(t, db, instrumentID, []bar{
		{0, 100, 90}, {1, 105, 85}, {2, 110, 80}, {3, 104, 86},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(0), et(4)); err != nil {
		t.Fatalf("EvaluateRange (partial): %v", err)
	}

	_, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingHigh, clockEventTime(2))
	if !errors.Is(err, ErrSwingNotFound) {
		t.Fatalf("GetCurrentSwing before the 2nd right-hand bar closes = %v, want ErrSwingNotFound (spec: a pivot is not known before its right-hand bars close)", err)
	}

	// Now the second right-hand bar closes.
	feedCandles(t, db, instrumentID, []bar{{4, 99, 91}})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(4), et(5)); err != nil {
		t.Fatalf("EvaluateRange (completing): %v", err)
	}

	high, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingHigh, clockEventTime(2))
	if err != nil {
		t.Fatalf("GetCurrentSwing after the 2nd right-hand bar closes: %v", err)
	}
	if high.Price != 110 {
		t.Errorf("confirmed high price = %d, want 110", high.Price)
	}
}

func assertSourceIDs(t *testing.T, got, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("source candle ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("source candle ids = %v, want %v", got, want)
		}
	}
}

// --- spec fixture: cold-start direction establishment, both
// directions. Hand-constructed, fully verified sequences (not seeded):
// this fixture is specifically about the organic path -- swings
// accumulating until the HH+HL / LH+LL condition first holds -- so it
// is not shortcut via seedSwing the way the break fixtures below are. ---

func bullishEstablishmentBars() []bar {
	return []bar{
		// Ramp down into L1 (pivot low, L=90, minute 2).
		{0, 110, 100}, {1, 105, 95}, {2, 100, 90}, {3, 104, 94}, {4, 108, 98},
		// Ramp up into H1 (pivot high, H=125, minute 7).
		{5, 115, 105}, {6, 120, 110}, {7, 125, 112}, {8, 119, 109}, {9, 114, 104},
		// Ramp down into L2 (pivot low, L=95 > L1's 90, minute 12).
		{10, 110, 100}, {11, 105, 97}, {12, 100, 95}, {13, 104, 98}, {14, 108, 102},
		// Ramp up into H2 (pivot high, H=130 > H1's 125, minute 17).
		{15, 115, 105}, {16, 122, 112}, {17, 130, 115}, {18, 124, 114}, {19, 118, 108},
	}
}

func TestIntegration_ColdStartEstablishment_Bullish(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	feedCandles(t, db, instrumentID, bullishEstablishmentBars())
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(0), et(20)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.Direction != DirectionBullish {
		t.Fatalf("Direction = %v, want Bullish", state.Direction)
	}

	// Establishment fires on the candle that confirms H2 (minute 17's
	// pivot, confirmed when minute 19 closes) -- the last of the four
	// swings needed, not an arbitrary later candle.
	event, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", mustCandleIDAt(t, db, instrumentID, 19))
	if err != nil {
		t.Fatalf("GetCurrentEvent at minute 19: %v", err)
	}
	if event.Type != EventDirectionEstablished {
		t.Fatalf("event type = %v, want EventDirectionEstablished", event.Type)
	}
	if event.DirectionBefore != DirectionUndetermined || event.DirectionAfter != DirectionBullish {
		t.Fatalf("direction before/after = %v/%v, want Undetermined/Bullish", event.DirectionBefore, event.DirectionAfter)
	}
	if len(event.ReferenceSwingIDs) != 4 {
		t.Fatalf("ReferenceSwingIDs = %v, want exactly 4 (prior_high, latest_high, prior_low, latest_low)", event.ReferenceSwingIDs)
	}
}

func bearishEstablishmentBars() []bar {
	// The exact mirror of bullishEstablishmentBars under high'=220-low,
	// low'=220-high: every strict inequality verified for the bullish
	// sequence reverses, giving a lower-high + lower-low sequence with
	// the same isolation guarantees (no accidental extra pivots).
	return []bar{
		{0, 120, 110}, {1, 125, 115}, {2, 130, 120}, {3, 126, 116}, {4, 122, 112},
		{5, 115, 105}, {6, 110, 100}, {7, 108, 95}, {8, 111, 101}, {9, 116, 106},
		{10, 120, 110}, {11, 123, 115}, {12, 125, 120}, {13, 122, 116}, {14, 118, 112},
		{15, 115, 105}, {16, 108, 98}, {17, 105, 90}, {18, 106, 96}, {19, 112, 102},
	}
}

func TestIntegration_ColdStartEstablishment_Bearish(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	feedCandles(t, db, instrumentID, bearishEstablishmentBars())
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(0), et(20)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.Direction != DirectionBearish {
		t.Fatalf("Direction = %v, want Bearish", state.Direction)
	}

	event, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", mustCandleIDAt(t, db, instrumentID, 19))
	if err != nil {
		t.Fatalf("GetCurrentEvent at minute 19: %v", err)
	}
	if event.Type != EventDirectionEstablished || event.DirectionAfter != DirectionBearish {
		t.Fatalf("event = %+v, want DirectionEstablished -> Bearish", event)
	}
}

func mustCandleIDAt(t *testing.T, db *sql.DB, instrumentID int64, minute int) int64 {
	t.Helper()
	c, err := marketdata.NewStore(db, clock.Real{}).GetCurrent(context.Background(), instrumentID, clockEventTime(minute))
	if err != nil {
		t.Fatalf("GetCurrent minute=%d: %v", minute, err)
	}
	return c.ID
}

// --- spec fixtures: BOS continuation, CHOCH flip, and a lower high
// that changes nothing. These seed an established direction and its
// reference swings directly (seedSwing + SetState) rather than
// reproducing a full cold-start sequence: what these fixtures test is
// the break decision given an established direction, which the
// cold-start fixtures above already prove the organic path reaches. ---

func seedBullishEstablished(t *testing.T, store *Store, instrumentID int64) (highID, lowID int64) {
	t.Helper()
	high := seedSwing(t, store, instrumentID, SwingHigh, 2, 125, []int64{1, 2, 3, 4, 5})
	low := seedSwing(t, store, instrumentID, SwingLow, 7, 90, []int64{6, 7, 8, 9, 10})
	if err := store.SetState(context.Background(), StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
		ReferenceSwingHighID: &high.ID, ReferenceSwingLowID: &low.ID,
	}, nowUTC()); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	return high.ID, low.ID
}

func TestIntegration_BOS_ContinuesDirection(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	highID, _ := seedBullishEstablished(t, store, instrumentID)

	// Close strictly above the reference high (125): a BOS.
	feedCloseCandles(t, db, instrumentID, []closeBar{{20, 135, 128, 130}})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(20), et(21)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	triggerID := mustCandleIDAt(t, db, instrumentID, 20)
	event, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", triggerID)
	if err != nil {
		t.Fatalf("GetCurrentEvent: %v", err)
	}
	if event.Type != EventBOS || event.DirectionBefore != DirectionBullish || event.DirectionAfter != DirectionBullish {
		t.Fatalf("event = %+v, want BOS, Bullish -> Bullish", event)
	}
	if len(event.ReferenceSwingIDs) != 1 || event.ReferenceSwingIDs[0] != highID {
		t.Fatalf("ReferenceSwingIDs = %v, want [%d] (the broken high, per spec section 3: BOS references the swing in the trend's own direction)", event.ReferenceSwingIDs, highID)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.Direction != DirectionBullish {
		t.Fatalf("Direction after BOS = %v, want still Bullish (BOS continues, never flips)", state.Direction)
	}
}

func TestIntegration_CHOCH_FlipsDirection(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	_, lowID := seedBullishEstablished(t, store, instrumentID)

	// Close strictly below the reference low (90): a CHOCH, flips bearish.
	feedCloseCandles(t, db, instrumentID, []closeBar{{20, 92, 80, 85}})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(20), et(21)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	triggerID := mustCandleIDAt(t, db, instrumentID, 20)
	event, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", triggerID)
	if err != nil {
		t.Fatalf("GetCurrentEvent: %v", err)
	}
	if event.Type != EventCHOCH || event.DirectionBefore != DirectionBullish || event.DirectionAfter != DirectionBearish {
		t.Fatalf("event = %+v, want CHOCH, Bullish -> Bearish", event)
	}
	if len(event.ReferenceSwingIDs) != 1 || event.ReferenceSwingIDs[0] != lowID {
		t.Fatalf("ReferenceSwingIDs = %v, want [%d] (the broken low, per spec section 3: CHOCH references the swing against the trend)", event.ReferenceSwingIDs, lowID)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.Direction != DirectionBearish {
		t.Fatalf("Direction after CHOCH = %v, want Bearish", state.Direction)
	}
}

func TestIntegration_LowerHighChangesNothing(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	seedBullishEstablished(t, store, instrumentID)

	// A new, confirmable swing high that is LOWER than the current
	// reference (125): confirmed as evidence, but not itself a BOS or
	// CHOCH, and direction must not change or revert to Undetermined.
	feedCandles(t, db, instrumentID, []bar{
		{20, 115, 105}, {21, 118, 108}, {22, 120, 110}, {23, 117, 107}, {24, 112, 102},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(20), et(25)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	newHigh, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingHigh, clockEventTime(22))
	if err != nil {
		t.Fatalf("GetCurrentSwing(high, minute 22): %v", err)
	}
	if newHigh.Price != 120 {
		t.Fatalf("new high price = %d, want 120 (confirmed as evidence even though lower than the reference)", newHigh.Price)
	}

	// No structure event at the confirming candle (minute 24): a
	// confirmed-but-lower swing is not itself a break.
	triggerID := mustCandleIDAt(t, db, instrumentID, 24)
	_, err = store.GetCurrentEvent(context.Background(), instrumentID, "1m", triggerID)
	if !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("GetCurrentEvent at the confirming candle = %v, want ErrEventNotFound: a lower high must not itself produce an event", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.Direction != DirectionBullish {
		t.Fatalf("Direction = %v, want still Bullish: a lower high must not change direction or revert it to Undetermined (spec section 2)", state.Direction)
	}
}

func nowUTC() time.Time { return time.Now().UTC() }

// --- deliberate fixture (approved review point): step 2 must use the
// reference pointer step 1 just updated on the SAME candle, so a close
// can break a swing confirmed by that same candle two bars back. This
// is constructed so a stale-pointer bug would visibly fail it: the
// triggering candle's close is positioned to produce a DIFFERENT
// verdict depending on whether the OLD or the NEW reference high is
// used, and only the new-reference verdict (no event) is correct. ---

func TestIntegration_SameCandleConfirmsPivotAndUsesItForBreak(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)

	oldHigh := seedSwing(t, store, instrumentID, SwingHigh, 2, 100, []int64{1, 2, 3, 4, 5})
	low := seedSwing(t, store, instrumentID, SwingLow, 7, 50, []int64{6, 7, 8, 9, 10})
	if err := store.SetState(context.Background(), StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
		ReferenceSwingHighID: &oldHigh.ID, ReferenceSwingLowID: &low.ID,
	}, nowUTC()); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	// minute 22 is a new, higher swing high (150) confirmed exactly when
	// minute 24 closes. minute 24's own close (120) is strictly ABOVE
	// the OLD reference (100) but strictly BELOW the NEW one (150): a
	// stale-pointer bug would wrongly report a BOS here; the correct
	// behavior is no event at all.
	feedCandles(t, db, instrumentID, []bar{
		{20, 145, 135}, {21, 148, 138}, {22, 150, 140}, {23, 147, 137},
	})
	feedCloseCandles(t, db, instrumentID, []closeBar{{24, 140, 115, 120}})

	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(20), et(25)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	newHigh, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingHigh, clockEventTime(22))
	if err != nil {
		t.Fatalf("GetCurrentSwing(high, minute 22): %v", err)
	}
	if newHigh.Price != 150 {
		t.Fatalf("new high price = %d, want 150", newHigh.Price)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.ReferenceSwingHighID == nil || *state.ReferenceSwingHighID != newHigh.ID {
		t.Fatalf("ReferenceSwingHighID = %v, want %d (the swing just confirmed on this same candle)", state.ReferenceSwingHighID, newHigh.ID)
	}

	triggerID := mustCandleIDAt(t, db, instrumentID, 24)
	_, err = store.GetCurrentEvent(context.Background(), instrumentID, "1m", triggerID)
	if !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("GetCurrentEvent at minute 24 = %v, want ErrEventNotFound: close=120 must be checked against the NEW reference (150), not the stale one (100) -- a stale-pointer bug would wrongly produce a BOS here", err)
	}
}

// --- spec fixture: a correction to a source candle recomputes a
// structural event, with the prior event preserved (ADR-011, ADR-019).
// This is the as-of-reconstruction path specifically: it builds a full
// organic bullish establishment plus a subsequent BOS, then corrects the
// earliest pivot candle (H1, minute 7) in a way that invalidates the
// higher-high condition establishment depended on, and checks that:
//   - the swing at minute 7 is superseded by a new revision (a
//     different price), never rewritten in place;
//   - the old establishment event is superseded with NO replacement
//     (retracted) and its original evidence is preserved unchanged;
//   - the old BOS event is likewise retracted and preserved;
//   - the live projection ends up back at Undetermined -- the state the
//     as-of reconstruction at the correction's affected-from point
//     (minute 5, two bars before minute 7) must have resumed from, not
//     whatever the live, pre-correction projection said.
func TestIntegration_CorrectionRecompute_RetractsDownstreamEvents_PreservesPriorEvidence(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	mdStore := marketdata.NewStore(db, clock.Real{})

	feedCandles(t, db, instrumentID, bullishEstablishmentBars())
	// minute 20: a BOS, close strictly above H2 (130).
	feedCloseCandles(t, db, instrumentID, []closeBar{{20, 140, 132, 135}})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(0), et(21)); err != nil {
		t.Fatalf("EvaluateRange (initial): %v", err)
	}

	// Sanity: establishment and BOS both exist and direction is
	// Bullish, before touching anything -- otherwise the rest of this
	// test would not be exercising what it claims to.
	stateBefore, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState (before): %v", err)
	}
	if stateBefore.Direction != DirectionBullish {
		t.Fatalf("precondition failed: Direction = %v, want Bullish before the correction", stateBefore.Direction)
	}
	establishTriggerID := mustCandleIDAt(t, db, instrumentID, 19)
	oldEstablish, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", establishTriggerID)
	if err != nil {
		t.Fatalf("precondition failed: GetCurrentEvent (establishment): %v", err)
	}
	bosTriggerID := mustCandleIDAt(t, db, instrumentID, 20)
	oldBOS, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", bosTriggerID)
	if err != nil {
		t.Fatalf("precondition failed: GetCurrentEvent (BOS): %v", err)
	}
	oldH1, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingHigh, clockEventTime(7))
	if err != nil {
		t.Fatalf("precondition failed: GetCurrentSwing (H1): %v", err)
	}
	if oldH1.Price != 125 {
		t.Fatalf("precondition failed: H1 price = %d, want 125", oldH1.Price)
	}

	// Correct H1's candle (minute 7): raise its high from 125 to 135,
	// which makes it HIGHER than H2 (130) -- the established
	// sequence's "higher high" condition (H2 > H1) no longer holds
	// under the corrected data.
	_, err = mdStore.SupersedeCandle(context.Background(), instrumentID, clockEventTime(7), marketdata.Correction{
		Open: 118, High: 135, Low: 112, Close: 118, Volume: 1, Provider: "test", IngestedAt: clock.NewIngestionTime(time.Now()),
	})
	if err != nil {
		t.Fatalf("SupersedeCandle (correct H1): %v", err)
	}

	if err := e.HandleCandleCorrection(context.Background(), instrumentID, "1m", et(7)); err != nil {
		t.Fatalf("HandleCandleCorrection: %v", err)
	}

	// --- the swing itself: superseded by a new revision, not rewritten ---
	newH1, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingHigh, clockEventTime(7))
	if err != nil {
		t.Fatalf("GetCurrentSwing (H1 after correction): %v", err)
	}
	if newH1.ID == oldH1.ID {
		t.Fatal("H1 row id did not change: the correction must append a new revision, never rewrite the old row")
	}
	if newH1.Price != 135 {
		t.Fatalf("new H1 price = %d, want 135", newH1.Price)
	}
	oldH1Reloaded, err := store.GetSwingByID(context.Background(), oldH1.ID)
	if err != nil {
		t.Fatalf("GetSwingByID (old H1, must still exist): %v", err)
	}
	if oldH1Reloaded.Price != 125 {
		t.Fatalf("old H1 preserved price = %d, want unchanged 125", oldH1Reloaded.Price)
	}
	if oldH1Reloaded.SupersededAt == nil || oldH1Reloaded.SupersededBy == nil || *oldH1Reloaded.SupersededBy != newH1.ID {
		t.Fatalf("old H1 superseded state = %+v, want superseded_at set and superseded_by = %d", oldH1Reloaded, newH1.ID)
	}

	// --- the establishment event: retracted (no replacement), original
	// evidence preserved exactly ---
	oldEstablishReloaded, err := getEventByID(context.Background(), db, oldEstablish.ID)
	if err != nil {
		t.Fatalf("getEventByID (old establishment, must still exist): %v", err)
	}
	if oldEstablishReloaded.DirectionAfter != DirectionBullish {
		t.Fatalf("old establishment preserved DirectionAfter = %v, want unchanged Bullish", oldEstablishReloaded.DirectionAfter)
	}
	if oldEstablishReloaded.SupersededAt == nil {
		t.Fatal("old establishment event is not marked superseded")
	}
	if oldEstablishReloaded.SupersededBy != nil {
		t.Fatalf("old establishment event has a replacement (%d), want a pure retraction (no replacement): the corrected sequence no longer establishes a direction at all", *oldEstablishReloaded.SupersededBy)
	}
	if _, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", establishTriggerID); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("a current establishment event still exists at minute 19 = %v, want ErrEventNotFound (retracted)", err)
	}

	// --- the BOS event: likewise retracted, likewise preserved ---
	oldBOSReloaded, err := getEventByID(context.Background(), db, oldBOS.ID)
	if err != nil {
		t.Fatalf("getEventByID (old BOS, must still exist): %v", err)
	}
	if oldBOSReloaded.DirectionBefore != DirectionBullish || oldBOSReloaded.DirectionAfter != DirectionBullish {
		t.Fatalf("old BOS preserved direction = %v -> %v, want unchanged Bullish -> Bullish", oldBOSReloaded.DirectionBefore, oldBOSReloaded.DirectionAfter)
	}
	if oldBOSReloaded.SupersededAt == nil || oldBOSReloaded.SupersededBy != nil {
		t.Fatalf("old BOS superseded state = %+v, want retracted (superseded_at set, no replacement)", oldBOSReloaded)
	}
	if _, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", bosTriggerID); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("a current BOS event still exists at minute 20 = %v, want ErrEventNotFound (retracted): a BOS cannot stand once the direction it continued was never actually established", err)
	}

	// --- the live projection: back to Undetermined, proving the as-of
	// reconstruction (not the stale live state) is what the replay
	// resumed from ---
	stateAfter, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState (after): %v", err)
	}
	if stateAfter.Direction != DirectionUndetermined {
		t.Fatalf("Direction after correction = %v, want Undetermined: the corrected H1/H2 relationship no longer satisfies establishment", stateAfter.Direction)
	}
}

func getEventByID(ctx context.Context, db *sql.DB, id int64) (StructureEvent, error) {
	// Store has no public GetEventByID (nothing in the engine's normal
	// path needs one -- only this test, to inspect a superseded row
	// directly), so this reads the table directly for that one purpose.
	var row struct {
		ID, InstrumentID, TriggerCandleID                                       int64
		Timeframe, EventType, DirectionBefore, DirectionAfter, AlgorithmVersion string
		SupersededAt                                                            sql.NullTime
		SupersededBy                                                            sql.NullInt64
		SweepValidated                                                          bool
		SweepEventID                                                            sql.NullInt64
		ProtectedSwingID                                                        sql.NullInt64
	}
	err := db.QueryRowContext(ctx, `SELECT id, instrument_id, timeframe, event_type, direction_before, direction_after, trigger_candle_id, algorithm_version, superseded_at, superseded_by, sweep_validated, sweep_event_id, protected_swing_id FROM structure_events WHERE id = $1`, id).
		Scan(&row.ID, &row.InstrumentID, &row.Timeframe, &row.EventType, &row.DirectionBefore, &row.DirectionAfter, &row.TriggerCandleID, &row.AlgorithmVersion, &row.SupersededAt, &row.SupersededBy, &row.SweepValidated, &row.SweepEventID, &row.ProtectedSwingID)
	if err != nil {
		return StructureEvent{}, err
	}
	before, err := parseDirection(row.DirectionBefore)
	if err != nil {
		return StructureEvent{}, err
	}
	after, err := parseDirection(row.DirectionAfter)
	if err != nil {
		return StructureEvent{}, err
	}
	eventType, err := parseEventType(row.EventType)
	if err != nil {
		return StructureEvent{}, err
	}
	ev := StructureEvent{
		ID: row.ID, InstrumentID: row.InstrumentID, Timeframe: row.Timeframe,
		Type: eventType, DirectionBefore: before, DirectionAfter: after,
		TriggerCandleID: row.TriggerCandleID, AlgorithmVersion: row.AlgorithmVersion,
		SweepValidated: row.SweepValidated,
	}
	if row.SupersededAt.Valid {
		pt := clock.NewProcessingTime(row.SupersededAt.Time)
		ev.SupersededAt = &pt
	}
	if row.SupersededBy.Valid {
		supID := row.SupersededBy.Int64
		ev.SupersededBy = &supID
	}
	if row.SweepEventID.Valid {
		sid := row.SweepEventID.Int64
		ev.SweepEventID = &sid
	}
	if row.ProtectedSwingID.Valid {
		pid := row.ProtectedSwingID.Int64
		ev.ProtectedSwingID = &pid
	}
	return ev, nil
}
