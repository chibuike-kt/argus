//go:build integration

package marketdata

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/quant"

	// lib/pq registers the "postgres" driver.
	_ "github.com/lib/pq"
)

// testDB connects to the integration test database named by
// ARGUS_TEST_DATABASE_URL (falling back to the docker-compose default) and
// skips the test if it is unreachable, rather than failing the whole suite
// when Postgres is not running locally.
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
		t.Skipf("integration test database unreachable at %s: %v (start it with: docker compose up -d postgres, then apply migrations)", dsn, err)
	}
	return db
}

// testInstrument registers a fresh instrument with a unique symbol per
// call (derived from the test name and the clock), so concurrent and
// repeated test runs do not collide on the (provider, native_symbol)
// uniqueness constraint. It returns the instrument id.
func testInstrument(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	priceScale := quant.Scale(2)
	qtyScale := quant.Scale(6)
	symbol := fmt.Sprintf("TEST-%s-%d", t.Name(), time.Now().UnixNano())

	inst, err := instruments.NewStore(db).Insert(context.Background(), instruments.NewInstrumentParams{
		Provider:     "test-provider",
		NativeSymbol: symbol,
		AssetClass:   "crypto_perp",
		PriceScale:   &priceScale,
		QtyScale:     &qtyScale,
		TickSize:     1,
		QtyStepSize:  1,
	})
	if err != nil {
		t.Fatalf("create test instrument: %v", err)
	}
	return inst.ID
}

func et(y int, mo time.Month, d, h, mi int) clock.EventTime {
	return clock.NewEventTime(time.Date(y, mo, d, h, mi, 0, 0, time.UTC))
}

func it(t time.Time) clock.IngestionTime { return clock.NewIngestionTime(t) }
