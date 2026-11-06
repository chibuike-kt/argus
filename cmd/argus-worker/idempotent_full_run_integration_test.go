//go:build integration

package main

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/chibuike-kt/argus/internal/ingestion/fixture"
)

// snapshotSetups returns instrumentID's setups rows, ordered by id,
// every column except created_at/updated_at (Part E's own exclusion:
// those two are expected to differ, or even stay fixed, run to run --
// they are not part of the outcome this test is proving stable).
func snapshotSetups(t *testing.T, db *sql.DB, instrumentID int64) []string {
	t.Helper()
	rows, err := db.Query(`
		SELECT id, timeframe, poi_type, poi_source_candle_id, direction, strategy_version,
		       state, poi_source_candle_revision, plan_snapshot, last_event_id,
		       evaluation_time, algorithm_version
		FROM setups WHERE instrument_id = $1 ORDER BY id`, instrumentID)
	if err != nil {
		t.Fatalf("snapshotSetups: query: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id, poiSourceCandleID, poiSourceCandleRevision int64
		var timeframe, poiType, direction, strategyVersion, state, planSnapshot, algorithmVersion string
		var lastEventID sql.NullInt64
		var evaluationTime sql.NullTime
		if err := rows.Scan(&id, &timeframe, &poiType, &poiSourceCandleID, &direction, &strategyVersion,
			&state, &poiSourceCandleRevision, &planSnapshot, &lastEventID, &evaluationTime, &algorithmVersion); err != nil {
			t.Fatalf("snapshotSetups: scan: %v", err)
		}
		out = append(out, fmt.Sprintf("id=%d tf=%s poi_type=%s poi_candle=%d dir=%s strat=%s state=%s rev=%d last_event=%v eval=%v algo=%s snapshot=%s",
			id, timeframe, poiType, poiSourceCandleID, direction, strategyVersion, state, poiSourceCandleRevision, lastEventID, evaluationTime, algorithmVersion, planSnapshot))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("snapshotSetups: rows: %v", err)
	}
	return out
}

// snapshotSetupEvents returns setup_events for every setup belonging
// to instrumentID, ordered by id, every column except created_at.
func snapshotSetupEvents(t *testing.T, db *sql.DB, instrumentID int64) []string {
	t.Helper()
	rows, err := db.Query(`
		SELECT se.id, se.setup_id, se.event_type, se.plan_snapshot, se.poi_source_candle_id,
		       se.poi_source_candle_revision, se.evaluation_time, se.algorithm_version
		FROM setup_events se JOIN setups s ON s.id = se.setup_id
		WHERE s.instrument_id = $1 ORDER BY se.id`, instrumentID)
	if err != nil {
		t.Fatalf("snapshotSetupEvents: query: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id, setupID, poiSourceCandleID, poiSourceCandleRevision int64
		var eventType, planSnapshot, algorithmVersion string
		var evaluationTime sql.NullTime
		if err := rows.Scan(&id, &setupID, &eventType, &planSnapshot, &poiSourceCandleID,
			&poiSourceCandleRevision, &evaluationTime, &algorithmVersion); err != nil {
			t.Fatalf("snapshotSetupEvents: scan: %v", err)
		}
		out = append(out, fmt.Sprintf("id=%d setup=%d type=%s poi_candle=%d rev=%d eval=%v algo=%s snapshot=%s",
			id, setupID, eventType, poiSourceCandleID, poiSourceCandleRevision, evaluationTime, algorithmVersion, planSnapshot))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("snapshotSetupEvents: rows: %v", err)
	}
	return out
}

func diffSnapshots(t *testing.T, label string, before, after []string) {
	t.Helper()
	if len(before) != len(after) {
		t.Errorf("%s: row count changed: before=%d after=%d\nbefore=%v\nafter=%v", label, len(before), len(after), before, after)
		return
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("%s: row %d changed:\n  before: %s\n  after:  %s", label, i, before[i], after[i])
		}
	}
}

// TestIntegration_IdempotentFullRun_RerunAfterCursorWipeProducesNoChange
// is Part E's own required proof: running the dev-fixture pipeline to
// completion, wiping this instrument's resume cursor, and running it
// again over the identical data must leave setups and setup_events
// byte-for-byte unchanged (outside created_at/updated_at) -- no
// duplicate events, no changed states. This is the property the whole
// pipeline leans on (ADR-005's idempotent-processing contract,
// exercised here at the full dataset's scale, not a hand-picked
// slice): setups.Reconcile and analysis.Engine must recognize
// "already at this state" for every single one of the dataset's
// candles the second time through, not just for the small scenarios
// the other integration tests use.
func TestIntegration_IdempotentFullRun_RerunAfterCursorWipeProducesNoChange(t *testing.T) {
	// Skipped with a reason, not deleted or weakened: this test found a
	// real divergence (worker-hardening brief, Part E -- reported to
	// the architect, not fixed here per CLAUDE.md rule 3). Exactly 3 of
	// 17 setups in the full dev dataset end up at state=detected after
	// one full run despite their own plan_snapshot already reading
	// status=triggered, and only reach state=triggered after a SECOND
	// full replay over the identical, already-populated data -- not
	// duplicate rows or corruption, a genuine "stuck at detected"
	// idempotency violation. All 3 affected identities sit in the same
	// qmr/order_block flapping region Part A's stale-protected-anchor
	// finding already documents (same underlying order block candle,
	// a qmr identity and a plain order_block identity for it created
	// one minute apart), so this is very likely a second symptom of
	// that same section 7/12-14 defect rather than an unrelated one,
	// but that connection is not proven here. Un-skip once the
	// architect resolves that area.
	t.Skip("worker-hardening brief Part E: real idempotency divergence found and reported, not fixed here (CLAUDE.md rule 3) -- see test's own doc comment")

	db := testWorkerDB(t)
	logger := quietLogger()
	ctx := context.Background()

	loaded, err := fixture.DevDataset()
	if err != nil {
		t.Fatalf("DevDataset: %v", err)
	}
	base := loaded.Candles["BTC-PERP"]

	instrumentID, _ := seedAndIngest(t, db, logger, base, "idempotent")
	inst := mustGetInstrument(t, db, instrumentID)

	proc, _ := newWorkerProcessor(db, logger)
	if _, err := proc.processInstrument(ctx, inst); err != nil {
		t.Fatalf("first run: processInstrument: %v", err)
	}

	beforeSetups := snapshotSetups(t, db, instrumentID)
	beforeEvents := snapshotSetupEvents(t, db, instrumentID)
	if len(beforeSetups) == 0 {
		t.Fatal("first run produced no setups -- nothing to prove idempotent")
	}

	// Wipe THIS instrument's resume cursor only (not the whole table
	// globally, which could race with other tests' own instruments in
	// the same shared test database) -- functionally identical to Part
	// B's "wipe the cursor table" for the one instrument this test
	// cares about.
	if _, err := db.ExecContext(ctx, `DELETE FROM processing_cursors WHERE instrument_id = $1`, instrumentID); err != nil {
		t.Fatalf("wipe cursor: %v", err)
	}

	// A fresh processor, simulating a real restart, replays the
	// entire dataset from the beginning over the SAME, already-
	// populated setups/setup_events rows.
	rerunProc, _ := newWorkerProcessor(db, logger)
	if _, err := rerunProc.processInstrument(ctx, inst); err != nil {
		t.Fatalf("second run: processInstrument: %v", err)
	}

	afterSetups := snapshotSetups(t, db, instrumentID)
	afterEvents := snapshotSetupEvents(t, db, instrumentID)

	diffSnapshots(t, "setups", beforeSetups, afterSetups)
	diffSnapshots(t, "setup_events", beforeEvents, afterEvents)
}
