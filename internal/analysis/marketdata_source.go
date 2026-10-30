package analysis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/marketdata"
)

// marketdataSource is the thin adapter satisfying CandleSource over the
// real candle store (ADR-003: analysis depends on marketdata, never the
// reverse). It is the only place in this package that knows
// marketdata.Candle or marketdata.Store exist.
type marketdataSource struct {
	store *marketdata.Store
}

// NewMarketdataSource wraps store as a CandleSource.
func NewMarketdataSource(store *marketdata.Store) CandleSource {
	return &marketdataSource{store: store}
}

func (m *marketdataSource) ListCandles(ctx context.Context, instrumentID int64, start, end time.Time) ([]CandleInput, error) {
	rows, err := m.store.ListCurrentCandlesInRange(ctx, instrumentID, start, end)
	if err != nil {
		return nil, fmt.Errorf("analysis: list candles instrument=%d [%s,%s): %w", instrumentID, start, end, err)
	}
	out := make([]CandleInput, len(rows))
	for i, row := range rows {
		out[i] = CandleInput{
			ID:        row.ID,
			OpenTime:  row.OpenTime,
			CloseTime: row.CloseTime,
			Open:      row.Open,
			High:      row.High,
			Low:       row.Low,
			Close:     row.Close,
			Revision:  row.Revision,
			IsClosed:  row.IsClosed,
		}
	}
	return out, nil
}

func (m *marketdataSource) LatestClosed(ctx context.Context, instrumentID int64) (CandleInput, error) {
	c, err := m.store.GetLatestClosed(ctx, instrumentID)
	if err != nil {
		if errors.Is(err, marketdata.ErrCandleNotFound) {
			return CandleInput{}, fmt.Errorf("analysis: latest closed candle instrument=%d: %w", instrumentID, ErrNoClosedCandles)
		}
		return CandleInput{}, fmt.Errorf("analysis: latest closed candle instrument=%d: %w", instrumentID, err)
	}
	return CandleInput{
		ID:        c.ID,
		OpenTime:  c.OpenTime.Time(),
		CloseTime: c.CloseTime.Time(),
		Open:      int64(c.Open),
		High:      int64(c.High),
		Low:       int64(c.Low),
		Close:     int64(c.Close),
		Revision:  c.Revision,
		IsClosed:  c.IsClosed,
	}, nil
}
