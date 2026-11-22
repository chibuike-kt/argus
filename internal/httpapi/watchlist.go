package httpapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/chibuike-kt/argus/internal/identity"
	"github.com/chibuike-kt/argus/internal/tracking"
	"github.com/chibuike-kt/argus/internal/watchlist"
)

// watchlistStore is the boundary apiHandler needs from
// internal/watchlist.Store -- the same consumer-defined-interface
// pattern as setupsStore/instrumentsStore/candlesStore/authService.
type watchlistStore interface {
	List(ctx context.Context, userID int64) ([]watchlist.Item, error)
	Replace(ctx context.Context, userID int64, instrumentIDs []int64, maxItems int) ([]watchlist.Item, error)
	Remove(ctx context.Context, userID, instrumentID int64) (bool, error)
	Add(ctx context.Context, userID, instrumentID int64, opt watchlist.AddOptions) (watchlist.AddResult, error)
	Reorder(ctx context.Context, userID int64, instrumentIDs []int64) ([]watchlist.Item, error)
}

// trackingStore is what GET /watchlist/tracking needs (Phase 4.1a).
type trackingStore interface {
	Tracked(ctx context.Context, floor []string) ([]tracking.Tracked, error)
	States(ctx context.Context, ids []int64) (map[int64]tracking.Status, error)
}

var _ trackingStore = (*tracking.Store)(nil)

var _ watchlistStore = (*watchlist.Store)(nil)

func (h *apiHandler) GetWatchlist(ctx context.Context, _ GetWatchlistRequestObject) (GetWatchlistResponseObject, error) {
	user, _ := identity.UserFromContext(ctx)
	items, err := h.watchlist.List(ctx, user.ID)
	if err != nil {
		h.logger.Error("httpapi: get watchlist", "user_id", user.ID, "error", err)
		return nil, fmt.Errorf("get watchlist: %w", err)
	}
	return GetWatchlist200JSONResponse(toWatchlistItems(items)), nil
}

func (h *apiHandler) UpdateWatchlist(ctx context.Context, request UpdateWatchlistRequestObject) (UpdateWatchlistResponseObject, error) {
	if request.Body == nil {
		return UpdateWatchlist400JSONResponse(apiError("invalid_argument", "request body is required")), nil
	}
	user, _ := identity.UserFromContext(ctx)
	items, err := h.watchlist.Replace(ctx, user.ID, request.Body.InstrumentIds, h.authPolicy.WatchlistMaxItems)
	if err == nil {
		return UpdateWatchlist200JSONResponse(toWatchlistItems(items)), nil
	}
	switch {
	case errors.Is(err, watchlist.ErrTooManyItems),
		errors.Is(err, watchlist.ErrDuplicateInstrument),
		errors.Is(err, watchlist.ErrInstrumentNotFound),
		errors.Is(err, watchlist.ErrInstrumentDisabled):
		return UpdateWatchlist400JSONResponse(apiError("invalid_argument", err.Error())), nil
	}
	h.logger.Error("httpapi: update watchlist", "user_id", user.ID, "error", err)
	return nil, fmt.Errorf("update watchlist: %w", err)
}

// RemoveWatchlistItem serves DELETE /watchlist/items/{instrument_id}:
// the session user's own row only (deny by default -- there is no user id
// in the request to trust), idempotent, and the remaining list back so
// the client can reconcile.
func (h *apiHandler) RemoveWatchlistItem(ctx context.Context, request RemoveWatchlistItemRequestObject) (RemoveWatchlistItemResponseObject, error) {
	if request.InstrumentId <= 0 {
		return RemoveWatchlistItem400JSONResponse(apiError("invalid_argument", "instrument_id must be a positive integer")), nil
	}
	user, _ := identity.UserFromContext(ctx)
	if _, err := h.watchlist.Remove(ctx, user.ID, request.InstrumentId); err != nil {
		h.logger.Error("httpapi: remove watchlist item", "user_id", user.ID, "instrument_id", request.InstrumentId, "error", err)
		return nil, fmt.Errorf("remove watchlist item: %w", err)
	}
	items, err := h.watchlist.List(ctx, user.ID)
	if err != nil {
		h.logger.Error("httpapi: list watchlist after remove", "user_id", user.ID, "error", err)
		return nil, fmt.Errorf("list watchlist after remove: %w", err)
	}
	return RemoveWatchlistItem200JSONResponse(toWatchlistItems(items)), nil
}

func toWatchlistItems(items []watchlist.Item) []WatchlistItem {
	out := make([]WatchlistItem, len(items))
	for i, item := range items {
		out[i] = WatchlistItem{InstrumentId: item.InstrumentID, Position: int32(item.Position)} //nolint:gosec // position is 0..len(instrumentIDs)-1, capped at WatchlistMaxItems (ADR-015: "maximum 50")
	}
	return out
}

