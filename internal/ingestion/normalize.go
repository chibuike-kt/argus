package ingestion

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/quant"
)

// NormalizedCandle is a validated, scale-converted 1-minute candle built
// from a ProviderCandle. IsFinal carries the provider's own claim about
// finality straight through: deciding what to DO with that claim (call
// Close, wait for corroborating evidence, cross-check against expected
// timing) is the ingestion service's job, not this package's -- see the
// package doc below for why that split is deliberate.
type NormalizedCandle struct {
	InstrumentID int64
	Provider     string
	OpenTime     clock.EventTime
	Open         quant.Price
	High         quant.Price
	Low          quant.Price
	Close        quant.Price
	Volume       quant.Qty
	// TickAligned is a data-quality signal, not a validation failure: it
	// records whether OHLC landed on the instrument's tick_size, the same
	// signal candles.tick_aligned stores (000003). A misaligned price is
	// still accepted and normalized -- rejecting it outright would be
	// guessing that the provider, not our tick_size, is wrong.
	TickAligned bool
	IngestedAt  clock.IngestionTime
	IsFinal     bool
}

// NormalizedQuote is a validated, scale-converted latest-quote
// observation built from a ProviderQuote.
type NormalizedQuote struct {
	InstrumentID int64
	Provider     string
	Bid          quant.Price
	Ask          quant.Price
	QuoteTime    clock.EventTime
	IngestedAt   clock.IngestionTime
}

// Normalizer validates and converts raw Provider values into the
// canonical types the store expects, enforcing the scale contract at
// this one boundary (guide 1: decode and validate once here; everything
// downstream trusts the result).
//
// It never rounds or guesses: a value with more fractional digits than
// the instrument's registered price_scale or qty_scale allows is
// rejected (ErrScaleMismatch), not silently truncated to fit. Malformed,
// negative or internally inconsistent OHLC is rejected the same way --
// reject, not quarantine, for this slice. A quarantine table (persisting
// a rejected raw message for later inspection or replay, rather than
// just logging and counting the rejection) is a real possibility and
// deliberately not built here; nothing in this design forecloses adding
// one later, but doing so now would be built against zero evidence of
// what actually needs quarantining.
type Normalizer struct {
	instruments *InstrumentCache
	clk         clock.Clock
}

// NewNormalizer wraps an InstrumentCache and an injected clock (guide 7,
// 13: business logic never calls time.Now() directly). clk.Now() becomes
// every normalized value's IngestedAt.
func NewNormalizer(instruments *InstrumentCache, clk clock.Clock) *Normalizer {
	return &Normalizer{instruments: instruments, clk: clk}
}

