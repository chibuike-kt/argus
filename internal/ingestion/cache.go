package ingestion

import (
	"context"
	"fmt"
	"sync"

	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/quant"
)

// instrumentLookup is the minimal boundary InstrumentCache needs to fill
// a miss. instruments.Store satisfies it; tests use a handwritten fake
// (guide 4, 10: an interface defined by the consumer, kept small).
type instrumentLookup interface {
	GetByProviderAndSymbol(ctx context.Context, provider, symbol string) (instruments.Instrument, error)
}

// InstrumentFacts are the handful of per-instrument facts normalization
// needs on every candle or quote.
type InstrumentFacts struct {
	ID          int64
	AssetClass  string
	PriceScale  quant.Scale
	QtyScale    quant.Scale
	TickSize    quant.Price // scaled by PriceScale
	QtyStepSize quant.Qty   // scaled by QtyScale
}

type instrumentKey struct {
	Provider string
	Symbol   string
}

// InstrumentCache caches InstrumentFacts by (provider, symbol) so
// validating one candle or quote never costs a database round trip
// (guide 9: avoid a per-item query for a fact that does not change
// between items). Safe for concurrent use -- concurrent per-instrument
// streams read and populate it at the same time.
//
// Its fields do not all have the same staleness story, and this type
// does not pretend otherwise:
//
//   - PriceScale and QtyScale need no invalidation at all, and that is a
//     direct consequence of a schema decision, not an assumption this
//     package makes on its own: once an instrument exists, those two
//     columns are structurally immutable
//     (000002_instrument_sizing_and_scale.up.sql's
//     trg_instruments_prevent_scale_update rejects any update to them,
//     unconditionally). A cached value for either can never go stale.
//   - AssetClass, TickSize and QtyStepSize carry no such guarantee.
//     Nothing in this repository updates them today -- instruments.Store
//     has no Update method -- so a stale cached value for one of them is
//     currently unreachable in practice, not just unlikely. But that is
//     a latent property of the current code, not a property of the
//     schema, so it is not treated as "safe forever": Invalidate exists
//     specifically so that whoever adds an update path for one of these
//     three fields has an answer ready, rather than discovering the
//     cache-invalidation gap the hard way.
//
// A lookup miss is never itself cached -- the registry gains instruments
// over time, so every miss re-queries rather than remembering a false
// negative.
type InstrumentCache struct {
	lookup instrumentLookup

	mu    sync.RWMutex
	byKey map[instrumentKey]InstrumentFacts
}

// NewInstrumentCache wraps lookup (typically instruments.Store).
func NewInstrumentCache(lookup instrumentLookup) *InstrumentCache {
	return &InstrumentCache{
		lookup: lookup,
		byKey:  make(map[instrumentKey]InstrumentFacts),
	}
}

// Get returns the cached facts for (provider, symbol), querying lookup
// and populating the cache on a miss.
func (c *InstrumentCache) Get(ctx context.Context, provider, symbol string) (InstrumentFacts, error) {
	key := instrumentKey{Provider: provider, Symbol: symbol}

	c.mu.RLock()
	facts, ok := c.byKey[key]
	c.mu.RUnlock()
	if ok {
		return facts, nil
	}

	inst, err := c.lookup.GetByProviderAndSymbol(ctx, provider, symbol)
	if err != nil {
		return InstrumentFacts{}, fmt.Errorf("ingestion: look up instrument %s/%s: %w", provider, symbol, err)
	}
	facts = InstrumentFacts{
		ID:          inst.ID,
		AssetClass:  inst.AssetClass,
		PriceScale:  inst.PriceScale,
		QtyScale:    inst.QtyScale,
		TickSize:    inst.TickSize,
		QtyStepSize: inst.QtyStepSize,
	}

	c.mu.Lock()
	c.byKey[key] = facts
	c.mu.Unlock()

	return facts, nil
}

// Invalidate removes any cached entry for (provider, symbol), forcing
// the next Get to re-query. Call this after updating an instrument's
// AssetClass, TickSize or QtyStepSize -- see the type doc for why those
// three, specifically, need it and PriceScale/QtyScale never will. No
// code path updates any of them today, so this method is currently
// unreachable from the rest of this codebase; it exists so that gap has
// an answer already.
func (c *InstrumentCache) Invalidate(provider, symbol string) {
	c.mu.Lock()
	delete(c.byKey, instrumentKey{Provider: provider, Symbol: symbol})
	c.mu.Unlock()
}
