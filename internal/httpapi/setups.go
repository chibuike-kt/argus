package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/setups"
)

// defaultSetupsLimit/maxSetupsLimit are ListSetups' own caps (worker-
// hardening brief, Part C), matching the OpenAPI spec's documented
// default/maximum.
const defaultSetupsLimit = 50
const maxSetupsLimit = 200

// defaultTimeframe is what listSetups uses when the caller omits the
// optional timeframe query parameter. Only "1m" is evaluable today
// (cmd/argus-worker/processing.go's own comment on why), but
// setups.Store.ListByInstrument itself takes timeframe as a required
// argument -- there is no "all timeframes" variant -- so an omitted
// query parameter has to resolve to something concrete somewhere. This
// is that resolution, kept out of the OpenAPI contract itself (which
// leaves the parameter real, per this endpoint's own brief) and out of
// setups.Store (which has no opinion on what "default" means).
const defaultTimeframe = "1m"

// setupsStore is the boundary apiHandler needs from setups.Store --
// kept small and consumer-defined (guide 4), so the found/not-found/
// empty-list routing and mapping logic below can be unit tested
// against a handwritten fake instead of a real database. *setups.Store
// itself (used in production, see server.go) is exercised by setups'
// own integration tests; this interface's job is only to let this
// package's tests avoid needing Postgres for logic that has nothing to
// do with SQL.
type setupsStore interface {
	ListByInstrument(ctx context.Context, instrumentID int64, timeframe string) ([]setups.Setup, error)
	ListByInstrumentFiltered(ctx context.Context, instrumentID int64, timeframe string, state *setups.State, limit int32) ([]setups.Setup, error)
	GetByID(ctx context.Context, id int64) (setups.Setup, error)
	ListEvents(ctx context.Context, setupID int64) ([]setups.SetupEvent, error)
}

// setupStateFromAPI maps the generated wire enum to setups' own
// exported State constants -- kept here rather than as a new exported
// parser on the setups package itself (CLAUDE.md rule 4: no exported
// API changes to a shared package without asking).
func setupStateFromAPI(s ListSetupsParamsState) (setups.State, bool) {
	switch s {
	case "detected":
		return setups.StateDetected, true
	case "actionable":
		return setups.StateActionable, true
	case "triggered":
		return setups.StateTriggered, true
	case "invalidated":
		return setups.StateInvalidated, true
	default:
		return 0, false
	}
}

var _ setupsStore = (*setups.Store)(nil)

// apiHandler implements StrictServerInterface -- every operation in
// api/openapi.yaml, not just the setups ones, since oapi-codegen's
// chi-server generates one router covering the whole spec and this is
// the first feature module to actually use that generated router
// (server.go's own comment was still just a stub). Ping is trivial and
// does not need store; it lives here rather than as a second struct so
// one generated router can be mounted with one implementation.
type apiHandler struct {
	store       setupsStore
	instruments instrumentsStore
	candles     candlesStore
	logger      *slog.Logger
}

func (h *apiHandler) Ping(_ context.Context, _ PingRequestObject) (PingResponseObject, error) {
	msg := "pong"
	return Ping200JSONResponse{Message: &msg}, nil
}

