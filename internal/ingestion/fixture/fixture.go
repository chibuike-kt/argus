// Package fixture implements ingestion.Provider by replaying a recorded
// dataset instead of talking to a real exchange or broker. ADR-007
// (provider selection and commercial data rights) is still open, so this
// is what development and tests run against: dev work, the ingestion
// service's integration tests, and ADR-019 point-in-time replay all use
// the same Provider interface a real Binance or OANDA adapter will
// implement later.
package fixture

import (
	"context"
	"sort"
	"time"

	"github.com/chibuike-kt/argus/internal/ingestion"
)

// Data is a recorded dataset for one provider: every candle and quote
// Provider can replay, grouped by symbol.
type Data struct {
	Instruments []ingestion.ProviderInstrument
	// Candles maps symbol to its recorded 1-minute candles. New sorts a
	// copy by OpenTime ascending; the caller does not need to pre-sort.
	Candles map[string][]ingestion.ProviderCandle
	// Quotes maps symbol to its recorded quotes, sorted by New the same
	// way as Candles.
	Quotes map[string][]ingestion.ProviderQuote
}

// Provider implements ingestion.Provider by replaying a Data set.
//
// StreamCandles and StreamQuotes replay their symbol's recorded sequence
// in order, waiting Pace between each call to handle (0 means no delay,
// the right choice for most tests). Once the recorded sequence is
// exhausted, both methods block on ctx.Done() rather than returning --
// matching the real Provider contract exactly, where running out of new
// data right now is never itself a reason for a live stream to end.
// Tests that want to stop a stream deliberately cancel ctx (typically
// via context.WithTimeout, or by canceling once they have observed the
// store state they expected) rather than relying on the fixture to end
// on its own.
type Provider struct {
	data Data
	pace time.Duration
}

// New copies data (sorting each symbol's candles and quotes by time
// ascending) and returns a Provider that replays it with pace between
// each streamed event.
func New(data Data, pace time.Duration) *Provider {
	candles := make(map[string][]ingestion.ProviderCandle, len(data.Candles))
	for symbol, cs := range data.Candles {
		sorted := make([]ingestion.ProviderCandle, len(cs))
		copy(sorted, cs)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].OpenTime.Before(sorted[j].OpenTime) })
		candles[symbol] = sorted
	}

	quotes := make(map[string][]ingestion.ProviderQuote, len(data.Quotes))
	for symbol, qs := range data.Quotes {
		sorted := make([]ingestion.ProviderQuote, len(qs))
		copy(sorted, qs)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].QuoteTime.Before(sorted[j].QuoteTime) })
		quotes[symbol] = sorted
	}

	instruments := make([]ingestion.ProviderInstrument, len(data.Instruments))
	copy(instruments, data.Instruments)

	return &Provider{
		data: Data{Instruments: instruments, Candles: candles, Quotes: quotes},
		pace: pace,
	}
}

// Instruments returns the fixture's recorded instrument metadata.
func (p *Provider) Instruments(_ context.Context) ([]ingestion.ProviderInstrument, error) {
	out := make([]ingestion.ProviderInstrument, len(p.data.Instruments))
	copy(out, p.data.Instruments)
	return out, nil
}

// HistoricalCandles returns the recorded candles for symbol with
// OpenTime in [start, end). It never synthesizes a candle for a missing
// minute -- a deliberately absent minute in the fixture is exactly how
// tests exercise gap detection.
func (p *Provider) HistoricalCandles(_ context.Context, symbol string, start, end time.Time) ([]ingestion.ProviderCandle, error) {
	var out []ingestion.ProviderCandle
	for _, c := range p.data.Candles[symbol] {
		if !c.OpenTime.Before(start) && c.OpenTime.Before(end) {
			out = append(out, c)
		}
	}
	return out, nil
}

// StreamCandles replays symbol's recorded candles in order, then blocks
// on ctx.Done(). See the Provider doc comment for the full contract.
func (p *Provider) StreamCandles(ctx context.Context, symbol string, handle func(ctx context.Context, c ingestion.ProviderCandle) error) error {
	for _, c := range p.data.Candles[symbol] {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if err := handle(ctx, c); err != nil {
			return err
		}

		if p.pace > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(p.pace):
			}
		}
	}

	<-ctx.Done()
	return nil
}

// StreamQuotes is StreamCandles' counterpart for quotes.
func (p *Provider) StreamQuotes(ctx context.Context, symbol string, handle func(ctx context.Context, q ingestion.ProviderQuote) error) error {
	for _, q := range p.data.Quotes[symbol] {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if err := handle(ctx, q); err != nil {
			return err
		}

		if p.pace > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(p.pace):
			}
		}
	}

	<-ctx.Done()
	return nil
}

var _ ingestion.Provider = (*Provider)(nil)
