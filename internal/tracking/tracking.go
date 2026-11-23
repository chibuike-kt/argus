// Package tracking is a feature module boundary (see ADR-003): which
// Hyperliquid instruments ARGUS ingests and analyses, their per symbol
// pipeline state, and the catalog of instruments that can be added
// (Phase 4.1a of the product overhaul brief, decision D15).
//
// The tracked set is the configured floor symbols plus every enabled
// Hyperliquid instrument on at least one watchlist (D15.2), in cap order:
// the floor first, then by when it was first put on any watchlist. The
// worker tracks at most the system cap of them (D15.3); the rest wait.
package tracking

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/ingestion/hyperliquid"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/store"
)

// State is a tracked symbol's pipeline state, shown on its watchlist row.
type State string

// The states (tracked_symbol_states.state).
const (
	// Waiting: queued for the history backfill (D15.4: at most 2 at once),
	// or over the system cap.
	Waiting State = "waiting"
	// LoadingHistory: the history backfill is running.
	LoadingHistory State = "loading_history"
	// Live: the history is caught up and the live stream is running.
	Live State = "live"
	// Retrying: a backfill or the stream failed; the worker is retrying.
	Retrying State = "retrying"
	// Failed: cannot be ingested without an operator (no instrument row,
	// delisted by the provider).
	Failed State = "failed"
	// NeedsAttention: the provider changed the symbol's price precision
	// (D15.5); ingestion stopped, nothing was rescaled.
	NeedsAttention State = "needs_attention"
	// Stopped: no longer tracked; the supervisor ended its pipeline after
	// the removal grace period.
	Stopped State = "stopped"
)

// Status is one symbol's state as last written by the worker.
type Status struct {
	InstrumentID int64
	State        State
	Reason       string
	Attempt      int
	UpdatedAt    time.Time
}

// Tracked is one instrument of the tracked set.
type Tracked struct {
	ID      int64
	Symbol  string
	IsFloor bool
}

// Store reads the tracked set and reads and writes symbol states.
type Store struct {
	q *store.Queries
}

// NewStore wraps db (a *sql.DB or a *sql.Tx).
func NewStore(db store.DBTX) *Store {
	return &Store{q: store.New(db)}
}

// Tracked returns the tracked set in cap order (the floor first, then by
// when it was first watched).
func (s *Store) Tracked(ctx context.Context, floor []string) ([]Tracked, error) {
	if floor == nil {
		floor = []string{}
	}
	rows, err := s.q.ListTrackedInstruments(ctx, floor)
	if err != nil {
		return nil, fmt.Errorf("tracking: list tracked: %w", err)
	}
	out := make([]Tracked, len(rows))
	for i, r := range rows {
		out[i] = Tracked{ID: r.ID, Symbol: r.NativeSymbol, IsFloor: r.IsFloor}
	}
	return out, nil
}

// SetState records a symbol's state (in place; a projection, not history).
func (s *Store) SetState(ctx context.Context, instrumentID int64, state State, reason string, attempt int, at time.Time) error {
	if attempt < 0 {
		attempt = 0
	}
	err := s.q.UpsertTrackedSymbolState(ctx, store.UpsertTrackedSymbolStateParams{
		InstrumentID: instrumentID, State: string(state), Reason: reason,
		Attempt:   int32(min(attempt, 1<<30)), //nolint:gosec // bounded just above
		UpdatedAt: at.UTC(),
	})
	if err != nil {
		return fmt.Errorf("tracking: set state %s for instrument %d: %w", state, instrumentID, err)
	}
	return nil
}