func (h *apiHandler) ListSetups(ctx context.Context, request ListSetupsRequestObject) (ListSetupsResponseObject, error) {
	if request.Params.InstrumentId <= 0 {
		return ListSetups400JSONResponse(apiError("invalid_argument", "instrument_id must be a positive integer")), nil
	}
	timeframe := defaultTimeframe
	if request.Params.Timeframe != nil && *request.Params.Timeframe != "" {
		timeframe = *request.Params.Timeframe
	}

	limit := int32(defaultSetupsLimit)
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	if limit < 1 || limit > maxSetupsLimit {
		return ListSetups400JSONResponse(apiError("invalid_argument", fmt.Sprintf("limit must be between 1 and %d", maxSetupsLimit))), nil
	}
	var state *setups.State
	if request.Params.State != nil {
		s, ok := setupStateFromAPI(*request.Params.State)
		if !ok {
			return ListSetups400JSONResponse(apiError("invalid_argument", "state must be one of detected, actionable, triggered, invalidated")), nil
		}
		state = &s
	}

	inst, err := h.instruments.GetByID(ctx, request.Params.InstrumentId)
	if errors.Is(err, instruments.ErrNotFound) {
		return ListSetups400JSONResponse(apiError("invalid_argument", "instrument not found")), nil
	}
	if err != nil {
		h.logger.Error("httpapi: get instrument for setups", "instrument_id", request.Params.InstrumentId, "error", err)
		return nil, fmt.Errorf("get instrument %d: %w", request.Params.InstrumentId, err)
	}

	rows, err := h.store.ListByInstrumentFiltered(ctx, request.Params.InstrumentId, timeframe, state, limit)
	if err != nil {
		h.logger.Error("httpapi: list setups", "instrument_id", request.Params.InstrumentId, "timeframe", timeframe, "error", err)
		return nil, fmt.Errorf("list setups: %w", err)
	}

	// inst.PriceScale is fetched once above, not once per row -- the
	// N+1 the brief's own Part 3 asks to avoid.
	out := make(ListSetups200JSONResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAPISetup(row, inst.PriceScale))
	}
	return out, nil
}

func (h *apiHandler) GetSetup(ctx context.Context, request GetSetupRequestObject) (GetSetupResponseObject, error) {
	row, err := h.store.GetByID(ctx, request.Id)
	if errors.Is(err, setups.ErrSetupNotFound) {
		return GetSetup404JSONResponse(apiError("not_found", "setup not found")), nil
	}
	if err != nil {
		h.logger.Error("httpapi: get setup", "setup_id", request.Id, "error", err)
		return nil, fmt.Errorf("get setup %d: %w", request.Id, err)
	}

	inst, err := h.instruments.GetByID(ctx, row.InstrumentID)
	if err != nil {
		h.logger.Error("httpapi: get instrument for setup", "setup_id", request.Id, "instrument_id", row.InstrumentID, "error", err)
		return nil, fmt.Errorf("get instrument %d: %w", row.InstrumentID, err)
	}

	return GetSetup200JSONResponse(toAPISetup(row, inst.PriceScale)), nil
}

func (h *apiHandler) ListSetupEvents(ctx context.Context, request ListSetupEventsRequestObject) (ListSetupEventsResponseObject, error) {
	// GetByID first: ListEvents alone cannot distinguish "setup 404
	// exists with zero events" (impossible in practice -- every setup
	// is created with a paired Detected event -- but not a case this
	// endpoint should silently also treat as "no such setup") from "no
	// such setup at all". Both return the shared 404 shape identically,
	// but checking existence explicitly is the honest way to produce
	// that 404, not inferring it from an empty list.
	setup, err := h.store.GetByID(ctx, request.Id)
	if err != nil {
		if errors.Is(err, setups.ErrSetupNotFound) {
			return ListSetupEvents404JSONResponse(apiError("not_found", "setup not found")), nil
		}
		h.logger.Error("httpapi: get setup for events", "setup_id", request.Id, "error", err)
		return nil, fmt.Errorf("get setup %d: %w", request.Id, err)
	}

	inst, err := h.instruments.GetByID(ctx, setup.InstrumentID)
	if err != nil {
		h.logger.Error("httpapi: get instrument for setup events", "setup_id", request.Id, "instrument_id", setup.InstrumentID, "error", err)
		return nil, fmt.Errorf("get instrument %d: %w", setup.InstrumentID, err)
	}

	rows, err := h.store.ListEvents(ctx, request.Id)
	if err != nil {
		h.logger.Error("httpapi: list setup events", "setup_id", request.Id, "error", err)
		return nil, fmt.Errorf("list events for setup %d: %w", request.Id, err)
	}

	out := make(ListSetupEvents200JSONResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAPISetupEvent(row, inst.PriceScale))
	}
	return out, nil
}

