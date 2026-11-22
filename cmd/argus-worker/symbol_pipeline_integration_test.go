//go:build integration

package main

import (
	"context"
	"log/slog"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/ingestion"
)

// runHyperliquidSymbol runs one symbol's pipeline with no supervisor
// (no state reports, no backfill queue): the shape the ingestion and
// backfill integration tests drive directly.
func runHyperliquidSymbol(ctx context.Context, symbol string, svc *ingestion.Service, state integrityState, cfg config.Config, logger *slog.Logger, clk clock.Clock) {
	runHyperliquidSymbolWith(ctx, symbol, svc, state, cfg, logger, clk, symbolHooks{})
}
