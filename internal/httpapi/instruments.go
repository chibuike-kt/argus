package httpapi

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
)

// maxInstrumentCandles is ListInstrumentCandles' hard cap (worker-
// hardening brief, Part C): a client cannot request the whole table
// in one call regardless of how wide [from, to) is. Matches the
// OpenAPI spec's own documented maximum.
const maxInstrumentCandles = 1000

// defaultInstrumentCandles is the limit applied when the caller omits
// the query parameter entirely.
const defaultInstrumentCandles = 500

// defaultInstrumentsLimit/maxInstrumentsLimit are ListInstruments' own
// caps (backend-guards-cleanup brief: the one list endpoint with no
// cap), matching the OpenAPI spec's documented default/maximum.
const defaultInstrumentsLimit = 200
const maxInstrumentsLimit = 500

// instrumentsStore is the boundary apiHandler needs from
// instruments.Store -- kept small and consumer-defined (guide 4), the
// same reason setupsStore exists, so the handler tests below can use
// a handwritten fake instead of a real database.
type instrumentsStore interface {
	ListAllFiltered(ctx context.Context, afterID *int64, enabled *bool, limit int32) ([]instruments.Instrument, error)
	GetByID(ctx context.Context, id int64) (instruments.Instrument, error)
}

var _ instrumentsStore = (*instruments.Store)(nil)

// candlesStore is the boundary apiHandler needs from marketdata.Store
// for the candles read path -- the same consumer-defined-interface
// pattern as instrumentsStore/setupsStore.
type candlesStore interface {
	ListClosedInRangeLimited(ctx context.Context, instrumentID int64, start, end time.Time, limit int32) ([]marketdata.Candle, error)
	ListClosedInRangeLimitedDesc(ctx context.Context, instrumentID int64, start, end *time.Time, limit int32) ([]marketdata.Candle, error)
	// AggregateRange and LatestCompleteWindow back the interval>1m path
	// and the last_closed_* join-point fields (live-chart-pipeline
	// brief, Phase 1; the per-interval join point is
	// adr-004-ws-contract-review.md's own review item 5) -- both
	// expressed entirely in marketdata's own exported types, never the
	// sqlc-generated store.Candle candleReader itself uses internally,
	// so this interface never needs to import internal/store just to be
	// satisfied.
	AggregateRange(ctx context.Context, instrumentID int64, windows []marketdata.Window) ([]marketdata.AggregateOHLCV, error)
	LatestCompleteWindow(ctx context.Context, instrumentID int64, d time.Duration) (*marketdata.AggregateOHLCV, error)
}

var _ candlesStore = (*marketdata.Store)(nil)

// trueVal/falseVal back the enabled=true/false cases below -- a local
// *bool to pass through to instruments.Store.ListAllFiltered, which
// takes nil (no filter) vs a pointer (exactly this value) to mean "no
// ?enabled= query param" is a decision this handler makes, not the
// store's.
var trueVal = true
var falseVal = false

func (h *apiHandler) ListInstruments(ctx context.Context, request ListInstrumentsRequestObject) (ListInstrumentsResponseObject, error) {
	// Default is "true" (enabled only, web-wiring-polish brief, item 2),
	// not "no filter": the bug this item fixes is a disabled test/
	// fixture row filling the capped default page, so omitting the
	// parameter must not mean the endpoint's old unfiltered behaviour.
	enabled := &trueVal
	if request.Params.Enabled != nil {
		switch *request.Params.Enabled {
		case True:
			enabled = &trueVal
		case False:
			enabled = &falseVal
		case All:
			enabled = nil
		default:
			return ListInstruments400JSONResponse(apiError("invalid_argument", "enabled must be true, false, or all")), nil
		}
	}

	limit := int32(defaultInstrumentsLimit)
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	if limit < 1 || limit > maxInstrumentsLimit {
		return ListInstruments400JSONResponse(apiError("invalid_argument", fmt.Sprintf("limit must be between 1 and %d", maxInstrumentsLimit))), nil
	}

	rows, err := h.instruments.ListAllFiltered(ctx, request.Params.AfterId, enabled, limit)
	if err != nil {
		h.logger.Error("httpapi: list instruments", "error", err)
		return nil, fmt.Errorf("list instruments: %w", err)
	}
	out := make(ListInstruments200JSONResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAPIInstrument(row))
	}
	return out, nil
}

// intervalDuration maps a validated ListInstrumentCandlesParamsInterval
// to its duration. Called only after interval.Valid() has already
// confirmed membership, so the default case is unreachable in practice;
// it still returns an error rather than a zero Duration, consistent
// with never silently guessing (CLAUDE.md rule 3).
func intervalDuration(interval ListInstrumentCandlesParamsInterval) (time.Duration, error) {
	switch interval {
	case N1m:
		return time.Minute, nil
	case N5m:
		return 5 * time.Minute, nil
	case N15m:
		return 15 * time.Minute, nil
	case N1h:
		return time.Hour, nil
	case N4h:
		return 4 * time.Hour, nil
	case N1d:
		return 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("httpapi: interval %q has no known duration", interval)
	}
}

