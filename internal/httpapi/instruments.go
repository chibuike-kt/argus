package httpapi

import (
	"context"
	"errors"
	"fmt"
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

func (h *apiHandler) ListInstrumentCandles(ctx context.Context, request ListInstrumentCandlesRequestObject) (ListInstrumentCandlesResponseObject, error) {
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

	// from/to are optional in the OpenAPI schema (web-wiring-polish
	// brief, item 1) so order=desc can mean "the latest N, no range
	// known or needed". asc (the default) keeps the original contract:
	// both are required, with the same message oapi-codegen's own
	// router-level "required" validation used to produce back when
	// they were required:true in the spec -- that validation no longer
	// fires for either direction, so this reproduces it here, for asc
	// only.
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

	out := make(ListInstrumentCandles200JSONResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAPICandle(row, inst.PriceScale, inst.QtyScale))
	}
	return out, nil
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
