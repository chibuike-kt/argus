// Package ingestion is a feature module boundary (see ADR-003):
// market-data ingestion and normalization, behind a provider-agnostic
// port (ADR-006, ADR-007).
//
// ADR-007 (provider selection and commercial data rights) is still open,
// so this package never depends on a real provider client. Development
// and tests run against a fixture/replay Provider until that resolves;
// a real Binance or OANDA adapter implements the same Provider interface
// later without this package changing.
package ingestion

import (
	"context"
	"time"
)

// Provider is the provider-agnostic adapter boundary for market-data
// ingestion. Every value it returns is raw and untrusted: numeric fields
// are decimal strings, never float64 and never a quant type.
//
// That is deliberate, not an oversight. A float64 can already have lost
// precision before Normalizer gets a chance to accept or reject a value
// -- putting quant.Price on this interface would mean something upstream
// of this package already parsed and rounded the number, defeating the
// entire point of a validating boundary (guide 1, 13). Normalizer is
// the first and only place any numeric value from a provider is parsed.
type Provider interface {
	// Instruments returns the provider's current instrument metadata.
	// Returning an instrument here does not register or enable it --
	// a symbol existing at a provider does not imply enablement
	// (ADR-006, ADR-007); that decision happens elsewhere, deliberately
	// outside this package.
	Instruments(ctx context.Context) ([]ProviderInstrument, error)

	// HistoricalCandles returns 1-minute candles for symbol with
	// open_time in [start, end), oldest first. An implementation returns
	// as many candles as the provider actually has in range; returning
	// fewer than the full range covers is not itself an error here --
	// the caller compares the result against the market calendar and its
	// own gap-detection logic, not against a count this method reports.
	HistoricalCandles(ctx context.Context, symbol string, start, end time.Time) ([]ProviderCandle, error)

	// StreamCandles blocks, calling handle once per live 1-minute candle
	// update (forming or closing) for symbol, in arrival order, until one
	// of three things happens:
	//   - ctx is canceled: StreamCandles returns nil.
	//   - handle returns a non-nil error: StreamCandles stops and returns
	//     that error UNCHANGED (an implementation must not wrap it), so a
	//     caller's errors.Is/As against its own handler's sentinel errors
	//     works without the implementation's wrapping getting in the way.
	//   - the provider itself has an unrecoverable error (a dropped
	//     connection it cannot retry, a fatal protocol error, ...):
	//     StreamCandles returns that error. Reconnect policy, backoff and
	//     alerting are the caller's decision, never this method's.
	// handle is called synchronously on the implementation's own
	// goroutine and must not block significantly; it also receives ctx
	// (not necessarily identical to the ctx StreamCandles was called
	// with -- an implementation may derive a per-message context) so it
	// can thread cancellation into whatever it does with the candle.
	//
	// A channel-pair API (a data channel plus an error channel) was
	// considered and rejected for this port specifically: which side
	// owns closing those channels, and when, differs subtly across a
	// WebSocket-driven adapter, a polling adapter, and a replay fixture
	// -- exactly the kind of detail this port needs to get right once,
	// for every future implementation, not leave to each adapter's
	// judgment (guide 7: channels coordinate work, they are not a way to
	// call a function). A blocking call with a callback has exactly one
	// completion signal (the method returns) and exactly one way for the
	// caller to stop it early (return an error from handle).
	StreamCandles(ctx context.Context, symbol string, handle func(ctx context.Context, c ProviderCandle) error) error

	// StreamQuotes is StreamCandles' counterpart for latest bid/ask
	// updates. Same blocking, same callback, same three-way completion
	// contract.
	StreamQuotes(ctx context.Context, symbol string, handle func(ctx context.Context, q ProviderQuote) error) error
}

// ProviderInstrument is a provider's raw, as-reported instrument
// metadata. Precision is carried as a decimal string or a provider-
// reported decimal-place count, never a parsed quant.Scale: deciding
// this instrument's price_scale and qty_scale, and validating that the
// provider's own figures are consistent with that choice, belongs to the
// instrument registry (ADR-006), not this port.
type ProviderInstrument struct {
	Symbol             string
	AssetClass         string
	BaseAsset          string
	QuoteAsset         string
	ContractType       string
	PriceDecimalPlaces int32
	QtyDecimalPlaces   int32
	TickSize           string // decimal string, e.g. "0.01"
	QtyStepSize        string // decimal string
	ContractSize       string // decimal string; "" when not applicable
	PipSize            string // decimal string; "" for non-forex instruments
	SupportedIntervals []string
}

// ProviderCandle is one 1-minute candle as reported by the provider.
// OHLCV are decimal strings -- see Provider's doc comment for why.
type ProviderCandle struct {
	Symbol   string
	OpenTime time.Time // provider-reported bar open; Normalizer requires this to be minute-aligned UTC
	Open     string
	High     string
	Low      string
	Close    string
	Volume   string
	// IsFinal is the provider's own claim that this candle will not
	// change further. Normalizer passes it through unconditionally --
	// deciding whether and when to act on that claim (call Close, wait
	// for corroborating evidence, cross-check against expected timing)
	// belongs to the ingestion service that consumes Normalizer's
	// output, not to this boundary.
	IsFinal bool
}

// ProviderQuote is one latest-bid/ask observation as reported by the
// provider.
type ProviderQuote struct {
	Symbol    string
	Bid       string
	Ask       string
	QuoteTime time.Time
}
