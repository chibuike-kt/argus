// Command argus-worker runs background processing: market-data ingestion,
// analysis and reevaluation, and historical backfill (ADR-003, ADR-005).
//
// This is a skeleton. The queue consumer and per-instrument processing loops
// are added as those modules land. It connects to the datastores, logs, and
// shuts down cleanly on signal.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/platform/logging"
	"github.com/chibuike-kt/argus/internal/platform/postgres"
	"github.com/chibuike-kt/argus/internal/platform/redis"
)

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

	logger.Info("worker started", "env", cfg.Env)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("worker shutdown complete")
			return
		case <-ticker.C:
			logger.Debug("worker heartbeat")
		}
	}
}
