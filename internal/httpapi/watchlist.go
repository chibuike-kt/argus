package httpapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/chibuike-kt/argus/internal/identity"
	"github.com/chibuike-kt/argus/internal/watchlist"
)

// watchlistStore is the boundary apiHandler needs from
// internal/watchlist.Store -- the same consumer-defined-interface
// pattern as setupsStore/instrumentsStore/candlesStore/authService.
type watchlistStore interface {
	List(ctx context.Context, userID int64) ([]watchlist.Item, error)
	Replace(ctx context.Context, userID int64, instrumentIDs []int64, maxItems int) ([]watchlist.Item, error)
}

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
		errors.Is(err, watchlist.ErrInstrumentNotFound),
		errors.Is(err, watchlist.ErrInstrumentDisabled):
		return UpdateWatchlist400JSONResponse(apiError("invalid_argument", err.Error())), nil
	}
	h.logger.Error("httpapi: update watchlist", "user_id", user.ID, "error", err)
	return nil, fmt.Errorf("update watchlist: %w", err)
}

func toWatchlistItems(items []watchlist.Item) []WatchlistItem {
	out := make([]WatchlistItem, len(items))
	for i, item := range items {
		out[i] = WatchlistItem{InstrumentId: item.InstrumentID, Position: int32(item.Position)} //nolint:gosec // position is 0..len(instrumentIDs)-1, capped at WatchlistMaxItems (ADR-015: "maximum 50")
	}
	return out
}
