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
	// Loud-warning duty for these two overrides lives in main.go,
	// unconditionally (not gated on IngestProvider=="hyperliquid") --
	// see its own comment on why.
	opts := []hyperliquid.Option{
		hyperliquid.WithMaxConnectionAge(cfg.Hyperliquid.MaxConnectionAge),
		hyperliquid.WithHeartbeat(cfg.Hyperliquid.PingInterval, cfg.Hyperliquid.StallTimeout),
	}
	if cfg.Hyperliquid.BaseURLOverride != "" {
		opts = append(opts, hyperliquid.WithBaseURL(cfg.Hyperliquid.BaseURLOverride))
	}
	if cfg.Hyperliquid.WSURLOverride != "" {
		opts = append(opts, hyperliquid.WithWSURL(cfg.Hyperliquid.WSURLOverride))
	}
	provider := hyperliquid.NewProvider(clk, logger, opts...)

	// Price-format checking (brief-hyperliquid-adapter.md's own real-
	// run requirement: every streamed price checked against the 5-
	// significant-figure/decimal rule) is opt-in and needs szDecimals
	// per symbol -- fetched once here, from this same Provider's own
	// Instruments, rather than StreamCandles ever fetching it itself.
	// A failure here is logged and skipped, never fatal to startup:
	// this check is observational, not load-bearing.
	if all, err := provider.Instruments(ctx); err != nil {
		logger.Warn("hyperliquid: could not fetch instruments for price-format checking; streamed prices will not be checked against Hyperliquid's own precision rule this run", "error", err)
	} else {
		szDecimals := make(map[string]int32, len(all))
		for _, pi := range all {
			szDecimals[pi.Symbol] = pi.QtyDecimalPlaces // QtyDecimalPlaces is szDecimals itself, unmodified -- see Instruments' own doc comment
		}
		provider.SetSzDecimals(szDecimals)
	}

	cache := ingestion.NewInstrumentCache(instrumentStore)
	normalizer := ingestion.NewNormalizer(cache, clk)
	svc := ingestion.NewService(hyperliquid.ProviderName, provider, normalizer, cache, marketdataStore, marketdata.ContinuousCalendar{}, logger)
	svc.SetPublisher(publisher)

	logger.Warn("worker: ARGUS_INGEST_PROVIDER=hyperliquid is enabled -- ingesting real Hyperliquid data for private research use only (ADR-007 rights not yet confirmed); no instrument this creates is ever enabled by this process",
		"symbols", cfg.Hyperliquid.Symbols, "backfill_days", cfg.Hyperliquid.BackfillDays)

	state := storeBackfillState{instruments: instrumentStore, candles: marketdataStore}
	for _, symbol := range cfg.Hyperliquid.Symbols {
		go runHyperliquidSymbol(ctx, symbol, svc, state, cfg, logger, clk)
	}
}

// runHyperliquidSymbol closes any hole since the newest stored candle
// (backfillUntilCaughtUp: window read from the database, retried until
// it succeeds), then alternates between streaming live candles and --
// on any disconnect, planned, failed or stalled -- the same database-
// anchored backfill before reconnecting with exponential backoff
// (config.LiveFeed.ReconnectBackoffBase/Cap).
//
// The previous version anchored the post-disconnect backfill on the
// connection's own start time held in memory, made one attempt, and
// reset that anchor to "now" on the next loop: after hours offline
// (machine asleep, network down) the failed attempt's window was never
// retried and the hole stayed forever. The startup backfill had the
// same one-shot shape.
func runHyperliquidSymbol(ctx context.Context, symbol string, svc *ingestion.Service, state backfillState, cfg config.Config, logger *slog.Logger, clk clock.Clock) {
	if !backfillUntilCaughtUp(ctx, "startup", symbol, svc, state, cfg, logger, clk) {
		return
	}

	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}

		// Covers the real-run gap this brief's own verification
		// found: the stream's in-memory tracked-minute starts empty
		// on every fresh connection, so a minute the backfill above
		// left dangling is never closed by the stream itself if the
		// connection's first live message already belongs to a later
		// minute. Runs only while WatchCandles is actually in flight
		// -- canceled the instant it returns, so it is never what
		// delays a prompt reconnect.
		periodicCtx, cancelPeriodic := context.WithCancel(ctx)
		periodicDone := make(chan struct{})
		go func() {
			defer close(periodicDone)
			runPeriodicGapBackfill(periodicCtx, symbol, svc, cfg.Hyperliquid.PeriodicBackfillInterval, cfg.Hyperliquid.PeriodicBackfillWindow, logger, clk)
		}()

		err := svc.WatchCandles(ctx, symbol)
		cancelPeriodic()
		<-periodicDone
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logger.Error("hyperliquid: stream ended, will reconnect", "symbol", symbol, "error", err)
		} else {
			logger.Info("hyperliquid: stream ended cleanly, will reconnect", "symbol", symbol)
		}

		// Unconditional, planned disconnect or not: StreamCandles'
		// contract never guarantees the last in-flight minute was
		// closed before the connection ended, and after a long outage
		// this is what fills the hole.
		if !backfillUntilCaughtUp(ctx, "reconnect", symbol, svc, state, cfg, logger, clk) {
			return
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

// runPeriodicGapBackfill calls IngestHistorical on a fixed interval,
// over a short trailing window, for as long as ctx stays alive --
// the caller cancels ctx the instant its own StreamCandles call
// returns, so this never outlives (or delays) a reconnect. Every
// call it makes goes through the SAME IngestHistorical/applyCandle
// path the startup and gap backfills already use, so its own
// "every candle except the last fetched one is final" proof and
// idempotent-correction behavior (unchanged value -> no new
// revision, changed value -> the normal correction path) are not
// reimplemented here, only invoked again on a timer.
//
// A failed call (the exchange unreachable mid an otherwise-healthy
// stream) is logged and left for the next tick -- the interval
// itself is this method's only backoff; it never retries sooner.
//
// interval <= 0 disables this entirely (returns immediately) --
// never a time.NewTicker panic. config.Load's own envDefault always
// supplies a positive value in a real deployment; a test building a
// bare config.Config{} literal without setting
// Hyperliquid.PeriodicBackfillInterval gets the feature turned off,
// not a crash.
func runPeriodicGapBackfill(ctx context.Context, symbol string, svc *ingestion.Service, interval, window time.Duration, logger *slog.Logger, clk clock.Clock) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := clk.Now()
			since := now.Add(-window)
			if err := svc.IngestHistorical(ctx, symbol, since, now); err != nil && ctx.Err() == nil {
				logger.Warn("hyperliquid: periodic gap backfill failed, will retry next interval", "symbol", symbol, "error", err)
			}
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