// toAPISetup converts the domain type to its generated API shape.
// scale is the setup's own instrument's price_scale -- plan_snapshot's
// price fields are quant.Price, which carries no scale of its own
// (internal/quant), so every caller must already know which instrument
// this setup belongs to and look its scale up once (web-wiring-followup
// brief, Part 3: avoid an N+1 by fetching it once per request, not once
// per setup, at every call site below).
func toAPISetup(s setups.Setup, scale quant.Scale) Setup {
	return Setup{
		Id:                      s.ID,
		InstrumentId:            s.InstrumentID,
		Timeframe:               s.Timeframe,
		PoiType:                 s.POIType,
		PoiSourceCandleId:       s.POISourceCandleID,
		Direction:               s.Direction,
		StrategyVersion:         s.StrategyVersion,
		State:                   SetupState(s.State.String()),
		PoiSourceCandleRevision: s.POISourceCandleRevision,
		PlanSnapshot:            toAPIPlanSnapshot(s.Plan, scale),
		LastEventId:             s.LastEventID,
		EvaluationTime:          s.EvaluationTime,
		AlgorithmVersion:        s.AlgorithmVersion,
		CreatedAt:               s.CreatedAt,
		UpdatedAt:               s.UpdatedAt,
	}
}

func toAPISetupEvent(e setups.SetupEvent, scale quant.Scale) SetupEvent {
	return SetupEvent{
		Id:                      e.ID,
		SetupId:                 e.SetupID,
		EventType:               SetupEventEventType(e.Type.String()),
		PlanSnapshot:            toAPIPlanSnapshot(e.Plan, scale),
		PoiSourceCandleId:       e.POISourceCandleID,
		PoiSourceCandleRevision: e.POISourceCandleRevision,
		EvaluationTime:          e.EvaluationTime,
		AlgorithmVersion:        e.AlgorithmVersion,
		CreatedAt:               e.CreatedAt,
	}
}

// toAPIPlanSnapshot builds the typed wire shape field by field (not a
// JSON round-trip: the Go and wire shapes now genuinely differ --
// entry/stop/targets[].level/poi.low/poi.high are quant.Price on the
// domain side and decimal strings on the wire, exactly like every other
// money field this API sends (candle OHLCV, instrument tick_size).
// RR stays an exact rational string (big.Rat.RatString(), set by
// setups.newPlanSnapshot) -- untouched here, never reformatted.
func toAPIPlanSnapshot(p setups.PlanSnapshot, scale quant.Scale) PlanSnapshot {
	targets := make([]TargetSnapshot, len(p.Targets))
	for i, t := range p.Targets {
		targets[i] = TargetSnapshot{Level: t.Level.String(scale), Rr: t.RR}
	}
	return PlanSnapshot{
		Status:    p.Status,
		Direction: p.Direction,
		Entry:     p.Entry.String(scale),
		Stop:      p.Stop.String(scale),
		Targets:   targets,
		Poi: POISnapshot{
			Type:                     p.POI.Type,
			Direction:                p.POI.Direction,
			Low:                      p.POI.Low.String(scale),
			High:                     p.POI.High.String(scale),
			OrderBlockCandleId:       p.POI.OrderBlockCandleID,
			OrderBlockCandleRevision: p.POI.OrderBlockCandleRevision,
			OrderBlockOpenTime:       p.POI.OrderBlockOpenTime,
			QmrFailCandleId:          p.POI.QMRFailCandleID,
			QmrFailCandleRevision:    p.POI.QMRFailCandleRevision,
			StructuralSwingId:        p.POI.StructuralSwingID,
			InDiscount:               p.POI.InDiscount,
		},
		ProtectedSwingId: p.ProtectedSwingID,
	}
}

// apiError builds the shared Error schema's body. code/message are
// always client-facing (guide 12: internal error detail is logged at
// the call site, never placed here) -- every caller above either
// passes a message it chose itself (a validation failure) or a fixed,
// safe constant ("setup not found"), never err.Error().
func apiError(code, message string) Error {
	return Error{Error: struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		// RequestId, not RequestID: this anonymous struct's shape must
		// match the generated Error type's field exactly (openapi_gen.go,
		// excluded from lint as generated code; this literal is not).
		RequestId *string `json:"request_id,omitempty"` //nolint:revive
	}{Code: code, Message: message}}
}

// writeAPIError writes the shared Error schema directly to w -- used by
// the router- and strict-handler-level error hooks wired in server.go,
// which run outside the StrictServerInterface's own typed-response path
// (a malformed query parameter, or a handler that returned a plain
// error instead of a typed *Response).
func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiError(code, message))
}