// States returns the recorded state of each of ids that has one.
func (s *Store) States(ctx context.Context, ids []int64) (map[int64]Status, error) {
	out := make(map[int64]Status, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q.ListTrackedSymbolStates(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("tracking: list states: %w", err)
	}
	for _, r := range rows {
		out[r.InstrumentID] = Status{InstrumentID: r.InstrumentID, State: State(r.State), Reason: r.Reason, Attempt: int(r.Attempt), UpdatedAt: r.UpdatedAt.UTC()}
	}
	return out, nil
}

// ErrNoAttention is returned when the symbol is not marked needs_attention
// (or is not a registered Hyperliquid instrument).
var ErrNoAttention = errors.New("tracking: symbol is not marked needs attention")

// ClearNeedsAttention removes symbol's needs_attention state and returns
// the state it had; ErrNoAttention when there was none. The supervisor
// then starts the symbol again on its next pass if it is tracked.
func (s *Store) ClearNeedsAttention(ctx context.Context, symbol string) (Status, error) {
	r, err := s.q.ClearNeedsAttention(ctx, symbol)
	if errors.Is(err, sql.ErrNoRows) {
		return Status{}, ErrNoAttention
	}
	if err != nil {
		return Status{}, fmt.Errorf("tracking: clear needs attention for %s: %w", symbol, err)
	}
	return Status{InstrumentID: r.InstrumentID, State: NeedsAttention, Reason: r.Reason, UpdatedAt: r.UpdatedAt.UTC()}, nil
}

// HyperliquidInstruments returns every registered Hyperliquid instrument,
// enabled or not (the catalog as stored).
func (s *Store) HyperliquidInstruments(ctx context.Context) ([]store.Instrument, error) {
	rows, err := s.q.ListHyperliquidInstruments(ctx)
	if err != nil {
		return nil, fmt.Errorf("tracking: list hyperliquid instruments: %w", err)
	}
	return rows, nil
}

// CatalogParams converts one perpetual from Hyperliquid's meta response
// into the (always disabled) instrument row to register for it. Shared by
// the worker's catalog refresh and argus-instruments sync-hyperliquid.
func CatalogParams(pi ingestion.ProviderInstrument) (instruments.NewInstrumentParams, error) {
	priceScale := quant.Scale(pi.PriceDecimalPlaces)
	qtyScale := quant.Scale(pi.QtyDecimalPlaces)
	tick, err := quant.ParsePrice(pi.TickSize, priceScale)
	if err != nil {
		return instruments.NewInstrumentParams{}, fmt.Errorf("%s: parse tick size %q at scale %d: %w", pi.Symbol, pi.TickSize, priceScale, err)
	}
	step, err := quant.ParseQty(pi.QtyStepSize, qtyScale)
	if err != nil {
		return instruments.NewInstrumentParams{}, fmt.Errorf("%s: parse qty step %q at scale %d: %w", pi.Symbol, pi.QtyStepSize, qtyScale, err)
	}
	base, quote, contract := pi.BaseAsset, pi.QuoteAsset, pi.ContractType
	return instruments.NewInstrumentParams{
		Provider:           hyperliquid.ProviderName,
		NativeSymbol:       pi.Symbol,
		AssetClass:         pi.AssetClass,
		BaseAsset:          &base,
		QuoteAsset:         &quote,
		ContractType:       &contract,
		PriceScale:         &priceScale,
		QtyScale:           &qtyScale,
		TickSize:           tick,
		QtyStepSize:        step,
		SupportedIntervals: pi.SupportedIntervals,
	}, nil
}

// PrecisionChange says whether the provider's current precision for a
// symbol differs from the scales stored for it (D15.5), and how. A
// changed precision is never rescaled: stored prices are integers at the
// stored scale, and reading new values at a different scale would corrupt
// every comparison with the history.
func PrecisionChange(storedPriceScale, storedQtyScale int32, pi ingestion.ProviderInstrument) (changed bool, reason string) {
	if storedPriceScale == pi.PriceDecimalPlaces && storedQtyScale == pi.QtyDecimalPlaces {
		return false, ""
	}
	return true, fmt.Sprintf("price precision changed: stored %d price and %d size decimals, Hyperliquid now reports %d and %d; ingestion stopped, nothing was rescaled",
		storedPriceScale, storedQtyScale, pi.PriceDecimalPlaces, pi.QtyDecimalPlaces)
}
