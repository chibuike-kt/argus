package main

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/hub"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/ingestion/hyperliquid"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

// startHyperliquidIngest is ARGUS_INGEST_PROVIDER=hyperliquid's own
// entry point (brief-hyperliquid-adapter.md): one goroutine per
// configured symbol, each running its own backfill-then-stream-then-
// reconnect loop against the real Hyperliquid adapter. Runs until ctx
// is canceled (worker shutdown).
//
// Reconnect policy lives here, not in the hyperliquid package itself
// (ingestion.Provider's own documented contract: "reconnect policy,
// backoff and alerting are the caller's decision, never this
// method's") -- the same split startDevFixtureLiveReplay already uses
// for the dev fixture's own goroutine-per-symbol wiring, except the
// dev fixture never needs to reconnect at all (it runs in-process and
// never errors), so this is new orchestration, not a copy of
// anything that already existed.
func startHyperliquidIngest(
	ctx context.Context,
	cfg config.Config,
	logger *slog.Logger,
	instrumentStore *instruments.Store,
	marketdataStore *marketdata.Store,
	publisher hub.Publisher,
	clk clock.Clock,
) {
	provider := hyperliquid.NewProvider(clk, logger)
	cache := ingestion.NewInstrumentCache(instrumentStore)
	normalizer := ingestion.NewNormalizer(cache, clk)
	svc := ingestion.NewService(hyperliquid.ProviderName, provider, normalizer, cache, marketdataStore, marketdata.ContinuousCalendar{}, logger)
	svc.SetPublisher(publisher)

	logger.Warn("worker: ARGUS_INGEST_PROVIDER=hyperliquid is enabled -- ingesting real Hyperliquid data for private research use only (ADR-007 rights not yet confirmed); no instrument this creates is ever enabled by this process",
		"symbols", cfg.Hyperliquid.Symbols, "backfill_days", cfg.Hyperliquid.BackfillDays)

	for _, symbol := range cfg.Hyperliquid.Symbols {
		go runHyperliquidSymbol(ctx, symbol, svc, cfg, logger, clk)
	}
}

// runHyperliquidSymbol backfills cfg.Hyperliquid.BackfillDays once,
// then alternates between streaming live candles and -- on any
// disconnect, planned or not -- backfilling exactly the gap since this
// connection attempt started before reconnecting with exponential
// backoff (config.LiveFeed.ReconnectBackoffBase/Cap, reused rather
// than duplicated, per the brief's own instruction). A planned
// reconnect (the brief's own hostile-condition test, simulated with a
// shortened connection lifetime) loses no candle for exactly this
// reason: the gap backfill after StreamCandles returns is unconditional,
// whether the disconnect was planned or a real failure.
func runHyperliquidSymbol(ctx context.Context, symbol string, svc *ingestion.Service, cfg config.Config, logger *slog.Logger, clk clock.Clock) {
	now := clk.Now()
	start := now.Add(-time.Duration(cfg.Hyperliquid.BackfillDays) * 24 * time.Hour)
	if err := svc.IngestHistorical(ctx, symbol, start, now); err != nil {
		if ctx.Err() != nil {
			return
		}
		logger.Error("hyperliquid: initial backfill failed", "symbol", symbol, "error", err)
	}

	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}

		since := clk.Now()
		err := svc.WatchCandles(ctx, symbol)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logger.Error("hyperliquid: stream ended, will reconnect", "symbol", symbol, "error", err)
		} else {
			logger.Info("hyperliquid: stream ended cleanly, will reconnect", "symbol", symbol)
		}

		// The gap backfill: whatever this connection missed between
		// its own start and now, regardless of whether it ended with
		// an error or returned nil -- a planned reconnect (no error)
		// still needs this, since StreamCandles' own contract never
		// guarantees the LAST in-flight minute was closed before the
		// connection ended (see hyperliquid.Provider.StreamCandles'
		// own doc comment on this exact scenario).
		if err := svc.IngestHistorical(ctx, symbol, since, clk.Now()); err != nil && ctx.Err() == nil {
			logger.Error("hyperliquid: gap backfill after disconnect failed", "symbol", symbol, "error", err)
		}

		delay := reconnectBackoff(attempt, cfg.Live.ReconnectBackoffBase, cfg.Live.ReconnectBackoffCap)
		attempt++
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// reconnectBackoff is exponential backoff with full jitter (delay
// chosen uniformly in [0, min(cap, base*2^attempt)]) -- the same
// algorithm live-socket.ts already uses client-side for the identical
// config fields, reused here rather than invented separately.
func reconnectBackoff(attempt int, base, capDuration time.Duration) time.Duration {
	exp := float64(base) * float64(uint64(1)<<min(attempt, 32))
	d := min(exp, float64(capDuration))
	return time.Duration(rand.Float64() * d) //nolint:gosec // reconnect backoff jitter, never security-sensitive
}
