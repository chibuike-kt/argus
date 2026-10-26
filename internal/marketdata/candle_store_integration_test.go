//go:build integration

package marketdata

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
)

// These tests exercise the real schema from db/migrations -- the
// supersession handshake and trg_candles_enforce_lifecycle specifically --
// because a handwritten fake cannot verify a database trigger or a
// partial unique index (guide 10: "use integration tests for database
// constraints and transaction semantics that fakes cannot faithfully
// verify"). Run with: go test -tags=integration -race ./internal/marketdata/...
// against a Postgres with all migrations applied (make migrate-up).

func TestIntegration_UpsertForming_OverwritesInPlace(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db, clock.Real{})
	openTime := et(2026, time.October, 26, 18, 0)

	first, err := store.UpsertForming(context.Background(), Tick{
		InstrumentID: instrumentID, OpenTime: openTime,
		Open: 100, High: 105, Low: 95, Close: 100, Volume: 10,
		Provider: "test", IngestedAt: it(time.Now()),
	})
	if err != nil {
		t.Fatalf("first UpsertForming: %v", err)
	}
	if first.Revision != 1 || first.IsClosed {
		t.Fatalf("first candle = %+v, want revision 1, not closed", first)
	}

	// A later tick for the same minute overwrites in place -- same row id,
	// still revision 1, new values.
	second, err := store.UpsertForming(context.Background(), Tick{
		InstrumentID: instrumentID, OpenTime: openTime,
		Open: 100, High: 120, Low: 95, Close: 115, Volume: 25,
		Provider: "test", IngestedAt: it(time.Now()),
	})
	if err != nil {
		t.Fatalf("second UpsertForming: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second candle id = %d, want same id as first (%d) -- overwrite, not a new row", second.ID, first.ID)
	}
	if second.Revision != 1 {
		t.Fatalf("second candle revision = %d, want 1 -- a tick must never create a revision", second.Revision)
	}
	if second.High != 120 || second.Close != 115 || second.Volume != 25 {
		t.Fatalf("second candle = %+v, want the overwritten values", second)
	}
}

func TestIntegration_UpsertForming_OnAlreadyClosedCandle_Rejected(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db, clock.Real{})
	openTime := et(2026, time.October, 26, 18, 0)

	if _, err := store.UpsertForming(context.Background(), Tick{
		InstrumentID: instrumentID, OpenTime: openTime,
		Open: 100, High: 105, Low: 95, Close: 100, Volume: 10,
		Provider: "test", IngestedAt: it(time.Now()),
	}); err != nil {
		t.Fatalf("UpsertForming: %v", err)
	}
	if _, err := store.Close(context.Background(), instrumentID, openTime); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A tick arriving for an already-closed minute is a caller bug;
	// trg_candles_enforce_lifecycle must reject it, not silently reopen
	// or corrupt the closed candle.
	_, err := store.UpsertForming(context.Background(), Tick{
		InstrumentID: instrumentID, OpenTime: openTime,
		Open: 100, High: 999, Low: 95, Close: 999, Volume: 999,
		Provider: "test", IngestedAt: it(time.Now()),
	})
	if err == nil {
		t.Fatal("UpsertForming on an already-closed candle succeeded, want a rejection from the lifecycle trigger")
	}
}

func TestIntegration_Close_IsIdempotent(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db, clock.Real{})
	openTime := et(2026, time.October, 26, 18, 1)

	if _, err := store.UpsertForming(context.Background(), Tick{
		InstrumentID: instrumentID, OpenTime: openTime,
		Open: 100, High: 105, Low: 95, Close: 100, Volume: 10,
		Provider: "test", IngestedAt: it(time.Now()),
	}); err != nil {
		t.Fatalf("UpsertForming: %v", err)
	}

	first, err := store.Close(context.Background(), instrumentID, openTime)
	if err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// A worker retrying a close it is not sure succeeded must get success
	// back, not an error from the trigger's handshake-step check.
	second, err := store.Close(context.Background(), instrumentID, openTime)
	if err != nil {
		t.Fatalf("second (repeat) Close: %v, want idempotent success", err)
	}
	if second.ID != first.ID || !second.IsClosed {
		t.Fatalf("repeat Close = %+v, want the same closed row as %+v", second, first)
	}
}

