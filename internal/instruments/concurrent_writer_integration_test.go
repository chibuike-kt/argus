//go:build integration

package instruments

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/quant"
)

// The CI flake (got ids [160 162], want [160]): `go test ./...` runs
// packages in parallel against one shared database, and another package's
// tests insert instruments (disabled, the Insert default) while this
// package lists them. Ids come from one sequence, so a foreign row can
// land anywhere after this test's own rows -- inside its keyset window.
//
// startForeignWriter is that other package: it inserts disabled
// instruments from another goroutine until stopped, and returns how many
// it inserted (zero would mean the test proved nothing).
func startForeignWriter(t *testing.T, db *sql.DB) (stop func() int64) {
	t.Helper()
	store := NewStore(db)
	ctx, cancel := context.WithCancel(context.Background())
	var n atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		priceScale, qtyScale := quant.Scale(2), quant.Scale(6)
		for ctx.Err() == nil {
			_, err := store.Insert(ctx, NewInstrumentParams{
				Provider: "test-foreign-writer", NativeSymbol: fmt.Sprintf("FOREIGN-%d", time.Now().UnixNano()),
				AssetClass: "crypto_perp", PriceScale: &priceScale, QtyScale: &qtyScale,
				TickSize: 1, QtyStepSize: 1, SupportedIntervals: []string{"1m"},
			})
			if err == nil {
				n.Add(1)
			}
		}
	}()
	return func() int64 {
		cancel()
		wg.Wait()
		return n.Load()
	}
}

// TestIntegration_ListAllFiltered_UnaffectedByConcurrentWriters runs both
// listing tests while another goroutine keeps inserting disabled
// instruments: their result must not depend on what other packages write.
func TestIntegration_ListAllFiltered_UnaffectedByConcurrentWriters(t *testing.T) {
	db := testDB(t)
	var zone string
	if err := db.QueryRow("SHOW TimeZone").Scan(&zone); err != nil {
		t.Fatal(err)
	}
	t.Logf("session time zone %s", zone)
	stop := startForeignWriter(t, db)
	for i := 0; i < 5; i++ {
		checkEnabledFilter(t, db)
		checkKeysetAndLimit(t, db)
	}
	if n := stop(); n == 0 {
		t.Fatal("the foreign writer inserted nothing: this run proves nothing")
	} else {
		t.Logf("foreign writer inserted %d disabled instruments during the run", n)
	}
}
