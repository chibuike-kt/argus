// Command argus-worker runs background processing: market-data ingestion,
// analysis and reevaluation, and historical backfill (ADR-003, ADR-005).
//
// The processing loop (analysis.Engine plus setups.Reconcile, driven per
// enabled instrument) is real as of batch 7; the queue consumer market-data
// ingestion itself drives through is not wired here yet -- see processing.go
// and this change's own report for what that means in practice today. It
// connects to the datastores, logs, and shuts down cleanly on signal.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/ingestion/fixture"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/platform/logging"
	"github.com/chibuike-kt/argus/internal/platform/postgres"
	"github.com/chibuike-kt/argus/internal/platform/redis"
	"github.com/chibuike-kt/argus/internal/setups"
)

// tickInterval is how often the worker polls for newly closed candles.
// It is a polling cadence, not the candle interval itself (always 1
// minute today, see processing.go's timeframe constant) -- polling
// faster than candles can actually close would just repeat "nothing
// new" ticks, so this is set to the candle interval, not below it.
const tickInterval = time.Minute

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	logger := logging.New(cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("startup: postgres unavailable", "error", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	rdb, err := redis.Open(ctx, cfg.RedisURL)
	if err != nil {
		logger.Error("startup: redis unavailable", "error", err)
		os.Exit(1)
	}
	defer func() { _ = rdb.Close() }()

	clk := clock.Real{}
	instrumentStore := instruments.NewStore(db)
	marketdataStore := marketdata.NewStore(db, clk)
	candleSource := analysis.NewMarketdataSource(marketdataStore)
	analysisStore := analysis.NewStore(db)
	setupsStore := setups.NewStore(db)

	engineCfg := analysis.Config{
		AlgorithmVersion: algorithmVersion,
		LeftBars:         2,
		RightBars:        2,
		Interval:         time.Minute,
	}
	engine, err := analysis.NewEngine(db, candleSource, analysisStore, clk, engineCfg)
	if err != nil {
		logger.Error("startup: invalid analysis config", "error", err)
		os.Exit(1)
	}

	proc := newProcessor(db, logger, clk, instrumentStore, candleSource, engine, analysisStore, setupsStore)

	if cfg.DevFixtureIngest {
		logger.Warn("worker: ARGUS_DEV_FIXTURE_INGEST is enabled -- replaying the checked-in dev dataset, never do this against a production database")
		devData, err := fixture.DevDataset()
		if err != nil {
			logger.Error("startup: load dev dataset", "error", err)
			os.Exit(1)
		}
		if err := runDevFixtureIngest(ctx, logger, devData, instrumentStore, marketdataStore); err != nil {
			logger.Error("startup: dev fixture ingest failed", "error", err)
			os.Exit(1)
		}
	}

	logger.Info("worker started", "env", cfg.Env)

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("worker shutdown complete")
			return
		case <-ticker.C:
			proc.run(ctx)
		}
	}
}