func TestIntegration_Close_NotFound(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db, clock.Real{})

	_, err := store.Close(context.Background(), instrumentID, et(2026, time.October, 26, 23, 59))
	if !errors.Is(err, ErrCandleNotFound) {
		t.Fatalf("Close on a nonexistent candle: err = %v, want ErrCandleNotFound", err)
	}
}

// TestIntegration_SupersedeCandle_FullHandshake is the test the schema
// design turned on: it proves the three-step handshake --
//
//  1. begin superseding (superseded_at set, superseded_by still NULL),
//  2. insert the correcting revision,
//  3. resolve the pointer (superseded_by set)
//
// -- commits as one transaction, leaves exactly one current row behind,
// and leaves the old row fully terminal. This is exactly the sequence
// that failed on the first design attempt (a single-step
// superseded_at+superseded_by update is impossible because Postgres
// cannot make a partial unique index DEFERRABLE), so this test is the
// actual proof the fix holds, not just a read of the SQL.
func TestIntegration_SupersedeCandle_FullHandshake(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db, clock.Real{})
	openTime := et(2026, time.October, 26, 18, 0)

	original, err := store.UpsertForming(context.Background(), Tick{
		InstrumentID: instrumentID, OpenTime: openTime,
		Open: 100, High: 105, Low: 95, Close: 100, Volume: 10,
		Provider: "test", IngestedAt: it(time.Now()),
	})
	if err != nil {
		t.Fatalf("UpsertForming: %v", err)
	}
	if _, err := store.Close(context.Background(), instrumentID, openTime); err != nil {
		t.Fatalf("Close: %v", err)
	}

	corrected, err := store.SupersedeCandle(context.Background(), instrumentID, openTime, Correction{
		Open: 100, High: 130, Low: 90, Close: 120, Volume: 50,
		Provider: "test", IngestedAt: it(time.Now()),
	})
	if err != nil {
		t.Fatalf("SupersedeCandle: %v", err)
	}

	// The new row: revision 2, closed, current (not itself superseded).
	if corrected.Revision != original.Revision+1 {
		t.Errorf("corrected.Revision = %d, want %d", corrected.Revision, original.Revision+1)
	}
	if !corrected.IsClosed {
		t.Error("corrected candle is not closed")
	}
	if corrected.SupersededAt != nil {
		t.Errorf("corrected candle has SupersededAt = %v, want nil (it is the new current row)", corrected.SupersededAt)
	}
	if corrected.High != 130 || corrected.Close != 120 || corrected.Volume != 50 {
		t.Errorf("corrected candle = %+v, want the corrected values", corrected)
	}

	// GetCurrent must now return the corrected row, not the original.
	current, err := store.GetCurrent(context.Background(), instrumentID, openTime)
	if err != nil {
		t.Fatalf("GetCurrent: %v", err)
	}
	if current.ID != corrected.ID {
		t.Fatalf("GetCurrent returned id=%d, want the corrected row's id=%d", current.ID, corrected.ID)
	}

	// The old row must be fully terminal: superseded_at AND superseded_by
	// both set, pointing at the new row, and the schema must refuse any
	// further change to it whatsoever.
	oldRow := getCandleSupersedeState(t, db, original.ID)
	if !oldRow.SupersededAtSet {
		t.Error("old row's superseded_at is not set")
	}
	if oldRow.SupersededBy != corrected.ID {
		t.Errorf("old row's superseded_by = %d, want it to point at %d", oldRow.SupersededBy, corrected.ID)
	}
	if err := attemptUpdate(db, original.ID); err == nil {
		t.Error("a fully superseded row accepted an update, want the trigger to reject it unconditionally")
	}

	// Exactly one current row must exist for this open_time -- the
	// partial unique index's guarantee, checked directly.
	assertExactlyOneCurrentRow(t, db, instrumentID, openTime.Time())
}