// AddWatchlistItem serves POST /watchlist/items (Phase 4.1a, D15). The
// user is the session's; enabling a disabled Hyperliquid instrument is
// allowed only outside production (D15.1, the guard sync-hyperliquid
// uses) and is audited by the store in the same transaction.
func (h *apiHandler) AddWatchlistItem(ctx context.Context, request AddWatchlistItemRequestObject) (AddWatchlistItemResponseObject, error) {
	if request.Body == nil || request.Body.InstrumentId <= 0 {
		return AddWatchlistItem400JSONResponse(apiError("invalid_argument", "instrument_id must be a positive integer")), nil
	}
	user, _ := identity.UserFromContext(ctx)
	res, err := h.watchlist.Add(ctx, user.ID, request.Body.InstrumentId, watchlist.AddOptions{
		MaxItems:    h.authPolicy.WatchlistMaxItems,
		AllowEnable: !h.isProduction,
		Environment: h.environment,
		Floor:       h.trackingFloor,
		TrackedCap:  h.trackedCap,
	})
	switch {
	case err == nil:
		if res.Enabled {
			h.logger.Warn("httpapi: watchlist add enabled an instrument (D15.1, private research use only)",
				"user_id", user.ID, "instrument_id", request.Body.InstrumentId, "reason", watchlist.EnableReason, "environment", h.environment)
		}
		return AddWatchlistItem200JSONResponse(toWatchlistItems(res.Items)), nil
	case errors.Is(err, watchlist.ErrInstrumentNotFound):
		return AddWatchlistItem404JSONResponse(apiError("not_found", "instrument not found")), nil
	case errors.Is(err, watchlist.ErrInstrumentDisabled):
		return AddWatchlistItem400JSONResponse(apiError("instrument_not_enabled", "this instrument is not enabled; the operator has to enable it")), nil
	case errors.Is(err, watchlist.ErrTooManyItems):
		return AddWatchlistItem409JSONResponse(apiError("watchlist_full", fmt.Sprintf("a watchlist holds at most %d instruments", h.authPolicy.WatchlistMaxItems))), nil
	case errors.Is(err, watchlist.ErrTrackedCapReached):
		return AddWatchlistItem409JSONResponse(apiError("tracked_cap_reached", fmt.Sprintf("ARGUS is already tracking the maximum of %d symbols; a symbol already tracked can still be added", h.trackedCap))), nil
	}
	h.logger.Error("httpapi: add watchlist item", "user_id", user.ID, "instrument_id", request.Body.InstrumentId, "error", err)
	return nil, fmt.Errorf("add watchlist item: %w", err)
}

// ReorderWatchlist serves PUT /watchlist/order.
func (h *apiHandler) ReorderWatchlist(ctx context.Context, request ReorderWatchlistRequestObject) (ReorderWatchlistResponseObject, error) {
	if request.Body == nil {
		return ReorderWatchlist400JSONResponse(apiError("invalid_argument", "request body is required")), nil
	}
	if len(request.Body.InstrumentIds) > h.authPolicy.WatchlistMaxItems {
		return ReorderWatchlist400JSONResponse(apiError("invalid_argument", fmt.Sprintf("at most %d instrument ids", h.authPolicy.WatchlistMaxItems))), nil
	}
	user, _ := identity.UserFromContext(ctx)
	items, err := h.watchlist.Reorder(ctx, user.ID, request.Body.InstrumentIds)
	if errors.Is(err, watchlist.ErrOrderMismatch) {
		return ReorderWatchlist409JSONResponse(apiError("watchlist_changed", "the watchlist changed meanwhile; reload it and reorder again")), nil
	}
	if err != nil {
		h.logger.Error("httpapi: reorder watchlist", "user_id", user.ID, "error", err)
		return nil, fmt.Errorf("reorder watchlist: %w", err)
	}
	return ReorderWatchlist200JSONResponse(toWatchlistItems(items)), nil
}

// GetWatchlistTracking serves GET /watchlist/tracking.
func (h *apiHandler) GetWatchlistTracking(ctx context.Context, _ GetWatchlistTrackingRequestObject) (GetWatchlistTrackingResponseObject, error) {
	user, _ := identity.UserFromContext(ctx)
	items, err := h.watchlist.List(ctx, user.ID)
	if err != nil {
		h.logger.Error("httpapi: watchlist tracking: list", "user_id", user.ID, "error", err)
		return nil, fmt.Errorf("watchlist tracking: %w", err)
	}
	out := WatchlistTracking{CanEnable: !h.isProduction, Cap: int32(h.trackedCap), TrackedInstrumentIds: []int64{}, Items: make([]WatchlistItemTracking, 0, len(items))} //nolint:gosec // a small configured cap
	tracked := map[int64]bool{}
	states := map[int64]tracking.Status{}
	if h.tracking != nil {
		all, err := h.tracking.Tracked(ctx, h.trackingFloor)
		if err != nil {
			h.logger.Error("httpapi: watchlist tracking: tracked set", "error", err)
			return nil, fmt.Errorf("watchlist tracking: %w", err)
		}
		for i, t := range all {
			if i >= h.trackedCap {
				break
			}
			tracked[t.ID] = true
			out.TrackedInstrumentIds = append(out.TrackedInstrumentIds, t.ID)
		}
		ids := make([]int64, len(items))
		for i, it := range items {
			ids[i] = it.InstrumentID
		}
		if states, err = h.tracking.States(ctx, ids); err != nil {
			h.logger.Error("httpapi: watchlist tracking: states", "error", err)
			return nil, fmt.Errorf("watchlist tracking: %w", err)
		}
	}
	for _, it := range items {
		row := WatchlistItemTracking{InstrumentId: it.InstrumentID, Tracked: tracked[it.InstrumentID]}
		if st, ok := states[it.InstrumentID]; ok {
			state, reason, at := string(st.State), st.Reason, st.UpdatedAt.UTC()
			attempt := int32(min(st.Attempt, 1<<30)) //nolint:gosec // bounded just here
			row.State, row.Reason, row.Attempt, row.UpdatedAt = &state, &reason, &attempt, &at
		}
		out.Items = append(out.Items, row)
	}
	return GetWatchlistTracking200JSONResponse(out), nil
}
