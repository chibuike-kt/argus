package marketdata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/store"
)

// BidAsk is the latest observed bid/ask for an instrument (ADR-006: latest
// quote only, no historical quote retention or tick-level replay). Bid and
// Ask are scaled by the owning instrument's PriceScale.
type BidAsk struct {
	InstrumentID int64
	Bid          quant.Price
	Ask          quant.Price
	QuoteTime    clock.EventTime
	IngestedAt   clock.IngestionTime
	Provider     string
}

// IsCrossed reports whether Ask is below Bid. Real markets can produce a
// transient crossed quote (across venues, or during extreme volatility);
// this is a data-quality signal for the caller to act on, not something
// the store rejects or hides (approved decision).
func (q BidAsk) IsCrossed() bool { return q.Ask < q.Bid }

func fromQuoteRow(row store.LatestQuote) BidAsk {
	return BidAsk{
		InstrumentID: row.InstrumentID,
		Bid:          quant.Price(row.Bid),
		Ask:          quant.Price(row.Ask),
		QuoteTime:    clock.NewEventTime(row.QuoteTime),
		IngestedAt:   clock.NewIngestionTime(row.IngestedAt),
		Provider:     row.Provider,
	}
}

// UpsertQuote records the latest observed bid/ask for an instrument. An
// incoming quote older than the one already stored is rejected as
// ErrStaleQuote rather than silently overwriting newer data (guide 16: an
// out-of-order upstream message must not silently become the current
// value) -- the caller decides what, if anything, that implies about the
// feed, this method only refuses to regress stored state.
func (s *Store) UpsertQuote(ctx context.Context, q BidAsk) (BidAsk, error) {
	if q.InstrumentID == 0 {
		return BidAsk{}, ErrInstrumentIDRequired
	}
	if q.Provider == "" {
		return BidAsk{}, ErrProviderRequired
	}
	if q.Bid < 0 || q.Ask < 0 {
		return BidAsk{}, fmt.Errorf("marketdata: upsert quote instrument=%d: bid and ask must not be negative", q.InstrumentID)
	}

	row, err := s.q.UpsertLatestQuote(ctx, store.UpsertLatestQuoteParams{
		InstrumentID: q.InstrumentID,
		Bid:          int64(q.Bid),
		Ask:          int64(q.Ask),
		QuoteTime:    q.QuoteTime.Time(),
		IngestedAt:   q.IngestedAt.Time(),
		Provider:     q.Provider,
	})
	if err == nil {
		return fromQuoteRow(row), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return BidAsk{}, fmt.Errorf("marketdata: upsert quote instrument=%d: %w", q.InstrumentID, err)
	}
	// Zero rows: the conflict's DO UPDATE WHERE clause rejected the write
	// because the incoming quote_time is older than what is stored.
	return BidAsk{}, fmt.Errorf("marketdata: upsert quote instrument=%d quote_time=%s: %w", q.InstrumentID, q.QuoteTime, ErrStaleQuote)
}

// GetQuote returns the latest quote for instrumentID.
func (s *Store) GetQuote(ctx context.Context, instrumentID int64) (BidAsk, error) {
	row, err := s.q.GetLatestQuote(ctx, instrumentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BidAsk{}, fmt.Errorf("marketdata: get quote instrument=%d: %w", instrumentID, ErrQuoteNotFound)
		}
		return BidAsk{}, fmt.Errorf("marketdata: get quote instrument=%d: %w", instrumentID, err)
	}
	return fromQuoteRow(row), nil
}
