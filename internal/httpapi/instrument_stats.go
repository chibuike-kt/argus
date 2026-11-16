package httpapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
)

// maxInstrumentStatsIDs is GET /instruments/stats' own cap (real-panel-
// data brief), matching the OpenAPI spec's maxItems and the watchlist's
// own 50-instrument maximum, so one request covers a whole watchlist.
const maxInstrumentStatsIDs = 50

// instrumentStatsStore is the boundary apiHandler needs from
// marketdata.Store for GET /instruments/stats -- consumer-defined
// (guide 4), the same pattern as candlesStore, so a handler test can
// use a handwritten fake.
type instrumentStatsStore interface {
	Stats(ctx context.Context, instrumentID int64) (marketdata.Stats, error)
}

var _ instrumentStatsStore = (*marketdata.Store)(nil)

// ListInstrumentStats serves GET /instruments/stats. Every figure is
// computed server-side by marketdata.Store.Stats with exact scaled-
// integer arithmetic; this handler only renders the results as decimal
// strings at the instrument's own scales.
//
// An unknown or disabled id is omitted, never an error (the response
// must not reveal which ids exist). Cost: per distinct id, one
// instrument lookup and one stats statement (O(log n + 1440) each, see
// GetCandleStatsInputs), sequential -- at most 2*50 short queries.
func (h *apiHandler) ListInstrumentStats(ctx context.Context, request ListInstrumentStatsRequestObject) (ListInstrumentStatsResponseObject, error) {
	ids := request.Params.Ids
	if len(ids) == 0 {
		return ListInstrumentStats400JSONResponse(apiError("invalid_argument", "ids must list at least one instrument id")), nil
	}

	seen := make(map[int64]struct{}, len(ids))
	unique := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) > maxInstrumentStatsIDs {
		return ListInstrumentStats400JSONResponse(apiError("invalid_argument", fmt.Sprintf("ids must list at most %d instruments", maxInstrumentStatsIDs))), nil
	}

	out := make(ListInstrumentStats200JSONResponse, 0, len(unique))
	for _, id := range unique {
		inst, err := h.instruments.GetByID(ctx, id)
		if errors.Is(err, instruments.ErrNotFound) {
			continue
		}
		if err != nil {
			h.logger.Error("httpapi: get instrument for stats", "instrument_id", id, "error", err)
			return nil, fmt.Errorf("get instrument %d: %w", id, err)
		}
		if !inst.Enabled {
			continue
		}

		st, err := h.stats.Stats(ctx, id)
		if err != nil {
			h.logger.Error("httpapi: instrument stats", "instrument_id", id, "error", err)
			return nil, fmt.Errorf("stats for instrument %d: %w", id, err)
		}
		out = append(out, toAPIInstrumentStats(st, inst.PriceScale, inst.QtyScale))
	}
	return out, nil
}

// toAPIInstrumentStats renders st at the instrument's own scales. A nil
// domain value becomes a JSON null, never "0".
func toAPIInstrumentStats(st marketdata.Stats, priceScale, qtyScale quant.Scale) InstrumentStats {
	out := InstrumentStats{InstrumentId: st.InstrumentID}
	if st.Latest != nil {
		lc := st.Latest.Close.String(priceScale)
		out.LastClose = &lc
		t := st.Latest.OpenTime.Time().UTC()
		out.LastClosedOpenTime = &t
	}
	if st.Change24h != nil {
		abs := st.Change24h.Abs.String(priceScale)
		out.Change24h = &abs
		out.Change24hPct = percentPtr(st.Change24h.Pct)
	}
	if st.Volume24h != nil {
		v := st.Volume24h.String(qtyScale)
		out.Volume24h = &v
	}
	out.Performance = InstrumentPerformance{
		N1D: percentPtr(st.Performance["1D"]),
		N1W: percentPtr(st.Performance["1W"]),
		N1M: percentPtr(st.Performance["1M"]),
		N3M: percentPtr(st.Performance["3M"]),
		N6M: percentPtr(st.Performance["6M"]),
		N1Y: percentPtr(st.Performance["1Y"]),
	}
	return out
}

func percentPtr(p *marketdata.Percent) *string {
	if p == nil {
		return nil
	}
	s := p.String()
	return &s
}
