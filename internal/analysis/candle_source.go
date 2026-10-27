package analysis

import (
	"context"
	"time"
)

// CandleInput is the minimal 1-minute candle data the structure engine
// needs. It is deliberately not marketdata.Candle or store.Candle: every
// comparison the engine makes is within one instrument at one scale, so
// bare scaled int64 is correct and avoids quant.Price wrapping on a path
// that never needs it (the engine only ever asks "is this value greater
// than that one", never "what does this value mean in absolute terms").
type CandleInput struct {
	ID        int64
	OpenTime  time.Time
	CloseTime time.Time
	High      int64
	Low       int64
	Close     int64
	// IsClosed lets the engine stop at the live edge rather than assume
	// a range query returned only closed candles. Spec section 4: BOS,
	// CHOCH and pivot confirmation all require a closed candle; a
	// forming one is never used for any of them (no look-ahead, guide
	// 16).
	IsClosed bool
}

// CandleSource is the consumer-defined boundary the structure engine
// needs from the candle store (ADR-003: analysis depends on marketdata,
// never the reverse). A thin adapter over marketdata.Store satisfies
// it; tests use a handwritten fake (guide 4, 10).
//
// There is no timeframe parameter here, unlike on Engine's own methods:
// there is exactly one persisted candle series (the canonical 1-minute
// one), so the adapter never needs to choose between sources. Engine
// still carries timeframe explicitly through its own API and storage,
// because confirmed_swings/structure_events/structure_state are keyed by
// it -- see the package doc for why that is a named seam, not yet a
// usable one.
type CandleSource interface {
	// ListCandles returns candles for instrumentID with open_time in
	// [start, end), ordered ascending. It does not filter by IsClosed;
	// the engine decides where to stop.
	ListCandles(ctx context.Context, instrumentID int64, start, end time.Time) ([]CandleInput, error)

	// LatestClosed returns the most recent closed candle for
	// instrumentID. Used to bound a correction's recompute replay at
	// "now" without the caller having to know what "now" means in
	// candle terms.
	LatestClosed(ctx context.Context, instrumentID int64) (CandleInput, error)
}
