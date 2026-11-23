package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/user"

	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/platform/postgres"
	"github.com/chibuike-kt/argus/internal/tracking"
)

// clear-attention (architect review of 4.1a, item 3): removes the
// needs_attention state of one Hyperliquid symbol (D15.5: a precision
// change or a delisting stopped its ingestion), so the worker starts it
// again if it is still tracked. It is the operator's decision: the command
// logs who ran it, on which host, what the state said, and why it was
// allowed to run (the same non-production guard as sync-hyperliquid).
//
// It does not change the stored price or quantity scale. If the precision
// really changed, fix the instrument first; otherwise the next catalog
// refresh marks the symbol again.

type attentionClearer interface {
	ClearNeedsAttention(ctx context.Context, symbol string) (tracking.Status, error)
}

func runClearAttention(ctx context.Context, args []string) error {
	if len(args) != 1 || args[0] == "" {
		return errors.New("usage: argus-instruments clear-attention SYMBOL")
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := clearAttentionAllowed(cfg); err != nil {
		return err
	}
	db, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()
	return clearAttention(ctx, tracking.NewStore(db), args[0], cfg.Env, operator(), slog.Default())
}

// clearAttentionAllowed is the guard: never against production.
func clearAttentionAllowed(cfg config.Config) error {
	if cfg.IsProduction() {
		return fmt.Errorf("refusing to run clear-attention against ARGUS_ENV=%q: resuming a symbol whose precision changed is not this command's to do in production", cfg.Env)
	}
	return nil
}

func clearAttention(ctx context.Context, st attentionClearer, symbol, env, who string, logger *slog.Logger) error {
	prev, err := st.ClearNeedsAttention(ctx, symbol)
	if errors.Is(err, tracking.ErrNoAttention) {
		return fmt.Errorf("%s is not marked needs attention (or is not a registered Hyperliquid symbol); nothing changed", symbol)
	}
	if err != nil {
		return err
	}
	logger.Warn("argus-instruments: cleared needs attention; the worker starts this symbol again if it is tracked",
		"symbol", symbol, "instrument_id", prev.InstrumentID,
		"was", prev.Reason, "marked_at", prev.UpdatedAt,
		"run_by", who, "environment", env,
		"allowed_because", fmt.Sprintf("ARGUS_ENV=%q is not production (the guard sync-hyperliquid uses) and the operator holds the database credentials", env))
	return nil
}

// operator names who ran the command: the OS user and the host.
func operator() string {
	name := "unknown"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	} else if v := os.Getenv("USER"); v != "" {
		name = v
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return name + "@" + host
}
