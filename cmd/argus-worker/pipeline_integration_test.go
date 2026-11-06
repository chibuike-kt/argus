//go:build integration

package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/ingestion/fixture"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/setups"

	// lib/pq registers the "postgres" driver.
	_ "github.com/lib/pq"
)

// TestIntegration_Pipeline_DevFixtureIngestThroughSetupsLifecycle proves
// sections 1-15 work together as a pipeline, not just individually
// (the gap this whole brief exists to close): seed a dev/test fixture
// instrument the same way db/seed/dev_fixture_instruments.sql does,
// replay the checked-in dataset into it via runDevFixtureIngest (the
// same path cmd/argus-worker's main.go takes when
// ARGUS_DEV_FIXTURE_INGEST is set), drive the real processor.run over
// the whole dataset, and assert a setups row actually transitioned --
// not just that one was created.
//
// Run with: go test -tags=integration ./cmd/argus-worker/...
// against a Postgres with all migrations applied (make migrate-up).
func TestIntegration_Pipeline_DevFixtureIngestThroughSetupsLifecycle(t *testing.T) {
	db := testWorkerDB(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Load the real checked-in (embedded) dataset, then re-key its one
	// symbol's candles to a per-test-run unique value:
	// runDevFixtureIngest resolves instruments by data.Candles' own
	// map keys, and the dataset's literal "BTC-PERP"/"ETH-PERP" keys
	// are only meant to match the real dev/test deployment's seeded
	// rows, not a test instrument that must avoid colliding with other
	// runs or with make seed-dev's own rows in the same database.
	loaded, err := fixture.DevDataset()
	if err != nil {
		t.Fatalf("DevDataset: %v", err)
	}
	symbol := fmt.Sprintf("BTC-PERP-%s-%d", t.Name(), time.Now().UnixNano())
	candles := loaded.Candles["BTC-PERP"]
	if len(candles) == 0 {
		t.Fatal("dev dataset has no BTC-PERP candles")
	}
	for i := range candles {
		candles[i].Symbol = symbol
	}
	data := fixture.Data{Candles: map[string][]ingestion.ProviderCandle{symbol: candles}}

	instrumentStore := instruments.NewStore(db)
	instrumentID := seedDevFixtureInstrument(t, db, instrumentStore, symbol)

	if err := runDevFixtureIngest(ctx, logger, data, instrumentStore, marketdata.NewStore(db, clock.Real{})); err != nil {
		t.Fatalf("runDevFixtureIngest: %v", err)
	}

	clk := clock.Real{}
	marketdataStore := marketdata.NewStore(db, clk)
	candleSource := analysis.NewMarketdataSource(marketdataStore)
	analysisStore := analysis.NewStore(db)
	setupsStore := setups.NewStore(db)
	engine, err := analysis.NewEngine(db, candleSource, analysisStore, clk, analysis.Config{
		AlgorithmVersion: algorithmVersion, LeftBars: 2, RightBars: 2, Interval: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	proc := newProcessor(db, logger, clk, instrumentStore, candleSource, engine, analysisStore, setupsStore)

	// One call already walks the full backlog (processInstrument loops
	// over every new candle up to the latest closed one, not just one
	// per call) -- three ticks here mainly exercises that a later tick
	// with nothing new is a correct no-op, matching how the real
	// worker's ticker would behave once it catches up.
	for range 3 {
		proc.run(ctx)
	}

	all, err := setupsStore.ListByInstrument(ctx, instrumentID, "1m")
	if err != nil {
		t.Fatalf("ListByInstrument: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("ListByInstrument: no setups rows -- the pipeline produced nothing")
	}

	var reachedPastDetected bool
	for _, s := range all {
		t.Logf("setup id=%d poi_type=%s state=%s", s.ID, s.POIType, s.State)
		if s.State != setups.StateDetected {
			reachedPastDetected = true
		}
	}
	if !reachedPastDetected {
		t.Fatalf("no setup reached a state past Detected across %d setups -- the pipeline created rows but never observed a real transition", len(all))
	}
}

func testWorkerDB(t *testing.T) *sql.DB {
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

// seedDevFixtureInstrument registers an instrument under the given
// (already per-test-run unique, see the call site) symbol, then
// enables it directly via SQL -- the same bypass
// db/seed/dev_fixture_instruments.sql uses, never routed through
// instruments.Store (which has no method that can do this, and never
// will until ADR-007 resolves). provider is fixed to
// devFixtureProviderName so ingestion.InstrumentCache resolves it the
// same way the real seed's rows are resolved.
func seedDevFixtureInstrument(t *testing.T, db *sql.DB, store *instruments.Store, symbol string) int64 {
	t.Helper()
	priceScale := quant.Scale(0)
	qtyScale := quant.Scale(0)

	inst, err := store.Insert(context.Background(), instruments.NewInstrumentParams{
		Provider: devFixtureProviderName, NativeSymbol: symbol, AssetClass: "crypto_perp",
		PriceScale: &priceScale, QtyScale: &qtyScale, TickSize: 1, QtyStepSize: 1,
	})
	if err != nil {
		t.Fatalf("create dev fixture instrument: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE instruments SET enabled = TRUE WHERE id = $1`, inst.ID); err != nil {
		t.Fatalf("enable dev fixture instrument: %v", err)
	}
	return inst.ID
}
