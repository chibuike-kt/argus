//go:build integration

package analysis

import (
	"context"
	"database/sql"
	"testing"

	"github.com/chibuike-kt/argus/internal/clock"
)

// These tests exercise confirmed_swings_enforce_lifecycle and
// structure_events_enforce_lifecycle directly -- the two supersede-
// handshake triggers -- the same way the candle store's equivalent
// trigger was proved: by attempting the specific illegal updates and
// confirming the database itself rejects them, not just by reading the
// SQL.

func TestIntegration_ConfirmedSwingTrigger_RejectsEvidenceChangeOnCurrentRow(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)

	_, err := db.ExecContext(context.Background(),
		`INSERT INTO confirmed_swings (instrument_id, timeframe, swing_type, pivot_time, price, confirmed_at, evaluation_time, algorithm_version, source_candle_ids)
		 VALUES ($1, '1m', 'high', $2, 100, $3, $3, $4, ARRAY[1,2,3,4,5]::bigint[])`,
		instrumentID, et(2), et(5), testAlgorithmVersion)
	if err != nil {
		t.Fatalf("seed insert: %v", err)
	}

	// Changing price on a current (not yet superseded) row directly is
	// not one of the two legal handshake steps.
	_, err = db.ExecContext(context.Background(),
		`UPDATE confirmed_swings SET price = 999 WHERE instrument_id = $1 AND pivot_time = $2`, instrumentID, et(2))
	if err == nil {
		t.Fatal("UPDATE changing price on a current row succeeded, want the trigger to reject it")
	}
}

func TestIntegration_ConfirmedSwingTrigger_TerminalRowRejectsAnyUpdate(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)

	old := seedSwing(t, store, instrumentID, SwingHigh, 2, 100, []int64{1, 2, 3, 4, 5})
	replacement := NewSwing{
		InstrumentID: instrumentID, Timeframe: "1m", Type: SwingHigh,
		PivotTime: clockEventTime(2), Price: 110,
		ConfirmedAt: clockEventTime(5), EvaluationTime: old.EvaluationTime,
		AlgorithmVersion: testAlgorithmVersion, SourceCandleIDs: []int64{1, 2, 3, 4, 6},
	}
	newRow, err := store.SupersedeSwing(context.Background(), old.ID, nowUTC(), &replacement)
	if err != nil {
		t.Fatalf("SupersedeSwing: %v", err)
	}
	if newRow == nil {
		t.Fatal("SupersedeSwing returned nil replacement")
	}

	// old is now fully terminal (superseded_at AND superseded_by set):
	// the trigger must reject any further change whatsoever.
	_, err = db.ExecContext(context.Background(), `UPDATE confirmed_swings SET algorithm_version = 'tampered' WHERE id = $1`, old.ID)
	if err == nil {
		t.Fatal("UPDATE on a fully superseded confirmed_swings row succeeded, want the trigger to reject it unconditionally")
	}
}

func TestIntegration_ConfirmedSwingsCurrentIndex_ExactlyOnePerPosition(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)

	seedSwing(t, store, instrumentID, SwingHigh, 2, 100, []int64{1, 2, 3, 4, 5})

	// A second INSERT at the same (instrument, timeframe, swing_type,
	// pivot_time) while the first is still current must collide on
	// idx_confirmed_swings_current, the same guarantee candles rely on.
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO confirmed_swings (instrument_id, timeframe, swing_type, pivot_time, price, confirmed_at, evaluation_time, algorithm_version, source_candle_ids)
		 VALUES ($1, '1m', 'high', $2, 200, $3, $3, $4, ARRAY[9,10,11,12,13]::bigint[])`,
		instrumentID, et(2), et(5), testAlgorithmVersion)
	if err == nil {
		t.Fatal("a second current row for the same position was inserted, want a unique-index violation")
	}

	var count int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM confirmed_swings WHERE instrument_id = $1 AND timeframe = '1m' AND swing_type = 'high' AND pivot_time = $2 AND superseded_at IS NULL`,
		instrumentID, et(2)).Scan(&count); err != nil {
		t.Fatalf("count current rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("current rows for this position = %d, want exactly 1", count)
	}
}

