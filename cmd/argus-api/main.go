// Command argus-api runs the REST and (later) WebSocket API server.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/httpapi"
	"github.com/chibuike-kt/argus/internal/identity"
	"github.com/chibuike-kt/argus/internal/identity/mailer"
	"github.com/chibuike-kt/argus/internal/identity/password"
	"github.com/chibuike-kt/argus/internal/identity/ratelimit"
	"github.com/chibuike-kt/argus/internal/identity/service"
	"github.com/chibuike-kt/argus/internal/identity/session"
	"github.com/chibuike-kt/argus/internal/identity/tokens"
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

	clk := clock.Real{}
	users := identity.NewStore(db)
	sessions := session.NewStore(db, clk, cfg.Auth.SessionIdleDuration, cfg.Auth.SessionAbsoluteDuration)
	tokenStore := tokens.NewStore(db, clk)
	hasher := password.NewHasher(password.Params{
		MemoryKiB:   cfg.Auth.ArgonMemoryKiB,
		Iterations:  cfg.Auth.ArgonIterations,
		Parallelism: cfg.Auth.ArgonParallelism,
		SaltLen:     cfg.Auth.ArgonSaltLen,
		KeyLen:      cfg.Auth.ArgonKeyLen,
	}, cfg.Auth.ArgonMaxConcurrent)
	limiter := ratelimit.NewLimiter(rdb, logger, clk)

	// No mail provider is chosen yet (out of scope for this brief) --
	// LogMailer is the only Mailer, and it refuses to construct in
	// production so a real deployment cannot silently "send" email by
	// writing verification/reset links to its own logs.
	mail, err := mailer.NewLogMailer(logger, cfg.IsProduction())
	if err != nil {
		logger.Error("startup: mailer unavailable", "error", err)
		os.Exit(1)
	}

	authSvc := service.New(db, users, sessions, tokenStore, hasher, limiter, mail, clk, cfg.Auth, cfg.PublicBaseURL, logger)

	handler := httpapi.NewRouter(httpapi.Deps{
		Logger:       logger,
		DB:           db,
		Redis:        rdb,
		Auth:         authSvc,
		Sessions:     sessions,
		Users:        users,
		AuthPolicy:   cfg.Auth,
		IsProduction: cfg.IsProduction(),
		Clock:        clk,
	})
	srv := httpapi.NewServer(":"+cfg.HTTPPort, handler, cfg.HTTPReadTimeout)

	go func() {
		logger.Info("api listening", "addr", srv.Addr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown started")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("shutdown complete")
}
