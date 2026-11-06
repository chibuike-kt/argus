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

// TestIntegration_ListAllFiltered_KeysetAndLimit is the backend-guards-
// cleanup brief's own required proof for GET /instruments' new
// pagination: afterID excludes everything up to and including the
// cursor, ordering is strictly by id (ListAllInstrumentsPaged's own
// documented departure from ListAll's provider/native_symbol order),
// and limit is enforced by the query itself, not by fetching
// everything and truncating in Go.
func TestIntegration_ListAllFiltered_KeysetAndLimit(t *testing.T) {
	db := testDB(t)
	store := NewStore(db)
	ctx := context.Background()

	var maxIDBefore int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM instruments`).Scan(&maxIDBefore); err != nil {
		t.Fatalf("select max(id): %v", err)
	}

	priceScale := quant.Scale(2)
	qtyScale := quant.Scale(6)
	insertOne := func(label string) Instrument {
		inst, err := store.Insert(ctx, NewInstrumentParams{
			Provider:     "test-provider",
			NativeSymbol: fmt.Sprintf("PAGE-%s-%d", label, time.Now().UnixNano()),
			AssetClass:   "crypto_perp",
			PriceScale:   &priceScale,
			QtyScale:     &qtyScale,
			TickSize:     1,
			QtyStepSize:  1,
		})
		if err != nil {
			t.Fatalf("Insert (%s): %v", label, err)
		}
		return inst
	}
	a := insertOne("a")
	b := insertOne("b")
	c := insertOne("c")

	firstPage, err := store.ListAllFiltered(ctx, &maxIDBefore, 2)
	if err != nil {
		t.Fatalf("ListAllFiltered (first page): %v", err)
	}
	if len(firstPage) != 2 || firstPage[0].ID != a.ID || firstPage[1].ID != b.ID {
		t.Fatalf("firstPage = %+v, want exactly [a(%d), b(%d)]", firstPage, a.ID, b.ID)
	}

	secondPage, err := store.ListAllFiltered(ctx, &b.ID, 2)
	if err != nil {
		t.Fatalf("ListAllFiltered (second page): %v", err)
	}
	if len(secondPage) != 1 || secondPage[0].ID != c.ID {
		t.Fatalf("secondPage = %+v, want exactly [c(%d)]", secondPage, c.ID)
	}

	// A nil cursor is "from the start," not "unlimited" -- with a long-
	// lived test database accumulating rows across many previous runs,
	// a nil-cursor page capped at the default size legitimately will
	// not reach rows this far into the table. Re-querying from
	// maxIDBefore with a wide-enough limit is the meaningful version
	// of "everything after the cursor, in one page" this test can make
	// without depending on how many rows other tests left behind.
	allAfterCursor, err := store.ListAllFiltered(ctx, &maxIDBefore, 500)
	if err != nil {
		t.Fatalf("ListAllFiltered (wide page after cursor): %v", err)
	}
	found := map[int64]bool{}
	for _, inst := range allAfterCursor {
		found[inst.ID] = true
	}
	if !found[a.ID] || !found[b.ID] || !found[c.ID] {
		t.Fatalf("ListAllFiltered(maxIDBefore, 500) missing one of a(%d)/b(%d)/c(%d): got %d rows", a.ID, b.ID, c.ID, len(allAfterCursor))
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
