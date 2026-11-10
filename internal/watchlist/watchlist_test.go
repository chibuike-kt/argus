//go:build integration

package watchlist

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/quant"
)

// Run with (needs a real Postgres with all migrations applied):
//
//	go test -tags=integration ./internal/watchlist/...
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("ARGUS_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://argus:argus@localhost:5432/argus_test?sslmode=disable"
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("integration test database unreachable: %v", err)
	}
	return db
}

// testInstrument creates an instrument, enabled or not -- enablement
// is flipped by a direct SQL update since internal/instruments.Store
// deliberately has no code path to do that itself while ADR-007 is
// open.
func testInstrument(t *testing.T, db *sql.DB, enabled bool) int64 {
	t.Helper()
	priceScale := quant.Scale(0)
	qtyScale := quant.Scale(0)
	symbol := fmt.Sprintf("WATCHLIST-%s-%d", t.Name(), time.Now().UnixNano())
	inst, err := instruments.NewStore(db).Insert(context.Background(), instruments.NewInstrumentParams{
		Provider: "test-provider", NativeSymbol: symbol, AssetClass: "crypto_perp",
		PriceScale: &priceScale, QtyScale: &qtyScale, TickSize: 1, QtyStepSize: 1,
	})
	if err != nil {
		t.Fatalf("create test instrument: %v", err)
	}
	if enabled {
		if _, err := db.ExecContext(context.Background(), `UPDATE instruments SET enabled = true WHERE id = $1`, inst.ID); err != nil {
			t.Fatalf("enable test instrument: %v", err)
		}
	}
	return inst.ID
}

// testUser creates a verified user directly -- this package's own
// tests are about watchlist_items, not signup, so there is no need to
// go through the real signup flow for one.
func testUser(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var id int64
	email := fmt.Sprintf("watchlist-test-%d@example.com", time.Now().UnixNano())
	err := db.QueryRowContext(context.Background(), `
		INSERT INTO users (email, email_normalized, password_hash, email_verified_at)
		VALUES ($1, $1, 'x', now()) RETURNING id`, email).Scan(&id)
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return id
}

func TestIntegration_Replace_ReplacesWholeListInOrder(t *testing.T) {
	db := testDB(t)
	store := NewStore(db, instruments.NewStore(db))
	userID := testUser(t, db)
	a := testInstrument(t, db, true)
	b := testInstrument(t, db, true)

	items, err := store.Replace(context.Background(), userID, []int64{a, b}, 50)
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if len(items) != 2 || items[0].InstrumentID != a || items[0].Position != 0 || items[1].InstrumentID != b || items[1].Position != 1 {
		t.Fatalf("Replace returned %+v, want [{%d 0} {%d 1}]", items, a, b)
	}

	count, err := store.Count(context.Background(), userID)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 2 {
		t.Fatalf("Count = %d, want 2", count)
	}

	// Replacing again with just b, in position 0, drops a entirely --
	// a full replace, not an incremental add/remove.
	items, err = store.Replace(context.Background(), userID, []int64{b}, 50)
	if err != nil {
		t.Fatalf("second Replace: %v", err)
	}
	if len(items) != 1 || items[0].InstrumentID != b || items[0].Position != 0 {
		t.Fatalf("second Replace returned %+v, want [{%d 0}]", items, b)
	}

	list, err := store.List(context.Background(), userID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].InstrumentID != b {
		t.Fatalf("List = %+v, want [{%d 0}]", list, b)
	}
}

func TestIntegration_Replace_TooManyItems_Rejected(t *testing.T) {
	db := testDB(t)
	store := NewStore(db, instruments.NewStore(db))
	userID := testUser(t, db)
	a := testInstrument(t, db, true)

	if _, err := store.Replace(context.Background(), userID, []int64{a}, 0); !errors.Is(err, ErrTooManyItems) {
		t.Fatalf("Replace(1 item, max 0) = %v, want ErrTooManyItems", err)
	}
}

func TestIntegration_Replace_UnknownInstrument_Rejected(t *testing.T) {
	db := testDB(t)
	store := NewStore(db, instruments.NewStore(db))
	userID := testUser(t, db)

	if _, err := store.Replace(context.Background(), userID, []int64{999999999}, 50); !errors.Is(err, ErrInstrumentNotFound) {
		t.Fatalf("Replace(unknown instrument) = %v, want ErrInstrumentNotFound", err)
	}
}

func TestIntegration_Replace_DisabledInstrument_Rejected(t *testing.T) {
	db := testDB(t)
	store := NewStore(db, instruments.NewStore(db))
	userID := testUser(t, db)
	disabled := testInstrument(t, db, false)

	if _, err := store.Replace(context.Background(), userID, []int64{disabled}, 50); !errors.Is(err, ErrInstrumentDisabled) {
		t.Fatalf("Replace(disabled instrument) = %v, want ErrInstrumentDisabled", err)
	}
}