// NormalizeCandle validates and converts c. provider identifies which
// Provider c came from, for both the instrument lookup and the resulting
// NormalizedCandle.Provider field.
func (n *Normalizer) NormalizeCandle(ctx context.Context, provider string, c ProviderCandle) (NormalizedCandle, error) {
	facts, err := n.instruments.Get(ctx, provider, c.Symbol)
	if err != nil {
		return NormalizedCandle{}, fmt.Errorf("ingestion: normalize candle %s/%s: %w", provider, c.Symbol, err)
	}

	openTime := c.OpenTime.UTC()
	if !openTime.Equal(openTime.Truncate(time.Minute)) {
		return NormalizedCandle{}, fmt.Errorf("ingestion: normalize candle %s/%s open_time=%s: %w",
			provider, c.Symbol, openTime, ErrNotMinuteAligned)
	}

	open, err := n.parsePrice(c.Open, facts.PriceScale, "open", provider, c.Symbol)
	if err != nil {
		return NormalizedCandle{}, err
	}
	high, err := n.parsePrice(c.High, facts.PriceScale, "high", provider, c.Symbol)
	if err != nil {
		return NormalizedCandle{}, err
	}
	low, err := n.parsePrice(c.Low, facts.PriceScale, "low", provider, c.Symbol)
	if err != nil {
		return NormalizedCandle{}, err
	}
	closePrice, err := n.parsePrice(c.Close, facts.PriceScale, "close", provider, c.Symbol)
	if err != nil {
		return NormalizedCandle{}, err
	}
	volume, err := n.parseQty(c.Volume, facts.QtyScale, "volume", provider, c.Symbol)
	if err != nil {
		return NormalizedCandle{}, err
	}

	if high < low || high < open || high < closePrice || low > open || low > closePrice {
		return NormalizedCandle{}, fmt.Errorf("ingestion: normalize candle %s/%s open_time=%s: %w",
			provider, c.Symbol, openTime, ErrOHLCInconsistent)
	}

	tickAligned := open.IsMultipleOf(facts.TickSize) &&
		high.IsMultipleOf(facts.TickSize) &&
		low.IsMultipleOf(facts.TickSize) &&
		closePrice.IsMultipleOf(facts.TickSize)

	return NormalizedCandle{
		InstrumentID: facts.ID,
		Provider:     provider,
		OpenTime:     clock.NewEventTime(openTime),
		Open:         open,
		High:         high,
		Low:          low,
		Close:        closePrice,
		Volume:       volume,
		TickAligned:  tickAligned,
		IngestedAt:   clock.NewIngestionTime(n.clk.Now()),
		IsFinal:      c.IsFinal,
	}, nil
}

// NormalizeQuote validates and converts q.
func (n *Normalizer) NormalizeQuote(ctx context.Context, provider string, q ProviderQuote) (NormalizedQuote, error) {
	facts, err := n.instruments.Get(ctx, provider, q.Symbol)
	if err != nil {
		return NormalizedQuote{}, fmt.Errorf("ingestion: normalize quote %s/%s: %w", provider, q.Symbol, err)
	}

	bid, err := n.parsePrice(q.Bid, facts.PriceScale, "bid", provider, q.Symbol)
	if err != nil {
		return NormalizedQuote{}, err
	}
	ask, err := n.parsePrice(q.Ask, facts.PriceScale, "ask", provider, q.Symbol)
	if err != nil {
		return NormalizedQuote{}, err
	}

	return NormalizedQuote{
		InstrumentID: facts.ID,
		Provider:     provider,
		Bid:          bid,
		Ask:          ask,
		QuoteTime:    clock.NewEventTime(q.QuoteTime.UTC()),
		IngestedAt:   clock.NewIngestionTime(n.clk.Now()),
	}, nil
}

// parsePrice parses s at scale, distinguishing a genuine scale mismatch
// (ErrScaleMismatch, wrapping quant.ErrPrecisionLoss) from every other
// parse failure (malformed syntax, a negative value, overflow), which
// keep their own quant sentinel. The two are different problems with
// different remedies: a scale mismatch suggests this instrument's
// registered price_scale/qty_scale is wrong; a malformed or negative
// value suggests the provider (or its adapter) sent something broken.
func (n *Normalizer) parsePrice(s string, scale quant.Scale, field, provider, symbol string) (quant.Price, error) {
	p, err := quant.ParsePrice(s, scale)
	if err != nil {
		return 0, wrapParseError(err, field, provider, symbol, s, scale)
	}
	return p, nil
}

func (n *Normalizer) parseQty(s string, scale quant.Scale, field, provider, symbol string) (quant.Qty, error) {
	q, err := quant.ParseQty(s, scale)
	if err != nil {
		return 0, wrapParseError(err, field, provider, symbol, s, scale)
	}
	return q, nil
}

func wrapParseError(err error, field, provider, symbol, raw string, scale quant.Scale) error {
	if errors.Is(err, quant.ErrPrecisionLoss) {
		return fmt.Errorf("ingestion: %s/%s %s=%q at scale %d: %w: %w", provider, symbol, field, raw, scale, ErrScaleMismatch, err)
	}
	return fmt.Errorf("ingestion: %s/%s %s=%q at scale %d: %w", provider, symbol, field, raw, scale, err)
}
