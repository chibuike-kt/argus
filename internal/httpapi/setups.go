package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/chibuike-kt/argus/internal/setups"
)

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
	GetByID(ctx context.Context, id int64) (setups.Setup, error)
	ListEvents(ctx context.Context, setupID int64) ([]setups.SetupEvent, error)
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
	store  setupsStore
	logger *slog.Logger
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

	rows, err := h.store.ListByInstrument(ctx, request.Params.InstrumentId, timeframe)
	if err != nil {
		h.logger.Error("httpapi: list setups", "instrument_id", request.Params.InstrumentId, "timeframe", timeframe, "error", err)
		return nil, fmt.Errorf("list setups: %w", err)
	}

	out := make(ListSetups200JSONResponse, 0, len(rows))
	for _, row := range rows {
		s, err := toAPISetup(row)
		if err != nil {
			h.logger.Error("httpapi: encode setup", "setup_id", row.ID, "error", err)
			return nil, fmt.Errorf("encode setup %d: %w", row.ID, err)
		}
		out = append(out, s)
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

	s, err := toAPISetup(row)
	if err != nil {
		h.logger.Error("httpapi: encode setup", "setup_id", row.ID, "error", err)
		return nil, fmt.Errorf("encode setup %d: %w", row.ID, err)
	}
	return GetSetup200JSONResponse(s), nil
}

func (h *apiHandler) ListSetupEvents(ctx context.Context, request ListSetupEventsRequestObject) (ListSetupEventsResponseObject, error) {
	// GetByID first: ListEvents alone cannot distinguish "setup 404
	// exists with zero events" (impossible in practice -- every setup
	// is created with a paired Detected event -- but not a case this
	// endpoint should silently also treat as "no such setup") from "no
	// such setup at all". Both return the shared 404 shape identically,
	// but checking existence explicitly is the honest way to produce
	// that 404, not inferring it from an empty list.
	if _, err := h.store.GetByID(ctx, request.Id); err != nil {
		if errors.Is(err, setups.ErrSetupNotFound) {
			return ListSetupEvents404JSONResponse(apiError("not_found", "setup not found")), nil
		}
		h.logger.Error("httpapi: get setup for events", "setup_id", request.Id, "error", err)
		return nil, fmt.Errorf("get setup %d: %w", request.Id, err)
	}

	rows, err := h.store.ListEvents(ctx, request.Id)
	if err != nil {
		h.logger.Error("httpapi: list setup events", "setup_id", request.Id, "error", err)
		return nil, fmt.Errorf("list events for setup %d: %w", request.Id, err)
	}

	out := make(ListSetupEvents200JSONResponse, 0, len(rows))
	for _, row := range rows {
		ev, err := toAPISetupEvent(row)
		if err != nil {
			h.logger.Error("httpapi: encode setup event", "event_id", row.ID, "error", err)
			return nil, fmt.Errorf("encode setup event %d: %w", row.ID, err)
		}
		out = append(out, ev)
	}
	return out, nil
}

// toAPISetup converts the domain type to its generated API shape.
// PlanSnapshot round-trips through JSON rather than being rebuilt
// field by field: it is deliberately loosely typed in the API contract
// (map[string]interface{}), so re-deriving its exact shape by hand here
// would just be a second, driftable copy of types.go's own json tags.
func toAPISetup(s setups.Setup) (Setup, error) {
	snapshot, err := toAPIPlanSnapshot(s.Plan)
	if err != nil {
		return Setup{}, err
	}
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
		PlanSnapshot:            snapshot,
		LastEventId:             s.LastEventID,
		EvaluationTime:          s.EvaluationTime,
		AlgorithmVersion:        s.AlgorithmVersion,
		CreatedAt:               s.CreatedAt,
		UpdatedAt:               s.UpdatedAt,
	}, nil
}

func toAPISetupEvent(e setups.SetupEvent) (SetupEvent, error) {
	snapshot, err := toAPIPlanSnapshot(e.Plan)
	if err != nil {
		return SetupEvent{}, err
	}
	return SetupEvent{
		Id:                      e.ID,
		SetupId:                 e.SetupID,
		EventType:               SetupEventEventType(e.Type.String()),
		PlanSnapshot:            snapshot,
		PoiSourceCandleId:       e.POISourceCandleID,
		PoiSourceCandleRevision: e.POISourceCandleRevision,
		EvaluationTime:          e.EvaluationTime,
		AlgorithmVersion:        e.AlgorithmVersion,
		CreatedAt:               e.CreatedAt,
	}, nil
}

func toAPIPlanSnapshot(p setups.PlanSnapshot) (PlanSnapshot, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal plan snapshot: %w", err)
	}
	var out PlanSnapshot
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("unmarshal plan snapshot: %w", err)
	}
	return out, nil
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
