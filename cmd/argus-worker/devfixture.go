package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/hub"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/ingestion/fixture"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

// devFixtureProviderName must match the provider column
// db/seed/dev_fixture_instruments.sql seeds its rows with: it is how
// ingestion.InstrumentCache resolves a symbol in the dataset back to
// an instrument row.
const devFixtureProviderName = "fixture"

// runDevFixtureIngest replays data (the checked-in dev/test dataset in
// production, see main's own call site) into whatever instruments
// db/seed/dev_fixture_instruments.sql seeded (matched by provider +
// native_symbol), populating the candles table once before the
// processing loop's first tick. DEV/TEST ONLY -- gated by
// cfg.DevFixtureIngest in main, which must never be true in a real
// deployment (see config.Config's own doc comment on that field; this
// function does not re-check it, the caller already did).
//
// data is a parameter, not loaded internally from devDatasetPath,
// specifically so this package's own integration test can inject a
// dataset whose candles are keyed under a per-test-run unique symbol
// (the same collision-avoidance every other integration test in this
// repo already needs) rather than the dataset file's fixed "BTC-PERP"/
// "ETH-PERP" keys, which only the real dev/test deployment's seeded
// rows are meant to match.
//
// A background WatchCandles stream per instrument was considered and
// left out: fixture.Provider's StreamCandles replays the exact same
// recorded sequence this already ingests historically, so running it
// too would only re-apply already-closed candles with identical
// values -- a harmless no-op duplicate redelivery (ADR-005's own
// idempotency contract), not a way to show anything IngestHistorical
// has not already shown. One-time historical ingest is enough to
// prove the wiring; a later brief can add a live stream if an ongoing
// feed is genuinely needed to demonstrate something this does not.
func runDevFixtureIngest(ctx context.Context, logger *slog.Logger, data fixture.Data, instrumentStore *instruments.Store, marketdataStore *marketdata.Store, publisher hub.Publisher) error {
	cache := ingestion.NewInstrumentCache(instrumentStore)
	normalizer := ingestion.NewNormalizer(cache, clock.Real{})
	provider := fixture.New(data, 0)
	svc := ingestion.NewService(devFixtureProviderName, provider, normalizer, cache, marketdataStore, marketdata.ContinuousCalendar{}, logger)
	svc.SetPublisher(publisher)

	for symbol, candles := range data.Candles {
		if len(candles) == 0 {
			continue
		}
		start := candles[0].OpenTime
		end := candles[len(candles)-1].OpenTime.Add(time.Minute)
		if err := svc.IngestHistorical(ctx, symbol, start, end); err != nil {
			return fmt.Errorf("ingest historical %s: %w", symbol, err)
		}
		logger.Info("dev fixture: ingested historical candles", "symbol", symbol, "count", len(candles))
	}
	return nil
}
