// Package watchlist is a feature module boundary (see ADR-003): each
// user's own list of enabled instruments, with an order (ADR-015
// "Signup and onboarding"; ADR-016: "Watchlists are private" --
// every method here is scoped to the caller's own userID, never a
// client-supplied one, the deny-by-default ownership check that
// guarantee rests on).
package watchlist

import (
	"context"
	"errors"
	"fmt"

	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/store"
)

// ErrTooManyItems is Replace's own rejection when items exceeds the
// configured cap (ADR-015: "maximum 50").
var ErrTooManyItems = errors.New("watchlist: too many items")

// ErrInstrumentNotFound/ErrInstrumentDisabled are Replace's own
// rejection when an instrument id does not exist, or exists but is
// not enabled (ADR-015: "a watchlist of enabled instruments").
var (
	ErrInstrumentNotFound = errors.New("watchlist: instrument not found")
	ErrInstrumentDisabled = errors.New("watchlist: instrument is not enabled")
)

// Item is the domain view of a watchlist_items row.
type Item struct {
	InstrumentID int64
	Position     int
}

// instrumentLookup is the slice of *internal/instruments.Store Replace
// actually needs -- a consumer-defined interface (this brief's own
// rule 7) so this package does not need the full instruments.Store
// surface, and so a test can satisfy it with a hand-written fake
// instead of a real Postgres connection.
type instrumentLookup interface {
	GetByID(ctx context.Context, id int64) (instruments.Instrument, error)
}

// Store is watchlist_items' own data-access layer.
type Store struct {
	q           *store.Queries
	instruments instrumentLookup
}

// NewStore builds a Store. instruments is used only to check that
// each instrument id Replace is given exists and is enabled.
func NewStore(db store.DBTX, instruments instrumentLookup) *Store {
	return &Store{q: store.New(db), instruments: instruments}
}

// List returns userID's own watchlist, in order.
func (s *Store) List(ctx context.Context, userID int64) ([]Item, error) {
	rows, err := s.q.ListWatchlistItems(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("watchlist: list: %w", err)
	}
	items := make([]Item, len(rows))
	for i, row := range rows {
		items[i] = Item{InstrumentID: row.InstrumentID, Position: int(row.Position)}
	}
	return items, nil
}

// Count returns how many items are in userID's own watchlist --
// internal/identity/service.Service's own CompleteOnboarding check
// ("at least one watchlist item") is the reason this exists as its
// own method rather than len(List(...)) everywhere it is needed.
func (s *Store) Count(ctx context.Context, userID int64) (int, error) {
	n, err := s.q.CountWatchlistItems(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("watchlist: count: %w", err)
	}
	return int(n), nil
}

// Replace replaces userID's whole watchlist at once, in the given
// order (position = index in instrumentIDs) -- the simplest shape for
// a single settings/onboarding write, not an incremental add/remove/
// reorder API. Every id must name an existing, enabled instrument, and
// the list may not exceed maxItems (ADR-015), checked before anything
// is written. Not wrapped in an explicit transaction: the delete-then-
// insert below can only ever be re-run by the same user it belongs to
// (ADR-016 ownership scoping), and a failure partway through leaves at
// worst an empty or partial list for that one user to retry, never
// another user's data.
func (s *Store) Replace(ctx context.Context, userID int64, instrumentIDs []int64, maxItems int) ([]Item, error) {
	if len(instrumentIDs) > maxItems {
		return nil, ErrTooManyItems
	}
	for _, id := range instrumentIDs {
		inst, err := s.instruments.GetByID(ctx, id)
		if err != nil {
			if errors.Is(err, instruments.ErrNotFound) {
				return nil, ErrInstrumentNotFound
			}
			return nil, fmt.Errorf("watchlist: replace: look up instrument %d: %w", id, err)
		}
		if !inst.Enabled {
			return nil, ErrInstrumentDisabled
		}
	}

	if err := s.q.DeleteWatchlistItems(ctx, userID); err != nil {
		return nil, fmt.Errorf("watchlist: replace: delete existing: %w", err)
	}
	items := make([]Item, len(instrumentIDs))
	for i, id := range instrumentIDs {
		row, err := s.q.InsertWatchlistItem(ctx, store.InsertWatchlistItemParams{
			UserID: userID, InstrumentID: id, Position: int32(i),
		})
		if err != nil {
			return nil, fmt.Errorf("watchlist: replace: insert item: %w", err)
		}
		items[i] = Item{InstrumentID: row.InstrumentID, Position: int(row.Position)}
	}
	return items, nil
}
