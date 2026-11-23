package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/store"
	"github.com/chibuike-kt/argus/internal/tracking"
)

// The catalog refresh (Phase 4.1a, D15): every non-delisted Hyperliquid
// perpetual gets an instruments row, always disabled -- the worker never
// enables anything (D15.1); enabling is the audited watchlist add. The
// search dialog lists this catalog.
//
// It also guards D15.5: when Hyperliquid reports a different precision
// for a stored symbol than the scales stored for it, or no longer lists
// it, the symbol is marked needs_attention, which stops its ingestion.
// Nothing is ever rescaled.

type catalogProvider interface {
	Instruments(ctx context.Context) ([]ingestion.ProviderInstrument, error)
}

type catalogStore interface {
	HyperliquidInstruments(ctx context.Context) ([]store.Instrument, error)
	SetState(ctx context.Context, instrumentID int64, state tracking.State, reason string, attempt int, at time.Time) error
}

type instrumentInserter interface {
	Insert(ctx context.Context, p instruments.NewInstrumentParams) (instruments.Instrument, error)
}

// errCatalogListingTooSmall: the provider's listing was empty or under
// half of the stored catalog, so the refresh changed nothing.
var errCatalogListingTooSmall = errors.New("catalog refresh skipped: the provider listing is empty or under half of the stored catalog")

type catalogResult struct {
	Listed, Created, Attention int
}

// refreshCatalog runs one refresh. all is what the provider lists now.
func refreshCatalog(ctx context.Context, all []ingestion.ProviderInstrument, ins instrumentInserter, cs catalogStore, clk clock.Clock, logger *slog.Logger) (catalogResult, error) {
	res := catalogResult{Listed: len(all)}
	stored, err := cs.HyperliquidInstruments(ctx)
	if err != nil {
		return res, err
	}
	// A listing that is empty or under half of what is stored is far more
	// likely a provider fault (a partial or failed response) than a mass
	// delisting: change nothing and mark nothing (architect review of
	// 4.1a, item 3). The caller logs the error.
	if len(all) == 0 || 2*len(all) < len(stored) {
		return res, fmt.Errorf("%w: Hyperliquid listed %d perpetuals, %d are stored; nothing was changed and nothing marked needs attention",
			errCatalogListingTooSmall, len(all), len(stored))
	}
	bySymbol := make(map[string]store.Instrument, len(stored))
	for _, s := range stored {
		bySymbol[s.NativeSymbol] = s
	}
	listed := make(map[string]bool, len(all))
	for _, pi := range all {
		listed[pi.Symbol] = true
		if s, ok := bySymbol[pi.Symbol]; ok {
			if changed, reason := tracking.PrecisionChange(s.PriceScale, s.QtyScale, pi); changed {
				if err := cs.SetState(ctx, s.ID, tracking.NeedsAttention, reason, 0, clk.Now()); err != nil {
					return res, err
				}
				res.Attention++
				logger.Error("worker: catalog: price precision changed; ingestion of this symbol stops (D15.5)", "symbol", pi.Symbol, "reason", reason)
			}
			continue
		}
		params, err := tracking.CatalogParams(pi)
		if err != nil {
			logger.Error("worker: catalog: skipping a perpetual with unusable metadata", "symbol", pi.Symbol, "error", err)
			continue
		}
		if _, err := ins.Insert(ctx, params); err != nil && !errors.Is(err, instruments.ErrAlreadyExists) {
			return res, err
		} else if err == nil {
			res.Created++
		}
	}
	for _, s := range stored {
		if !listed[s.NativeSymbol] && s.Enabled {
			if err := cs.SetState(ctx, s.ID, tracking.NeedsAttention, "no longer listed by Hyperliquid (delisted or renamed); ingestion stopped", 0, clk.Now()); err != nil {
				return res, err
			}
			res.Attention++
			logger.Error("worker: catalog: an enabled symbol is no longer listed by Hyperliquid; ingestion stops", "symbol", s.NativeSymbol)
		}
	}
	return res, nil
}

// runCatalogRefresh refreshes every interval until ctx ends (the worker
// runs the first refresh itself at startup, before any stream starts).
func runCatalogRefresh(ctx context.Context, prov catalogProvider, ins instrumentInserter, cs catalogStore, interval time.Duration, clk clock.Clock, logger *slog.Logger) {
	once := func() {
		all, err := prov.Instruments(ctx)
		if err != nil {
			if ctx.Err() == nil {
				logger.Warn("worker: catalog refresh: could not list Hyperliquid perpetuals; will try at the next refresh", "error", err)
			}
			return
		}
		res, err := refreshCatalog(ctx, all, ins, cs, clk, logger)
		if err != nil {
			if ctx.Err() == nil {
				logger.Error("worker: catalog refresh failed", "error", err)
			}
			return
		}
		logger.Info("worker: catalog refreshed", "listed", res.Listed, "created_disabled", res.Created, "needs_attention", res.Attention)
	}
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
			once()
		}
	}
}
