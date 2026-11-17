package httpapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/chibuike-kt/argus/internal/identity"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/preferences"
)

// preferencesStore is the boundary apiHandler needs from
// preferences.Store (consumer-defined, guide 4).
type preferencesStore interface {
	GetPreferences(ctx context.Context, userID int64) (preferences.Doc, error)
	PutPreferences(ctx context.Context, userID int64, raw []byte, baseVersion int32) (preferences.Doc, error)
	GetDrawings(ctx context.Context, userID, instrumentID int64, timeframe string) (preferences.Doc, error)
	PutDrawings(ctx context.Context, userID, instrumentID int64, timeframe string, raw []byte, baseVersion int32) (preferences.Doc, error)
}

var _ preferencesStore = (*preferences.Store)(nil)

// Every handler here takes the user from the session (requireSession put
// it in ctx; these routes are not on its public allow-list), never from
// the request: a client cannot name another user's document (ADR-015).

func (h *apiHandler) GetPreferences(ctx context.Context, _ GetPreferencesRequestObject) (GetPreferencesResponseObject, error) {
	user, _ := identity.UserFromContext(ctx)
	doc, err := h.prefs.GetPreferences(ctx, user.ID)
	if err != nil {
		h.logger.Error("httpapi: get preferences", "user_id", user.ID, "error", err)
		return nil, fmt.Errorf("get preferences: %w", err)
	}
	return GetPreferences200JSONResponse{Data: doc.Data, Version: doc.Version, UpdatedAt: doc.UpdatedAt}, nil
}

func (h *apiHandler) UpdatePreferences(ctx context.Context, request UpdatePreferencesRequestObject) (UpdatePreferencesResponseObject, error) {
	if request.Body == nil || len(request.Body.Data) == 0 {
		return UpdatePreferences400JSONResponse(apiError("invalid_argument", "data and version are required")), nil
	}
	user, _ := identity.UserFromContext(ctx)
	doc, err := h.prefs.PutPreferences(ctx, user.ID, request.Body.Data, request.Body.Version)
	switch {
	case err == nil:
		return UpdatePreferences200JSONResponse{Data: doc.Data, Version: doc.Version, UpdatedAt: doc.UpdatedAt}, nil
	case errors.Is(err, preferences.ErrVersionConflict):
		return UpdatePreferences409JSONResponse(apiError("version_conflict", "preferences changed elsewhere; re-read and retry")), nil
	case errors.Is(err, preferences.ErrTooLarge):
		return UpdatePreferences413JSONResponse(apiError("request_too_large", err.Error())), nil
	case errors.Is(err, preferences.ErrInvalid):
		return UpdatePreferences400JSONResponse(apiError("invalid_argument", err.Error())), nil
	}
	h.logger.Error("httpapi: update preferences", "user_id", user.ID, "error", err)
	return nil, fmt.Errorf("update preferences: %w", err)
}

// drawingsInstrumentOK is the shared instrument check for the drawings
// endpoints: an unknown or disabled instrument is a 404 (the same answer
// for both, as elsewhere in this API).
func (h *apiHandler) drawingsInstrumentOK(ctx context.Context, id int64) (bool, error) {
	inst, err := h.instruments.GetByID(ctx, id)
	if errors.Is(err, instruments.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return inst.Enabled, nil
}

func (h *apiHandler) GetDrawings(ctx context.Context, request GetDrawingsRequestObject) (GetDrawingsResponseObject, error) {
	tf := request.Params.Timeframe
	if !preferences.IsTimeframe(tf) {
		return GetDrawings400JSONResponse(apiError("invalid_argument", "timeframe must be one of 1m, 5m, 15m, 1h, 4h, 1d")), nil
	}
	ok, err := h.drawingsInstrumentOK(ctx, request.Params.InstrumentId)
	if err != nil {
		return nil, fmt.Errorf("get drawings instrument %d: %w", request.Params.InstrumentId, err)
	}
	if !ok {
		return GetDrawings404JSONResponse(apiError("not_found", "instrument not found")), nil
	}
	user, _ := identity.UserFromContext(ctx)
	doc, err := h.prefs.GetDrawings(ctx, user.ID, request.Params.InstrumentId, tf)
	if err != nil {
		h.logger.Error("httpapi: get drawings", "user_id", user.ID, "error", err)
		return nil, fmt.Errorf("get drawings: %w", err)
	}
	return GetDrawings200JSONResponse{InstrumentId: request.Params.InstrumentId, Timeframe: tf, Data: doc.Data, Version: doc.Version, UpdatedAt: doc.UpdatedAt}, nil
}

func (h *apiHandler) UpdateDrawings(ctx context.Context, request UpdateDrawingsRequestObject) (UpdateDrawingsResponseObject, error) {
	tf := request.Params.Timeframe
	if !preferences.IsTimeframe(tf) {
		return UpdateDrawings400JSONResponse(apiError("invalid_argument", "timeframe must be one of 1m, 5m, 15m, 1h, 4h, 1d")), nil
	}
	if request.Body == nil || len(request.Body.Data) == 0 {
		return UpdateDrawings400JSONResponse(apiError("invalid_argument", "data and version are required")), nil
	}
	ok, err := h.drawingsInstrumentOK(ctx, request.Params.InstrumentId)
	if err != nil {
		return nil, fmt.Errorf("update drawings instrument %d: %w", request.Params.InstrumentId, err)
	}
	if !ok {
		return UpdateDrawings404JSONResponse(apiError("not_found", "instrument not found")), nil
	}
	user, _ := identity.UserFromContext(ctx)
	doc, err := h.prefs.PutDrawings(ctx, user.ID, request.Params.InstrumentId, tf, request.Body.Data, request.Body.Version)
	switch {
	case err == nil:
		return UpdateDrawings200JSONResponse{InstrumentId: request.Params.InstrumentId, Timeframe: tf, Data: doc.Data, Version: doc.Version, UpdatedAt: doc.UpdatedAt}, nil
	case errors.Is(err, preferences.ErrVersionConflict):
		return UpdateDrawings409JSONResponse(apiError("version_conflict", "drawings changed elsewhere; re-read and retry")), nil
	case errors.Is(err, preferences.ErrTooLarge):
		return UpdateDrawings413JSONResponse(apiError("request_too_large", err.Error())), nil
	case errors.Is(err, preferences.ErrInvalid):
		return UpdateDrawings400JSONResponse(apiError("invalid_argument", err.Error())), nil
	}
	h.logger.Error("httpapi: update drawings", "user_id", user.ID, "error", err)
	return nil, fmt.Errorf("update drawings: %w", err)
}