func TestIntegration_StructureEventTrigger_RejectsEvidenceChangeOnCurrentRow(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)
	high := seedSwing(t, store, instrumentID, SwingHigh, 2, 100, []int64{1, 2, 3, 4, 5})

	candleID := seedBareCandle(t, db, instrumentID, 10)
	event, err := store.InsertEvent(context.Background(), NewStructureEvent{
		InstrumentID: instrumentID, Timeframe: "1m", Type: EventBOS,
		DirectionBefore: DirectionBullish, DirectionAfter: DirectionBullish,
		TriggerCandleID: candleID, ReferenceSwingIDs: []int64{high.ID},
		EvaluationTime: clockEvalTime(10), AlgorithmVersion: testAlgorithmVersion,
	})
	if err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}

	_, err = db.ExecContext(context.Background(), `UPDATE structure_events SET direction_after = 'bearish' WHERE id = $1`, event.ID)
	if err == nil {
		t.Fatal("UPDATE changing direction_after on a current event succeeded, want the trigger to reject it")
	}
}

func TestIntegration_StructureEventTrigger_ChochDirectionShapeTightened(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	candleID := seedBareCandle(t, db, instrumentID, 10)

	// Tightened per review: CHOCH must be exactly bullish<->bearish, not
	// merely "different from before" -- a flip to 'undetermined' must
	// be rejected by structure_events_direction_shape.
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO structure_events (instrument_id, timeframe, event_type, direction_before, direction_after, trigger_candle_id, reference_swing_ids, evaluation_time, algorithm_version)
		 VALUES ($1, '1m', 'choch', 'bullish', 'undetermined', $2, ARRAY[1]::bigint[], $3, $4)`,
		instrumentID, candleID, et(10), testAlgorithmVersion)
	if err == nil {
		t.Fatal("a CHOCH flipping to 'undetermined' was accepted, want the tightened CHECK to reject it")
	}
}

func TestIntegration_StructureEventsCurrentIndex_ExactlyOnePerTrigger(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)
	high := seedSwing(t, store, instrumentID, SwingHigh, 2, 100, []int64{1, 2, 3, 4, 5})
	candleID := seedBareCandle(t, db, instrumentID, 10)

	if _, err := store.InsertEvent(context.Background(), NewStructureEvent{
		InstrumentID: instrumentID, Timeframe: "1m", Type: EventBOS,
		DirectionBefore: DirectionBullish, DirectionAfter: DirectionBullish,
		TriggerCandleID: candleID, ReferenceSwingIDs: []int64{high.ID},
		EvaluationTime: clockEvalTime(10), AlgorithmVersion: testAlgorithmVersion,
	}); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}

	_, err := db.ExecContext(context.Background(),
		`INSERT INTO structure_events (instrument_id, timeframe, event_type, direction_before, direction_after, trigger_candle_id, reference_swing_ids, evaluation_time, algorithm_version)
		 VALUES ($1, '1m', 'bos', 'bullish', 'bullish', $2, ARRAY[1]::bigint[], $3, $4)`,
		instrumentID, candleID, et(10), testAlgorithmVersion)
	if err == nil {
		t.Fatal("a second current event for the same trigger candle was inserted, want a unique-index violation")
	}
}

// seedBareCandle inserts a minimal closed candle directly, for trigger
// tests that need a valid trigger_candle_id FK target but do not care
// about the candle's own values.
func seedBareCandle(t *testing.T, db *sql.DB, instrumentID int64, minute int) int64 {
	t.Helper()
	var id int64
	err := db.QueryRowContext(context.Background(),
		`INSERT INTO candles (instrument_id, open_time, close_time, open, high, low, close, volume, revision, is_closed, tick_aligned, provider, ingested_at)
		 VALUES ($1, $2, $2 + interval '1 minute', 100, 100, 100, 100, 0, 1, true, true, 'test', now())
		 RETURNING id`, instrumentID, et(minute)).Scan(&id)
	if err != nil {
		t.Fatalf("seedBareCandle: %v", err)
	}
	return id
}

func clockEvalTime(minute int) clock.EvaluationTime { return clock.NewEvaluationTime(et(minute)) }
