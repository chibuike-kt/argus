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

// Remove takes instrumentID off userID's own watchlist. Removing an
// instrument that is not on it is not an error (idempotent): removed is
// false then. Ownership is the userID predicate itself -- the caller
// passes the session's user, never a client-supplied id.
func (s *Store) Remove(ctx context.Context, userID, instrumentID int64) (removed bool, err error) {
	n, err := s.q.DeleteWatchlistItem(ctx, store.DeleteWatchlistItemParams{UserID: userID, InstrumentID: instrumentID})
	if err != nil {
		return false, fmt.Errorf("watchlist: remove instrument %d: %w", instrumentID, err)
	}
	return n > 0, nil
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
// It applies the same system cap of tracked symbols as Add (architect
// review of 4.1a, item 5): a list that would track new Hyperliquid symbols
// beyond capOpt.Max is refused with a *TrackedCapError
// (ErrTrackedCapReached), and nothing is written.
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
func (s *Store) Replace(ctx context.Context, userID int64, instrumentIDs []int64, maxItems int, capOpt TrackedCapOptions) ([]Item, error) {
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

	// The same tracked cap as Add (architect review of 4.1a, item 5),
	// under the same lock: the set before and after the change, inside
	// this transaction; refused only when the change tracks a symbol that
	// was not tracked before and leaves the set over the cap.
	if err := q.LockTrackedCap(ctx); err != nil {
		return nil, fmt.Errorf("watchlist: replace: lock tracked cap: %w", err)
	}
	floor := capOpt.Floor
	if floor == nil {
		floor = []string{}
	}
	before, err := q.ListTrackedInstruments(ctx, floor)
	if err != nil {
		return nil, fmt.Errorf("watchlist: replace: tracked set: %w", err)
	}

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
	after, err := q.ListTrackedInstruments(ctx, floor)
	if err != nil {
		return nil, fmt.Errorf("watchlist: replace: tracked set after: %w", err)
	}
	wasTracked := make(map[int64]bool, len(before))
	for _, t := range before {
		wasTracked[t.ID] = true
	}
	adding := 0
	for _, t := range after {
		if !wasTracked[t.ID] {
			adding++
		}
	}
	if adding > 0 && len(after) > capOpt.Max {
		return nil, capError(before, capOpt.Max, adding)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("watchlist: replace: commit: %w", err)
	}
	return items, nil
}

// Phase 4.1a (D15): item-level add and reorder.

// ErrTrackedCapReached is an add that would make ARGUS track more distinct
// symbols than the system cap (D15.3).
var ErrTrackedCapReached = errors.New("watchlist: the system is tracking the maximum number of symbols")

// TrackedCapError is ErrTrackedCapReached with the numbers behind it, so
// the message says how many more symbols can be added (architect review
// of 4.1a, item 6). errors.Is(err, ErrTrackedCapReached) holds.
type TrackedCapError struct {
	// Cap is the system cap; Tracked is the tracked set now, the
	// configured floor symbols included (Floor of them); Adding is how
	// many new symbols the refused change would have tracked.
	Cap, Tracked, Floor, Adding int
}

// Remaining is how many more symbols can be tracked (never below 0).
func (e *TrackedCapError) Remaining() int { return max(e.Cap-e.Tracked, 0) }

func (e *TrackedCapError) Error() string {
	more := "no more symbols can be added"
	if r := e.Remaining(); r == 1 {
		more = "1 more symbol can be added"
	} else if r > 1 {
		more = fmt.Sprintf("%d more symbols can be added", r)
	}
	return fmt.Sprintf("ARGUS tracks at most %d symbols at once and is tracking %d (%d of them always tracked by configuration), so %s; this change would add %d. Remove a symbol from every watchlist first.",
		e.Cap, e.Tracked, e.Floor, more, e.Adding)
}

// Is makes errors.Is(err, ErrTrackedCapReached) true for a TrackedCapError.
func (e *TrackedCapError) Is(target error) bool { return target == ErrTrackedCapReached }

// ErrOrderMismatch is a reorder whose ids are not exactly the user's
// current list (another tab added or removed one meanwhile).
var ErrOrderMismatch = errors.New("watchlist: the order does not match the current list")

// EnableReason is the audit reason of an enablement made by an add (D15.1).
const EnableReason = "watchlist add"

// TrackedCapOptions are the system cap of distinct tracked symbols and the
// configured floor that counts against it (D15.3).
type TrackedCapOptions struct {
	Floor []string
	Max   int
}

// capError describes a refused change against the tracked set it saw.
func capError(tracked []store.ListTrackedInstrumentsRow, capSymbols, adding int) error {
	floor := 0
	for _, t := range tracked {
		if t.IsFloor {
			floor++
		}
	}
	return &TrackedCapError{Cap: capSymbols, Tracked: len(tracked), Floor: floor, Adding: adding}
}

// AddOptions are the limits and permissions of one add.
type AddOptions struct {
	// MaxItems is the per-user cap (ADR-015: 50).
	MaxItems int
	// AllowEnable permits enabling a disabled Hyperliquid instrument
	// (D15.1): true only outside production, the same guard
	// sync-hyperliquid uses.
	AllowEnable bool
	// Environment is recorded with an enablement (ARGUS_ENV).
	Environment string
	// Floor is the configured symbol floor (always tracked).
	Floor []string
	// TrackedCap is the system cap of distinct tracked symbols (D15.3: 8).
	TrackedCap int
}

// AddResult is the list after an add and what the add did.
type AddResult struct {
	Items []Item
	// Already: the instrument was on the list; nothing changed.
	Already bool
	// Enabled: this add enabled the instrument (an audit row was written).
	Enabled bool
}

// Add puts instrumentID at the end of userID's own watchlist, in one
// transaction: idempotent (already on the list: nothing changes), the
// per-user cap, the system cap of tracked symbols (serialised by an
// advisory lock, so two concurrent adds of two new symbols cannot both
// pass it), and -- only with AllowEnable, only for a Hyperliquid
// instrument -- enabling a disabled instrument with its audit record
// (D15.1). The user id comes from the caller's session, never the client.
func (s *Store) Add(ctx context.Context, userID, instrumentID int64, opt AddOptions) (AddResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AddResult{}, fmt.Errorf("watchlist: add: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.q.WithTx(tx)

	if _, err := q.GetWatchlistItem(ctx, store.GetWatchlistItemParams{UserID: userID, InstrumentID: instrumentID}); err == nil {
		items, err := listWith(ctx, q, userID)
		if err != nil {
			return AddResult{}, err
		}
		return AddResult{Items: items, Already: true}, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return AddResult{}, fmt.Errorf("watchlist: add: look up item: %w", err)
	}

	if err := q.LockTrackedCap(ctx); err != nil {
		return AddResult{}, fmt.Errorf("watchlist: add: lock tracked cap: %w", err)
	}
	inst, err := q.GetInstrumentForUpdate(ctx, instrumentID)
	if errors.Is(err, sql.ErrNoRows) {
		return AddResult{}, ErrInstrumentNotFound
	}
	if err != nil {
		return AddResult{}, fmt.Errorf("watchlist: add: look up instrument %d: %w", instrumentID, err)
	}
	if !inst.Enabled && (!opt.AllowEnable || inst.Provider != "hyperliquid") {
		return AddResult{}, ErrInstrumentDisabled
	}
	n, err := q.CountWatchlistItems(ctx, userID)
	if err != nil {
		return AddResult{}, fmt.Errorf("watchlist: add: count: %w", err)
	}
	if int(n) >= opt.MaxItems {
		return AddResult{}, ErrTooManyItems
	}
	if inst.Provider == "hyperliquid" {
		floor := opt.Floor
		if floor == nil {
			floor = []string{}
		}
		tracked, err := q.ListTrackedInstruments(ctx, floor)
		if err != nil {
			return AddResult{}, fmt.Errorf("watchlist: add: tracked set: %w", err)
		}
		already := false
		for _, t := range tracked {
			if t.ID == instrumentID {
				already = true
				break
			}
		}
		if !already && len(tracked) >= opt.TrackedCap {
			return AddResult{}, capError(tracked, opt.TrackedCap, 1)
		}
	}

	enabled := false
	if !inst.Enabled {
		changed, err := q.EnableInstrumentForWatchlist(ctx, instrumentID)
		if err != nil {
			return AddResult{}, fmt.Errorf("watchlist: add: enable instrument %d: %w", instrumentID, err)
		}
		if changed == 1 {
			if _, err := q.InsertInstrumentEnablement(ctx, store.InsertInstrumentEnablementParams{
				InstrumentID: instrumentID, UserID: sql.NullInt64{Int64: userID, Valid: true},
				Reason: EnableReason, Environment: opt.Environment,
			}); err != nil {
				return AddResult{}, fmt.Errorf("watchlist: add: audit enablement: %w", err)
			}
			enabled = true
		}
	}

	pos, err := q.NextWatchlistPosition(ctx, userID)
	if err != nil {
		return AddResult{}, fmt.Errorf("watchlist: add: next position: %w", err)
	}
	if _, err := q.InsertWatchlistItem(ctx, store.InsertWatchlistItemParams{UserID: userID, InstrumentID: instrumentID, Position: pos}); err != nil {
		return AddResult{}, fmt.Errorf("watchlist: add: insert: %w", err)
	}
	items, err := listWith(ctx, q, userID)
	if err != nil {
		return AddResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AddResult{}, fmt.Errorf("watchlist: add: commit: %w", err)
	}
	return AddResult{Items: items, Enabled: enabled}, nil
}

// Reorder sets userID's list to the order of instrumentIDs, which must be
// exactly the current list (same ids, each once); otherwise
// ErrOrderMismatch and nothing changes. The rows are locked first, so a
// concurrent add or remove waits and is never overwritten.
func (s *Store) Reorder(ctx context.Context, userID int64, instrumentIDs []int64) ([]Item, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("watchlist: reorder: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.q.WithTx(tx)
	rows, err := q.ListWatchlistItemsForUpdate(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("watchlist: reorder: lock: %w", err)
	}
	if len(rows) != len(instrumentIDs) {
		return nil, ErrOrderMismatch
	}
	current := make(map[int64]bool, len(rows))
	for _, r := range rows {
		current[r.InstrumentID] = true
	}
	seen := make(map[int64]bool, len(instrumentIDs))
	for _, id := range instrumentIDs {
		if !current[id] || seen[id] {
			return nil, ErrOrderMismatch
		}
		seen[id] = true
	}
	for i, id := range instrumentIDs {
		if err := q.SetWatchlistItemPosition(ctx, store.SetWatchlistItemPositionParams{
			UserID: userID, InstrumentID: id, Position: int32(i), //nolint:gosec // bounded by the list length (at most MaxItems)
		}); err != nil {
			return nil, fmt.Errorf("watchlist: reorder: set position: %w", err)
		}
	}
	items, err := listWith(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("watchlist: reorder: commit: %w", err)
	}
	return items, nil
}

func listWith(ctx context.Context, q *store.Queries, userID int64) ([]Item, error) {
	rows, err := q.ListWatchlistItems(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("watchlist: list: %w", err)
	}
	items := make([]Item, len(rows))
	for i, row := range rows {
		items[i] = Item{InstrumentID: row.InstrumentID, Position: int(row.Position)}
	}
	return items, nil
}
