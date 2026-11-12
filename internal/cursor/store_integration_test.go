//go:build integration

package cursor

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/quant"

	// lib/pq registers the "postgres" driver.
	_ "github.com/lib/pq"
)

// This package had no test file of its own (project-status audit,
// section H) -- it was only ever exercised transitively through
// cmd/argus-worker's cursor/idempotency integration tests. These are
// integration tests, not unit tests: Store talks directly to sqlc-
// generated queries against real Postgres, with no interface boundary
// to fake (guide 10) -- the same reason internal/instruments' own tests
// are integration-only.
// Run with: go test -tags=integration ./internal/cursor/...
// against a Postgres with all migrations applied (make migrate-up).

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("ARGUS_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://argus:argus@localhost:5432/argus?sslmode=disable"
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("integration test database unreachable at %s: %v", dsn, err)
	}
	return db
}

// testInstrument seeds a real instrument row -- processing_cursors.
// instrument_id is a real foreign key, so Get/SetAt need one to exist.
func testInstrument(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	priceScale := quant.Scale(0)
	qtyScale := quant.Scale(0)
	symbol := fmt.Sprintf("CURSOR-%s-%d", t.Name(), time.Now().UnixNano())

	inst, err := instruments.NewStore(db).Insert(context.Background(), instruments.NewInstrumentParams{
		Provider: "test-provider", NativeSymbol: symbol, AssetClass: "crypto_perp",
		PriceScale: &priceScale, QtyScale: &qtyScale, TickSize: 1, QtyStepSize: 1,
		SupportedIntervals: []string{"1m"},
	})
	if err != nil {
		t.Fatalf("create test instrument: %v", err)
	}
	return inst.ID
}

func TestIntegration_Get_NotFound(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	s := NewStore(db)

	_, err := s.Get(context.Background(), instrumentID, "1m")
	if err != ErrNotFound {
		t.Fatalf("Get (never advanced) = %v, want ErrNotFound", err)
	}
}

func TestIntegration_SetAt_InsertThenUpdate(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	s := NewStore(db)
	ctx := context.Background()
	now := time.Now().UTC()

	first := time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC)
	if err := s.SetAt(ctx, instrumentID, "1m", first, now); err != nil {
		t.Fatalf("SetAt (insert): %v", err)
	}
	got, err := s.Get(ctx, instrumentID, "1m")
	if err != nil {
		t.Fatalf("Get (after insert): %v", err)
	}
	if !got.Equal(first) {
		t.Fatalf("Get (after insert) = %v, want %v", got, first)
	}

	second := first.Add(time.Minute)
	if err := s.SetAt(ctx, instrumentID, "1m", second, now.Add(time.Second)); err != nil {
		t.Fatalf("SetAt (update): %v", err)
	}
	got, err = s.Get(ctx, instrumentID, "1m")
	if err != nil {
		t.Fatalf("Get (after update): %v", err)
	}
	if !got.Equal(second) {
		t.Fatalf("Get (after update) = %v, want %v (the later value)", got, second)
	}
}

// TestIntegration_SetAt_AllowsBackwardMovement is a finding, not a
// regression guard: db/queries/processing_cursors.sql's own
// SetProcessingCursor is a plain `ON CONFLICT ... DO UPDATE SET
// last_processed_close_time = EXCLUDED...` -- it compares nothing, so a
// caller that calls SetAt with a closeTime earlier than the one already
// stored silently moves the cursor backwards. This test pins that
// ACTUAL behaviour down (so a future change to the query is a visible,
// deliberate diff against this test, not a silent behaviour change);
// it does not assert this is correct, and this brief's own instruction
// is to report it, not fix it -- see the brief's own report for the
// write-up. In practice cmd/argus-worker never calls SetAt with
// anything other than a strictly-increasing close_time (its own
// per-candle loop processes candles in ascending order), so this is not
// known to be reachable in the real worker today.
func TestIntegration_SetAt_AllowsBackwardMovement(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	s := NewStore(db)
	ctx := context.Background()
	now := time.Now().UTC()

	later := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	if err := s.SetAt(ctx, instrumentID, "1m", later, now); err != nil {
		t.Fatalf("SetAt (later): %v", err)
	}

	earlier := later.Add(-time.Hour)
	if err := s.SetAt(ctx, instrumentID, "1m", earlier, now.Add(time.Second)); err != nil {
		t.Fatalf("SetAt (earlier): %v", err)
	}

	got, err := s.Get(ctx, instrumentID, "1m")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Equal(earlier) {
		t.Fatalf("Get = %v, want %v: SetAt is expected (today) to silently accept a backward move -- "+
			"if this now fails, SetProcessingCursor's own query gained a monotonic guard and this test's own comment is stale",
			got, earlier)
	}
}