// TestIntegration_SupersedeCandle_RejectsStillForming proves the schema
// correctly refuses to "correct" a candle that was never closed -- only a
// closed candle has a meaningful prior value to correct.
func TestIntegration_SupersedeCandle_RejectsStillForming(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db, clock.Real{})
	openTime := et(2026, time.October, 26, 18, 2)

	if _, err := store.UpsertForming(context.Background(), Tick{
		InstrumentID: instrumentID, OpenTime: openTime,
		Open: 100, High: 105, Low: 95, Close: 100, Volume: 10,
		Provider: "test", IngestedAt: it(time.Now()),
	}); err != nil {
		t.Fatalf("UpsertForming: %v", err)
	}

	_, err := store.SupersedeCandle(context.Background(), instrumentID, openTime, Correction{
		Open: 100, High: 130, Low: 90, Close: 120, Volume: 50,
		Provider: "test", IngestedAt: it(time.Now()),
	})
	if !errors.Is(err, ErrCandleStillForming) {
		t.Fatalf("SupersedeCandle on a forming candle: err = %v, want ErrCandleStillForming", err)
	}
}

// TestIntegration_SupersedeCandle_Twice proves a second correction on top
// of the first works the same way: the handshake is not a one-shot
// special case, it is the only way corrections ever happen.
func TestIntegration_SupersedeCandle_Twice(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db, clock.Real{})
	openTime := et(2026, time.October, 26, 18, 3)

	if _, err := store.UpsertForming(context.Background(), Tick{
		InstrumentID: instrumentID, OpenTime: openTime,
		Open: 100, High: 105, Low: 95, Close: 100, Volume: 10,
		Provider: "test", IngestedAt: it(time.Now()),
	}); err != nil {
		t.Fatalf("UpsertForming: %v", err)
	}
	if _, err := store.Close(context.Background(), instrumentID, openTime); err != nil {
		t.Fatalf("Close: %v", err)
	}

	first, err := store.SupersedeCandle(context.Background(), instrumentID, openTime, Correction{
		Open: 100, High: 130, Low: 90, Close: 120, Volume: 50,
		Provider: "test", IngestedAt: it(time.Now()),
	})
	if err != nil {
		t.Fatalf("first SupersedeCandle: %v", err)
	}

	second, err := store.SupersedeCandle(context.Background(), instrumentID, openTime, Correction{
		Open: 100, High: 140, Low: 85, Close: 135, Volume: 60,
		Provider: "test", IngestedAt: it(time.Now()),
	})
	if err != nil {
		t.Fatalf("second SupersedeCandle: %v", err)
	}
	if second.Revision != first.Revision+1 {
		t.Fatalf("second.Revision = %d, want %d", second.Revision, first.Revision+1)
	}

	assertExactlyOneCurrentRow(t, db, instrumentID, openTime.Time())

	current, err := store.GetCurrent(context.Background(), instrumentID, openTime)
	if err != nil {
		t.Fatalf("GetCurrent: %v", err)
	}
	if current.ID != second.ID {
		t.Fatalf("GetCurrent = id %d, want the latest revision's id %d", current.ID, second.ID)
	}
}

// --- direct SQL assertions the store API has no reason to expose, used
// only to confirm the schema's own guarantees from the test's side. ---

type candleSupersedeState struct {
	SupersededAtSet bool
	SupersededBy    int64 // 0 when not set
}

func getCandleSupersedeState(t *testing.T, db *sql.DB, id int64) candleSupersedeState {
	t.Helper()
	var s candleSupersedeState
	var supersededBy sql.NullInt64
	err := db.QueryRowContext(context.Background(),
		`SELECT superseded_at IS NOT NULL, superseded_by FROM candles WHERE id = $1`, id,
	).Scan(&s.SupersededAtSet, &supersededBy)
	if err != nil {
		t.Fatalf("get candle %d supersede state: %v", id, err)
	}
	if supersededBy.Valid {
		s.SupersededBy = supersededBy.Int64
	}
	return s
}

// attemptUpdate tries to change an arbitrary, clearly-frozen column
// (provider) on the given row. A fully superseded row must reject this
// unconditionally, regardless of which column is touched.
func attemptUpdate(db *sql.DB, id int64) error {
	_, err := db.ExecContext(context.Background(),
		`UPDATE candles SET provider = 'tampered' WHERE id = $1`, id)
	return err
}

func assertExactlyOneCurrentRow(t *testing.T, db *sql.DB, instrumentID int64, openTime time.Time) {
	t.Helper()
	var count int
	err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM candles WHERE instrument_id = $1 AND open_time = $2 AND superseded_at IS NULL`,
		instrumentID, openTime,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count current rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("current rows for instrument=%d open_time=%s = %d, want exactly 1", instrumentID, openTime, count)
	}
}