func (h *apiHandler) ListInstrumentCandles(ctx context.Context, request ListInstrumentCandlesRequestObject) (ListInstrumentCandlesResponseObject, error) {
	interval := N1m
	if request.Params.Interval != nil {
		interval = *request.Params.Interval
	}
	if !interval.Valid() {
		return ListInstrumentCandles400JSONResponse(apiError("invalid_argument", "interval must be one of 1m, 5m, 15m, 1h, 4h, 1d")), nil
	}

	desc := false
	if request.Params.Order != nil {
		switch *request.Params.Order {
		case Asc:
			desc = false
		case Desc:
			desc = true
		default:
			return ListInstrumentCandles400JSONResponse(apiError("invalid_argument", "order must be asc or desc")), nil
		}
	}

	// from/to are optional for order=desc at every interval, not just 1m
	// (addendum item C: "the chart's first load needs it" for 5m/15m/1h/
	// 4h/1d too, the same "latest N" shortcut phase 1 already gave 1m).
	// asc still requires both explicitly, at every interval -- an
	// open-ended "oldest N" with no starting point has no natural
	// meaning and is not built.
	if !desc {
		if request.Params.From == nil {
			return ListInstrumentCandles400JSONResponse(apiError("invalid_argument", "Query argument from is required, but not found")), nil
		}
		if request.Params.To == nil {
			return ListInstrumentCandles400JSONResponse(apiError("invalid_argument", "Query argument to is required, but not found")), nil
		}
	}
	if request.Params.From != nil && request.Params.To != nil && !request.Params.To.After(*request.Params.From) {
		return ListInstrumentCandles400JSONResponse(apiError("invalid_argument", "to must be after from")), nil
	}

	limit := int32(defaultInstrumentCandles)
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	if limit < 1 || limit > maxInstrumentCandles {
		return ListInstrumentCandles400JSONResponse(apiError("invalid_argument", fmt.Sprintf("limit must be between 1 and %d", maxInstrumentCandles))), nil
	}

	inst, err := h.instruments.GetByID(ctx, request.Id)
	if errors.Is(err, instruments.ErrNotFound) {
		return ListInstrumentCandles404JSONResponse(apiError("not_found", "instrument not found")), nil
	}
	if err != nil {
		h.logger.Error("httpapi: get instrument for candles", "instrument_id", request.Id, "error", err)
		return nil, fmt.Errorf("get instrument %d: %w", request.Id, err)
	}

	// The instrument's own supported_intervals is the single source of
	// truth for what this endpoint serves (owner approved 12 November
	// 2026, superseding the earlier asset_class-based gate): calendar-
	// aware forex session boundaries are still not built, so a forex
	// instrument's own row simply never lists anything above 1m --
	// there is no separate asset-class special case here to keep in
	// sync with that fact.
	if !slices.Contains(inst.SupportedIntervals, string(interval)) {
		return ListInstrumentCandles400JSONResponse(apiError("invalid_argument", fmt.Sprintf("interval %s is not in this instrument's own supported_intervals", interval))), nil
	}

	d, err := intervalDuration(interval)
	if err != nil {
		return nil, err
	}

	// The join point with the live stream, per the subscribed/requested
	// interval (adr-004-ws-contract-review.md, review item 5): the
	// latest COMPLETE window of this interval, not the latest closed
	// 1-minute candle regardless of what interval was asked for -- a 1h
	// chart's own join point is its own latest complete hour, which
	// could be well behind the latest closed minute. Also this
	// response's own anchor for "latest N" (addendum item C) when
	// from/to are both omitted, below.
	joinPoint, err := h.candles.LatestCompleteWindow(ctx, request.Id, d)
	if err != nil {
		h.logger.Error("httpapi: latest complete window", "instrument_id", request.Id, "interval", interval, "error", err)
		return nil, fmt.Errorf("latest complete window for instrument %d: %w", request.Id, err)
	}

	var apiCandles []Candle
	if interval == N1m {
		var rows []marketdata.Candle
		if desc {
			rows, err = h.candles.ListClosedInRangeLimitedDesc(ctx, request.Id, request.Params.From, request.Params.To, limit)
		} else {
			rows, err = h.candles.ListClosedInRangeLimited(ctx, request.Id, *request.Params.From, *request.Params.To, limit)
		}
		if err != nil {
			h.logger.Error("httpapi: list instrument candles", "instrument_id", request.Id, "error", err)
			return nil, fmt.Errorf("list candles for instrument %d: %w", request.Id, err)
		}
		apiCandles = make([]Candle, 0, len(rows))
		for _, row := range rows {
			apiCandles = append(apiCandles, toAPICandle(row, inst.PriceScale, inst.QtyScale))
		}
	} else {
		var windows []marketdata.Window
		switch {
		case request.Params.From != nil && request.Params.To != nil:
			windows = marketdata.WindowsInRange(*request.Params.From, *request.Params.To, d, desc, int(limit))
		case joinPoint == nil:
			// Latest-N mode (addendum item C), but no complete window of
			// this interval exists yet at all: zero candles, not an
			// error -- the same "nothing yet" shape the 1m path already
			// has for a brand new instrument.
			windows = nil
		default:
			// Latest-N mode: anchored at the same join point this
			// response reports, walking backward exactly limit windows.
			anchorTo := joinPoint.Window.End.Time()
			anchorFrom := anchorTo.Add(-time.Duration(limit) * d)
			windows = marketdata.WindowsInRange(anchorFrom, anchorTo, d, true, int(limit))
		}
		aggs, err := h.candles.AggregateRange(ctx, request.Id, windows)
		if err != nil {
			h.logger.Error("httpapi: aggregate instrument candles", "instrument_id", request.Id, "interval", interval, "error", err)
			return nil, fmt.Errorf("aggregate candles for instrument %d: %w", request.Id, err)
		}
		apiCandles = make([]Candle, 0, len(aggs))
		for _, agg := range aggs {
			apiCandles = append(apiCandles, toAPIAggregateCandle(agg, inst.PriceScale, inst.QtyScale))
		}
	}

	var lastClosedOpenTime *time.Time
	var lastClosedRevision *int32
	if joinPoint != nil {
		t := joinPoint.Window.Start.Time()
		lastClosedOpenTime = &t
		rev := joinPoint.Revision
		lastClosedRevision = &rev
	}

	return ListInstrumentCandles200JSONResponse{
		Candles:            apiCandles,
		LastClosedOpenTime: lastClosedOpenTime,
		LastClosedRevision: lastClosedRevision,
	}, nil
}

