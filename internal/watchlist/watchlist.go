// Package watchlist is a feature module boundary (see ADR-003): each
// user's own list of enabled instruments, with an order (ADR-015
// "Signup and onboarding"; ADR-016: "Watchlists are private" --
// every method here is scoped to the caller's own userID, never a
// client-supplied one, the deny-by-default ownership check that
// guarantee rests on).
package watchlist

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/store"
)

// ErrTooManyItems is Replace's own rejection when items exceeds the
// configured cap (ADR-015: "maximum 50").
var ErrTooManyItems = errors.New("watchlist: too many items")

// ErrDuplicateInstrument is Replace's own rejection when the same
// instrument id appears more than once in one call (architect review
// of Part 2, item 4): UNIQUE(user_id, instrument_id) would otherwise
// fail the second INSERT after the first DELETE had already run,
// leaving the user with an empty or partial list and a 500.
var ErrDuplicateInstrument = errors.New("watchlist: duplicate instrument id")

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

// Store is watchlist_items' own data-access layer. db is kept (not
// just wrapped into store.Queries) so Replace can open a transaction
// spanning its own delete and inserts (architect review of Part 2,
// item 4).
type Store struct {
	db          *sql.DB
	q           *store.Queries
	instruments instrumentLookup
}

// NewStore builds a Store. instruments is used only to check that
// each instrument id Replace is given exists and is enabled.
func NewStore(db *sql.DB, instruments instrumentLookup) *Store {
	return &Store{db: db, q: store.New(db), instruments: instruments}
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
// reorder API. The list may not exceed maxItems (ADR-015), it may not
// repeat an instrument id, and every id must name an existing, enabled
// instrument -- all three checked before anything is written.
//
// Architect review (of Part 2) item 4: this used to delete the old
// list, then insert the new one, with no transaction -- a duplicate id
// in the request failed UNIQUE(user_id, instrument_id) on its second
// INSERT, after the DELETE had already committed, leaving the user
// with an empty or partial list and a 500. Duplicates are now rejected
// up front (ErrDuplicateInstrument), before the database is touched at
// all, and the delete and every insert now share one transaction, so
// any other failure during that phase rolls back to the previous list
// instead of leaving a partial one.
func (s *Store) Replace(ctx context.Context, userID int64, instrumentIDs []int64, maxItems int) ([]Item, error) {
	if len(instrumentIDs) > maxItems {
		return nil, ErrTooManyItems
	}
	seen := make(map[int64]bool, len(instrumentIDs))
	for _, id := range instrumentIDs {
		if seen[id] {
			return nil, ErrDuplicateInstrument
		}
		seen[id] = true
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

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("watchlist: replace: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.q.WithTx(tx)

	if err := q.DeleteWatchlistItems(ctx, userID); err != nil {
		return nil, fmt.Errorf("watchlist: replace: delete existing: %w", err)
	}
	items := make([]Item, len(instrumentIDs))
	for i, id := range instrumentIDs {
		row, err := q.InsertWatchlistItem(ctx, store.InsertWatchlistItemParams{
			UserID: userID, InstrumentID: id, Position: int32(i), //nolint:gosec // i is bounded by maxItems (ADR-015: "maximum 50"), already checked above
		})
		if err != nil {
			return nil, fmt.Errorf("watchlist: replace: insert item: %w", err)
		}
		items[i] = Item{InstrumentID: row.InstrumentID, Position: int(row.Position)}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("watchlist: replace: commit: %w", err)
	}
	return items, nil
}
