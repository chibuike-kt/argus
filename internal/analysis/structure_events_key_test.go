//go:build integration

package analysis

import (
	"context"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

// TestIntegration_CorrectionToOwnTriggerCandle_RetractsOldEvent_ExactlyOneCurrentRemains
// is migration 000007's fixture: a correction to a candle that is
// itself a BOS or CHOCH's own trigger (not an upstream swing-window
// candle, as every earlier correction fixture used).
//
// Before 000007, GetCurrentEvent/the unique index keyed on
// trigger_candle_id, a row id. Correcting a candle mints a NEW row
// with a new id for the same open_time, so the replay's own lookup
// for this exact candle -- GetCurrentEvent(..., c.ID) -- would have
// looked up the NEW id, found nothing (the old event is keyed to the
// OLD id), and inserted a second row, leaving the OLD "current" event
// orphaned: two rows with superseded_at IS NULL for the same logical
// position, since (old_id) and (new_id) never collide on that index.
// Keying on trigger_open_time instead (stable across the correction)
// fixes this the same way confirmed_swings already keys on pivot_time.
func TestIntegration_CorrectionToOwnTriggerCandle_RetractsOldEvent_ExactlyOneCurrentRemains(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	mdStore := marketdata.NewStore(db, clock.Real{})

	h1, l1 := seedBullishEstablished(t, store, instrumentID)
	// seedBullishEstablished only writes the live structure_state
	// projection (SetState), not an actual event row. The initial
	// EvaluateRange below reads that live projection, which is enough
	// for it -- but HandleCandleCorrection's as-of reconstruction reads
	// the EVENT LOG (LatestEventBefore), never the live projection, so
	// it needs a real establishment event to find or it would wrongly
	// resume from Undetermined.
	establishTriggerID := feedCandles(t, db, instrumentID, []bar{{1, 105, 95}})[1]
	if _, err := store.InsertEvent(context.Background(), NewStructureEvent{
		InstrumentID: instrumentID, Timeframe: "1m", Type: EventDirectionEstablished,
		DirectionBefore: DirectionUndetermined, DirectionAfter: DirectionBullish,
		TriggerOpenTime: et(1), TriggerCandleID: establishTriggerID, ReferenceSwingIDs: []int64{h1, h1, l1, l1},
		EvaluationTime: clockEvalTime(1), AlgorithmVersion: testAlgorithmVersion,
	}); err != nil {
		t.Fatalf("seed establishment event: %v", err)
	}

	// Close strictly above the reference high (125): a BOS, referencing H1.
	feedCloseCandles(t, db, instrumentID, []closeBar{{20, 135, 128, 130}})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(20), et(21)); err != nil {
		t.Fatalf("EvaluateRange (initial): %v", err)
	}

	oldEvent, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", et(20))
	if err != nil {
		t.Fatalf("precondition failed: GetCurrentEvent: %v", err)
	}
	if oldEvent.Type != EventBOS || len(oldEvent.ReferenceSwingIDs) != 1 || oldEvent.ReferenceSwingIDs[0] != h1 {
		t.Fatalf("precondition failed: oldEvent = %+v, want BOS referencing H1 (%d)", oldEvent, h1)
	}

	// Correct minute 20 itself -- not an upstream swing-window candle --
	// so its close now breaks BELOW the reference low (90) instead:
	// a CHOCH, referencing L1. Materially different evidence (type,
	// direction, reference) from the old BOS, so this must replace, not
	// skip, under the fixed schema.
	_, err = mdStore.SupersedeCandle(context.Background(), instrumentID, clockEventTime(20), marketdata.Correction{
		Open: 85, High: 95, Low: 80, Close: 85, Volume: 1, Provider: "test", IngestedAt: clock.NewIngestionTime(time.Now()),
	})
	if err != nil {
		t.Fatalf("SupersedeCandle: %v", err)
	}
	if err := e.HandleCandleCorrection(context.Background(), instrumentID, "1m", et(20)); err != nil {
		t.Fatalf("HandleCandleCorrection: %v", err)
	}

	// Exactly one current event remains for this position, and it is
	// the new CHOCH -- not the stale BOS, and not both at once (the
	// bug's exact symptom: two rows, each "current" under a different
	// candle id).
	newEvent, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", et(20))
	if err != nil {
		t.Fatalf("GetCurrentEvent after correction: %v, want exactly one current event (a CHOCH) at this position", err)
	}
	if newEvent.ID == oldEvent.ID {
		t.Fatal("current event after correction is still the old row, want a new superseding row")
	}
	if newEvent.Type != EventCHOCH || newEvent.DirectionAfter != DirectionBearish {
		t.Fatalf("newEvent = %+v, want CHOCH -> Bearish", newEvent)
	}
	if len(newEvent.ReferenceSwingIDs) != 1 || newEvent.ReferenceSwingIDs[0] != l1 {
		t.Fatalf("newEvent.ReferenceSwingIDs = %v, want [%d] (L1, the broken low)", newEvent.ReferenceSwingIDs, l1)
	}

	// The old BOS is preserved exactly as it was, properly superseded
	// WITH a replacement pointer (not orphaned as still-current, and
	// not a pure retraction either -- a replacement happened).
	oldReloaded, err := getEventByID(context.Background(), db, oldEvent.ID)
	if err != nil {
		t.Fatalf("getEventByID (old event, must still exist): %v", err)
	}
	if oldReloaded.Type != EventBOS || oldReloaded.DirectionAfter != DirectionBullish {
		t.Fatalf("old event preserved evidence = %+v, want unchanged BOS -> Bullish", oldReloaded)
	}
	if oldReloaded.SupersededAt == nil || oldReloaded.SupersededBy == nil || *oldReloaded.SupersededBy != newEvent.ID {
		t.Fatalf("old event superseded state = %+v, want superseded_at set and superseded_by = %d (not orphaned as still-current, the bug's exact symptom)", oldReloaded, newEvent.ID)
	}
}
