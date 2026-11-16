//go:build integration

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/ingestion/fixture"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"

	_ "github.com/lib/pq"
)

func candleSeries(symbol string, n int, basePrice int64) []ingestion.ProviderCandle {
	out := make([]ingestion.ProviderCandle, 0, n)
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		p := basePrice + int64(i)
		out = append(out, ingestion.ProviderCandle{
			Symbol: symbol, OpenTime: base.Add(time.Duration(i) * time.Minute),
			Open: fmt.Sprintf("%d", p), High: fmt.Sprintf("%d", p+1), Low: fmt.Sprintf("%d", p-1), Close: fmt.Sprintf("%d", p),
			Volume: "1", IsFinal: true,
		})
	}
	return out
}

// TestIntegration_RunDevFixtureIngest_SkipsSymbolWithNoInstrumentRow is
// the real regression report: a dataset symbol with no instrument row
// (db/seed/e2e_only_instruments.sql's own SOL-PERP, present in
// internal/ingestion/fixture/testdata/dev_dataset.json but absent from
// a plain dev_fixture_instruments.sql-only database) used to make
// runDevFixtureIngest return an error and the worker exit(1) at
// startup. Confirmed to fail without the fix: reverting the
// errors.Is(err, instruments.ErrNotFound) branch back to an
// unconditional return produces "ingest historical
// MISSING-...: ingestion: historical fixture/MISSING-...: resolve
// instrument: ... instruments: not found", and the present symbol's
// own candles never land because the loop returns before reaching it
// (sorted order: "MISSING-..." < "PRESENT-..." alphabetically is not
// guaranteed by itself, so this test seeds both orderings' worth of
// confidence by checking the present symbol actually ingested, not
// just that no error was returned).
func TestIntegration_RunDevFixtureIngest_SkipsSymbolWithNoInstrumentRow(t *testing.T) {
	db := testWorkerDB(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	presentSymbol := fmt.Sprintf("PRESENT-%s-%d", t.Name(), time.Now().UnixNano())
	missingSymbol := fmt.Sprintf("MISSING-%s-%d", t.Name(), time.Now().UnixNano())

	instrumentStore := instruments.NewStore(db)
	presentID := seedDevFixtureInstrument(t, db, instrumentStore, presentSymbol)
	// missingSymbol deliberately gets no instrument row at all -- the
	// exact shape of the real regression.

	data := fixture.Data{Candles: map[string][]ingestion.ProviderCandle{
		presentSymbol: candleSeries(presentSymbol, 5, 100),
		missingSymbol: candleSeries(missingSymbol, 5, 200),
	}}

	marketdataStore := marketdata.NewStore(db, clock.Real{})
	err := runDevFixtureIngest(ctx, logger, data, instrumentStore, marketdataStore, nil)
	if err != nil {
		t.Fatalf("runDevFixtureIngest = %v, want nil (a missing-instrument symbol must be skipped, not fail the whole ingest)", err)
	}

	// The present symbol's own candles actually landed -- not just
	// that no error was returned (a version that skipped BOTH symbols
	// unconditionally would also return nil here).
	got, err := marketdataStore.GetCurrent(ctx, presentID, clock.NewEventTime(data.Candles[presentSymbol][0].OpenTime))
	if err != nil {
		t.Fatalf("GetCurrent for the present symbol: %v (its candles never ingested)", err)
	}
	_ = got
}

// TestIntegration_RunDevFixtureIngest_NonNotFoundErrorStillFails is
// the fix's own other half: only the specific "instrument not found"
// case is swallowed. A genuinely failed ingest (here: a context
// already canceled before the instrument lookup's own query runs,
// so the database driver returns a context error, never
// sql.ErrNoRows/instruments.ErrNotFound) must still return an error
// and must not be mistaken for the skip case.
func TestIntegration_RunDevFixtureIngest_NonNotFoundErrorStillFails(t *testing.T) {
	db := testWorkerDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	symbol := fmt.Sprintf("CANCELED-%s-%d", t.Name(), time.Now().UnixNano())
	instrumentStore := instruments.NewStore(db)
	seedDevFixtureInstrument(t, db, instrumentStore, symbol) // a real row exists; the lookup itself will still fail, below

	data := fixture.Data{Candles: map[string][]ingestion.ProviderCandle{
		symbol: candleSeries(symbol, 3, 100),
	}}

	marketdataStore := marketdata.NewStore(db, clock.Real{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled before runDevFixtureIngest ever runs a query

	err := runDevFixtureIngest(ctx, logger, data, instrumentStore, marketdataStore, nil)
	if err == nil {
		t.Fatal("runDevFixtureIngest with an already-canceled context = nil, want an error")
	}
	if errors.Is(err, instruments.ErrNotFound) {
		t.Fatalf("runDevFixtureIngest error = %v, want something other than instruments.ErrNotFound (a context cancellation, not a missing row)", err)
	}
}

// TestIntegration_RunDevFixtureIngest_RealDatasetStillIngestsSolPerpWhenRowExists
// uses the real, checked-in dev dataset (fixture.DevDataset, the exact
// file the regression report named) rather than a synthetic one: with
// only SOL-PERP's own instrument row seeded (BTC-PERP/ETH-PERP
// deliberately not, reproducing a plain dev_fixture_instruments.sql-only
// database missing db/seed/e2e_only_instruments.sql's own row the
// OTHER way around), SOL-PERP's real candles from the real dataset
// still ingest.
func TestIntegration_RunDevFixtureIngest_RealDatasetStillIngestsSolPerpWhenRowExists(t *testing.T) {
	db := testWorkerDB(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	loaded, err := fixture.DevDataset()
	if err != nil {
		t.Fatalf("DevDataset: %v", err)
	}
	solCandles := loaded.Candles["SOL-PERP"]
	if len(solCandles) == 0 {
		t.Fatal("dev dataset has no SOL-PERP candles -- test no longer matches the real regression's own fixture")
	}
	btcCandles := loaded.Candles["BTC-PERP"]
	if len(btcCandles) == 0 {
		t.Fatal("dev dataset has no BTC-PERP candles")
	}

	// Re-keyed to per-test-run unique symbols -- the dataset's literal
	// "SOL-PERP"/"BTC-PERP" keys are only meant to match the real
	// deployment's own seeded rows (same collision-avoidance every
	// other integration test in this repo already needs, see
	// TestIntegration_Pipeline_DevFixtureIngestThroughSetupsLifecycle's
	// own doc comment).
	solSymbol := fmt.Sprintf("SOL-PERP-%s-%d", t.Name(), time.Now().UnixNano())
	btcSymbol := fmt.Sprintf("BTC-PERP-%s-%d", t.Name(), time.Now().UnixNano())
	solCandlesCopy := append([]ingestion.ProviderCandle(nil), solCandles...)
	for i := range solCandlesCopy {
		solCandlesCopy[i].Symbol = solSymbol
	}
	btcCandlesCopy := append([]ingestion.ProviderCandle(nil), btcCandles...)
	for i := range btcCandlesCopy {
		btcCandlesCopy[i].Symbol = btcSymbol
	}

	instrumentStore := instruments.NewStore(db)
	solID := seedDevFixtureInstrument(t, db, instrumentStore, solSymbol)
	// btcSymbol deliberately gets no instrument row -- this run's own
	// database stands in for "db/seed/e2e_only_instruments.sql ran,
	// dev_fixture_instruments.sql did not" rather than the regression's
	// own direction, proving the fix is symmetric: whichever dataset
	// symbol lacks a row is skipped, whichever has one still ingests.

	data := fixture.Data{Candles: map[string][]ingestion.ProviderCandle{
		solSymbol: solCandlesCopy,
		btcSymbol: btcCandlesCopy,
	}}

	marketdataStore := marketdata.NewStore(db, clock.Real{})
	if err := runDevFixtureIngest(ctx, logger, data, instrumentStore, marketdataStore, nil); err != nil {
		t.Fatalf("runDevFixtureIngest = %v, want nil", err)
	}

	got, err := marketdataStore.GetCurrent(ctx, solID, clock.NewEventTime(solCandlesCopy[0].OpenTime))
	if err != nil {
		t.Fatalf("GetCurrent for SOL-PERP's own real dataset candles: %v", err)
	}
	_ = got
}