func toAPIInstrument(inst instruments.Instrument) Instrument {
	return Instrument{
		Id:                 inst.ID,
		Provider:           inst.Provider,
		NativeSymbol:       inst.NativeSymbol,
		AssetClass:         inst.AssetClass,
		BaseAsset:          inst.BaseAsset,
		QuoteAsset:         inst.QuoteAsset,
		PriceScale:         int32(inst.PriceScale),
		QtyScale:           int32(inst.QtyScale),
		TickSize:           inst.TickSize.String(inst.PriceScale),
		SupportedIntervals: inst.SupportedIntervals,
		Enabled:            inst.Enabled,
	}
}

// toAPICandle converts the domain type to its generated API shape.
// OHLC and volume are decimal strings built from the instrument's own
// price_scale/qty_scale (quant.Price/quant.Qty's own String method),
// never JSON numbers -- a float cannot carry a scaled integer's exact
// value without risking silent precision loss (guide 13).
func toAPICandle(c marketdata.Candle, priceScale, qtyScale quant.Scale) Candle {
	return Candle{
		OpenTime:  c.OpenTime.Time(),
		CloseTime: c.CloseTime.Time(),
		Open:      c.Open.String(priceScale),
		High:      c.High.String(priceScale),
		Low:       c.Low.String(priceScale),
		Close:     c.Close.String(priceScale),
		Volume:    c.Volume.String(qtyScale),
		IsClosed:  c.IsClosed,
		Revision:  c.Revision,
	}
}

// toAPIAggregateCandle converts a derived-interval aggregate (never
// persisted, ADR-006) to the same wire shape toAPICandle produces for a
// real stored row, so the client cannot tell the two apart at the
// interval boundary. Revision is marketdata.AggregateOHLCV's own derived-
// revision rule (adr-004-ws-contract-review.md, review item 4:
// 1 + sum(revision-1) over the window's own source candles), not a
// fixed 1 -- phase 1's own original choice, superseded by that review.
func toAPIAggregateCandle(agg marketdata.AggregateOHLCV, priceScale, qtyScale quant.Scale) Candle {
	return Candle{
		OpenTime:  agg.Window.Start.Time(),
		CloseTime: agg.Window.End.Time(),
		Open:      agg.Open.String(priceScale),
		High:      agg.High.String(priceScale),
		Low:       agg.Low.String(priceScale),
		Close:     agg.Close.String(priceScale),
		Volume:    agg.Volume.String(qtyScale),
		IsClosed:  true,
		Revision:  agg.Revision,
	}
}
