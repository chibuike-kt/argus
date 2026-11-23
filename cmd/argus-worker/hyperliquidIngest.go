package main

import (
	"context"
	"database/sql"
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
	"github.com/chibuike-kt/argus/internal/tracking"
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
	db *sql.DB,
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
	// The whole perpetual catalog, once at startup: its size decimals feed
	// the stream's price-format check (set before any stream starts --
	// SetSzDecimals is not safe during one, and 4.1a does not change the
	// adapter), and its rows are registered, disabled (D15).
	trackStore := tracking.NewStore(db)
	if all, err := provider.Instruments(ctx); err != nil {
		logger.Warn("hyperliquid: could not fetch instruments for price-format checking or the catalog; streamed prices will not be checked against Hyperliquid's own precision rule this run", "error", err)
	} else {
		szDecimals := make(map[string]int32, len(all))
		for _, pi := range all {
			szDecimals[pi.Symbol] = pi.QtyDecimalPlaces // QtyDecimalPlaces is szDecimals itself, unmodified -- see Instruments' own doc comment
		}
		provider.SetSzDecimals(szDecimals)
		if res, err := refreshCatalog(ctx, all, instrumentStore, trackStore, clk, logger); err != nil {
			logger.Error("worker: catalog refresh failed at startup", "error", err)
		} else {
			logger.Info("worker: catalog refreshed", "listed", res.Listed, "created_disabled", res.Created, "needs_attention", res.Attention)
		}
	}
	cache := ingestion.NewInstrumentCache(instrumentStore)
	normalizer := ingestion.NewNormalizer(cache, clk)
	svc := ingestion.NewService(hyperliquid.ProviderName, provider, normalizer, cache, marketdataStore, marketdata.ContinuousCalendar{}, logger)
	svc.SetPublisher(publisher)

	logger.Warn("worker: ARGUS_INGEST_PROVIDER=hyperliquid is enabled -- ingesting real Hyperliquid data for private research use only (ADR-007 rights not yet confirmed); no instrument this creates is ever enabled by this process",
		"symbols", cfg.Hyperliquid.Symbols, "backfill_days", cfg.Hyperliquid.BackfillDays)

	state := storeBackfillState{instruments: instrumentStore, candles: marketdataStore}
	go runCatalogRefresh(ctx, provider, instrumentStore, trackStore, cfg.Hyperliquid.CatalogRefreshInterval, clk, logger)
	sup := newSupervisor(cfg.Hyperliquid.Symbols, cfg.Hyperliquid.MaxTrackedSymbols, cfg.Hyperliquid.RemovalGrace, cfg.Hyperliquid.BackfillConcurrency,
		trackStore, func(ctx context.Context, symbol string, hooks symbolHooks) {
			runHyperliquidSymbolWith(ctx, symbol, svc, state, cfg, logger, clk, hooks)
		}, clk, logger)
	go sup.run(ctx, cfg.Hyperliquid.SupervisorInterval)
}

// startupBackfillOptions shape a symbol's startup history backfill: a
// backfill slot is held per attempt only (never across the retry delay),
// and the retry delay grows to cfg.Hyperliquid.NewSymbolBackfillRetryCap
// when the pipeline is supervised (hooks carry the gate).
func startupBackfillOptions(cfg config.Config, hooks symbolHooks) backfillOptions {
	opt := backfillOptions{
		onRetry: func(attempt int, err error) {
			hooks.set(tracking.Retrying, "history backfill failed: "+err.Error(), attempt)
		},
		onSlot: func(attempt int) {
			if attempt == 0 {
				hooks.set(tracking.LoadingHistory, "", 0)
			}
		},
		slot: hooks.gate,
	}
	if hooks.gate != nil {
		opt.retryCap = cfg.Hyperliquid.NewSymbolBackfillRetryCap
	}
	return opt
}

// symbolHooks connect one symbol pipeline to the supervisor (Phase 4.1a,
// D15): gate queues the history backfill of new symbols (D15.4: at most
// BackfillConcurrency at once), report records the pipeline's state for
// the watchlist row. Both optional.
type symbolHooks struct {
	gate   chan struct{}
	report func(state tracking.State, reason string, attempt int)
}

func (h symbolHooks) set(state tracking.State, reason string, attempt int) {
	if h.report != nil {
		h.report(state, reason, attempt)
	}
}

// runHyperliquidSymbolWith closes any hole since the newest stored candle
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
//
// hooks report its state to the supervisor and queue its history backfill
// (symbolHooks; the zero value reports nothing and does not queue).
func runHyperliquidSymbolWith(ctx context.Context, symbol string, svc *ingestion.Service, state integrityState, cfg config.Config, logger *slog.Logger, clk clock.Clock, hooks symbolHooks) {
	if hooks.gate != nil {
		hooks.set(tracking.Waiting, "queued for the history backfill", 0)
	}
	ok := backfillUntilCaughtUpWith(ctx, "startup", symbol, svc, state, cfg, logger, clk, startupBackfillOptions(cfg, hooks))
	if !ok {
		if ctx.Err() == nil {
			hooks.set(tracking.Failed, "no instrument row for this symbol", 0)
		}
		return
	}
	// The backfill only reaches back to the newest stored candle; holes
	// already inside the stored series are found and repaired here.
	integrityScan(ctx, "startup", symbol, svc, state, cfg, logger, clk)

	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}
		hooks.set(tracking.Live, "", 0)

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
		scanDone := make(chan struct{})
		go func() {
			defer close(scanDone)
			runPeriodicIntegrityScan(periodicCtx, symbol, svc, state, cfg, logger, clk)
		}()

		err := svc.WatchCandles(ctx, symbol)
		cancelPeriodic()
		<-periodicDone
		<-scanDone
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logger.Error("hyperliquid: stream ended, will reconnect", "symbol", symbol, "error", err)
			hooks.set(tracking.Retrying, "live stream ended: "+err.Error(), attempt+1)
		} else {
			logger.Info("hyperliquid: stream ended cleanly, will reconnect", "symbol", symbol)
		}

		// Unconditional, planned disconnect or not: StreamCandles'
		// contract never guarantees the last in-flight minute was
		// closed before the connection ended, and after a long outage
		// this is what fills the hole.
		if !backfillUntilCaughtUp(ctx, "reconnect", symbol, svc, state, cfg, logger, clk, func(n int, err error) {
			hooks.set(tracking.Retrying, "backfill after reconnect failed: "+err.Error(), n)
		}) {
			return
		}
		integrityScan(ctx, "reconnect", symbol, svc, state, cfg, logger, clk)

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
