package cursor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/store"
)

// ErrNotFound is returned when no cursor exists yet for the requested
// (instrument, timeframe) -- distinct from a cursor that exists at
// the zero time, which Get never returns (SetAt only ever persists a
// real close_time).
var ErrNotFound = errors.New("cursor: not found")

// Store is the resume cursor's data-access layer.
type Store struct {
	q *store.Queries
}

// NewStore wraps db (a *sql.DB or, inside a transaction, a *sql.Tx).
func NewStore(db store.DBTX) *Store {
	return &Store{q: store.New(db)}
}

// WithTx returns a Store bound to tx, so a caller can advance the
// cursor as part of the same atomic unit as whatever work it is
// recording the progress of.
func (s *Store) WithTx(tx *sql.Tx) *Store {
	return &Store{q: s.q.WithTx(tx)}
}

// Get returns the last fully processed candle's close_time for
// (instrumentID, timeframe), or ErrNotFound if this pair has never
// been advanced (a true cold start: process from the beginning).
func (s *Store) Get(ctx context.Context, instrumentID int64, timeframe string) (time.Time, error) {
	row, err := s.q.GetProcessingCursor(ctx, store.GetProcessingCursorParams{
		InstrumentID: instrumentID, Timeframe: timeframe,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, ErrNotFound
		}
		return time.Time{}, fmt.Errorf("cursor: get: %w", err)
	}
	return row.LastProcessedCloseTime, nil
}

// SetAt advances (or initializes) the cursor to closeTime. A plain
// upsert: this is a mutable projection, not an evidence record (see
// the package doc comment), so there is no history to preserve.
func (s *Store) SetAt(ctx context.Context, instrumentID int64, timeframe string, closeTime time.Time, now time.Time) error {
	_, err := s.q.SetProcessingCursor(ctx, store.SetProcessingCursorParams{
		InstrumentID: instrumentID, Timeframe: timeframe,
		LastProcessedCloseTime: closeTime, UpdatedAt: now,
	})
	if err != nil {
		return fmt.Errorf("cursor: set: %w", err)
	}
	return nil
}
