//go:build integration

package instruments

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/quant"

	_ "github.com/lib/pq"
)

// These tests exercise 000001/000002's real constraints and
// trg_instruments_prevent_scale_update -- a handwritten fake cannot verify
// a database trigger (guide 10). Run with:
//   go test -tags=integration -race ./internal/instruments/...
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

func TestIntegration_Insert_FailsWithoutExplicitScale(t *testing.T) {
	db := testDB(t)
	store := NewStore(db)

	// This bypasses NewInstrumentParams.validate's own nil check by
	// constructing the sqlc call directly where that is possible... but
	// Insert always validates first, so the real way to observe the
	// database-level guarantee is to go around the Go-level check. Both
	// checks exist and this test is specifically for the one closer to
	// the metal: the column has no DEFAULT (000002), so even a caller
	// that bypassed this package's validation would still be stopped.
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO instruments (provider, native_symbol, asset_class) VALUES ($1, $2, $3)`,
		"test-provider", fmt.Sprintf("RAW-%d", time.Now().UnixNano()), "crypto_perp")
	if err == nil {
		t.Fatal("raw INSERT omitting price_scale/qty_scale/tick_size/qty_step_size succeeded, want the NOT NULL columns to reject it")
	}

	// And the package-level API fails the same way, earlier, with a clear
	// sentinel rather than a database round trip.
	_, err = store.Insert(context.Background(), NewInstrumentParams{
		Provider:     "test-provider",
		NativeSymbol: fmt.Sprintf("PKG-%d", time.Now().UnixNano()),
		AssetClass:   "crypto_perp",
		// PriceScale, QtyScale deliberately left nil.
		TickSize:    1,
		QtyStepSize: 1,
	})
	if !errors.Is(err, ErrPriceScaleRequired) {
		t.Fatalf("Insert without scale: err = %v, want ErrPriceScaleRequired", err)
	}
}

func TestIntegration_Insert_WithExplicitScale_Succeeds(t *testing.T) {
	db := testDB(t)
	store := NewStore(db)
	priceScale := quant.Scale(2)
	qtyScale := quant.Scale(6)

	inst, err := store.Insert(context.Background(), NewInstrumentParams{
		Provider:     "test-provider",
		NativeSymbol: fmt.Sprintf("OK-%d", time.Now().UnixNano()),
		AssetClass:   "crypto_perp",
		PriceScale:   &priceScale,
		QtyScale:     &qtyScale,
		TickSize:     1,
		QtyStepSize:  1,
	})
	if err != nil {
		t.Fatalf("Insert with explicit scale: %v", err)
	}
	if inst.Enabled {
		t.Error("Insert produced an enabled instrument -- enablement must stay unsettable (ADR-007)")
	}

	got, err := store.GetByID(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.PriceScale != priceScale || got.QtyScale != qtyScale {
		t.Fatalf("GetByID = %+v, want PriceScale=%d QtyScale=%d", got, priceScale, qtyScale)
	}
}

func TestIntegration_Insert_DuplicateProviderSymbol_Rejected(t *testing.T) {
	db := testDB(t)
	store := NewStore(db)
	priceScale := quant.Scale(2)
	qtyScale := quant.Scale(6)
	symbol := fmt.Sprintf("DUP-%d", time.Now().UnixNano())

	params := NewInstrumentParams{
		Provider:     "test-provider",
		NativeSymbol: symbol,
		AssetClass:   "crypto_perp",
		PriceScale:   &priceScale,
		QtyScale:     &qtyScale,
		TickSize:     1,
		QtyStepSize:  1,
	}
	if _, err := store.Insert(context.Background(), params); err != nil {
		t.Fatalf("first Insert: %v", err)
	}
	_, err := store.Insert(context.Background(), params)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("second Insert with same provider/symbol: err = %v, want ErrAlreadyExists", err)
	}
}

// TestIntegration_ScaleIsImmutable proves
// trg_instruments_prevent_scale_update rejects any change to price_scale
// or qty_scale unconditionally -- there is no "only if candles exist"
// exception (approved decision: scale is identity, not a mutable
// attribute).
func TestIntegration_ScaleIsImmutable(t *testing.T) {
	db := testDB(t)
	store := NewStore(db)
	priceScale := quant.Scale(2)
	qtyScale := quant.Scale(6)

	inst, err := store.Insert(context.Background(), NewInstrumentParams{
		Provider:     "test-provider",
		NativeSymbol: fmt.Sprintf("IMMUT-%d", time.Now().UnixNano()),
		AssetClass:   "crypto_perp",
		PriceScale:   &priceScale,
		QtyScale:     &qtyScale,
		TickSize:     1,
		QtyStepSize:  1,
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// No candles exist for this instrument at all -- confirming the
	// trigger has no such exception, not merely that it works once
	// candles are present.
	if _, err := db.ExecContext(context.Background(),
		`UPDATE instruments SET price_scale = 4 WHERE id = $1`, inst.ID); err == nil {
		t.Error("UPDATE price_scale succeeded on an instrument with zero candles, want the trigger to reject it unconditionally")
	}
	if _, err := db.ExecContext(context.Background(),
		`UPDATE instruments SET qty_scale = 8 WHERE id = $1`, inst.ID); err == nil {
		t.Error("UPDATE qty_scale succeeded, want the trigger to reject it")
	}

	// An unrelated column must still be updatable -- the trigger targets
	// the two scale columns specifically, not the whole row.
	if _, err := db.ExecContext(context.Background(),
		`UPDATE instruments SET contract_type = 'perpetual' WHERE id = $1`, inst.ID); err != nil {
		t.Errorf("UPDATE of an unrelated column was rejected: %v, want it to succeed", err)
	}
}

func TestIntegration_GetByProviderAndSymbol_NotFound(t *testing.T) {
	db := testDB(t)
	store := NewStore(db)
	_, err := store.GetByProviderAndSymbol(context.Background(), "nobody", "NOTHING")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
