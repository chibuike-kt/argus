package marketdata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/store"
)

// Store is the market-data module's data-access layer: candles, gaps and
// latest quotes. It needs a *sql.DB specifically, not just the generated
// DBTX interface, because SupersedeCandle manages its own transaction.
type Store struct {
	db  *sql.DB
	q   *store.Queries
	clk clock.Clock
}

// NewStore wires db and an injected clock (guide 7, 13: business logic
// never calls time.Now() directly). Use clock.Real{} in production and a
// clock.Fixed or clock.Mutable in tests.
func NewStore(db *sql.DB, clk clock.Clock) *Store {
	return &Store{db: db, q: store.New(db), clk: clk}
}

// UpsertForming records one tick of a forming (not yet closed) candle: the
// first tick for a given (instrument, open_time) creates the row at
// revision 1; a later tick for the same minute overwrites it in place.
// Calling this once the candle is already closed is a caller bug, and
// trg_candles_enforce_lifecycle rejects it rather than this method papering
// over it.
func (s *Store) UpsertForming(ctx context.Context, t Tick) (Candle, error) {
	if err := t.validate(); err != nil {
		return Candle{}, err
	}
	row, err := s.q.UpsertFormingCandle(ctx, store.UpsertFormingCandleParams{
		InstrumentID: t.InstrumentID,
		OpenTime:     t.OpenTime.Time(),
		CloseTime:    t.closeTime(),
		Open:         int64(t.Open),
		High:         int64(t.High),
		Low:          int64(t.Low),
		Close:        int64(t.Close),
		Volume:       int64(t.Volume),
		TickAligned:  t.TickAligned,
		Provider:     t.Provider,
		IngestedAt:   t.IngestedAt.Time(),
	})
	if err != nil {
		return Candle{}, fmt.Errorf("marketdata: upsert forming candle instrument=%d open_time=%s: %w", t.InstrumentID, t.OpenTime, err)
	}
	return fromCandleRow(row), nil
}

// Close freezes the forming candle at (instrumentID, openTime). Closing an
// already-closed candle is treated as an idempotent success (it returns
// the existing closed row) rather than an error, so a worker retrying a
// close after an ambiguous failure does not get a spurious failure on the
// retry (guide 14).
func (s *Store) Close(ctx context.Context, instrumentID int64, openTime clock.EventTime) (Candle, error) {
	row, err := s.q.CloseCandle(ctx, store.CloseCandleParams{
		InstrumentID: instrumentID,
		OpenTime:     openTime.Time(),
	})
	if err == nil {
		return fromCandleRow(row), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Candle{}, fmt.Errorf("marketdata: close candle instrument=%d open_time=%s: %w", instrumentID, openTime, err)
	}

	// Zero rows: not found, already closed, or already superseded. Look at
	// the current row to tell these apart instead of guessing.
	current, getErr := s.q.GetCurrentCandle(ctx, store.GetCurrentCandleParams{
		InstrumentID: instrumentID,
		OpenTime:     openTime.Time(),
	})
	switch {
	case errors.Is(getErr, sql.ErrNoRows):
		return Candle{}, fmt.Errorf("marketdata: close candle instrument=%d open_time=%s: %w", instrumentID, openTime, ErrCandleNotFound)
	case getErr != nil:
		return Candle{}, fmt.Errorf("marketdata: close candle instrument=%d open_time=%s: %w", instrumentID, openTime, getErr)
	case current.IsClosed:
		return fromCandleRow(current), nil
	default:
		// current exists, is not closed, and CloseCandle still matched
		// zero rows -- the WHERE clauses on both queries are identical
		// except for is_closed, so this should not be reachable.
		return Candle{}, fmt.Errorf("marketdata: close candle instrument=%d open_time=%s: candle exists, is not closed, yet the close update matched no rows", instrumentID, openTime)
	}
}

// SupersedeCandle replaces the closed candle at (instrumentID, openTime)
// with a new revision carrying corr's values. This is the three-step
// correction handshake documented on trg_candles_enforce_lifecycle in
// 000003_candles_quotes_and_gaps.up.sql:
//
//  1. mark the current (old) row as being superseded -- this is what lets
//     it leave idx_candles_current, the partial unique index on
//     (instrument_id, open_time) WHERE superseded_at IS NULL;
//  2. insert the new revision, now that the index has room for it;
//  3. resolve the old row's superseded_by to the new revision's id.
//
// All three run in one transaction. The schema enforces that only exactly
// this sequence of changes is legal (the trigger) and that at most one
// current row exists at a time (the index), but it cannot enforce the
// ORDER across three separate statements -- a trigger only ever sees one
// statement at a time. This method is what owns that ordering; nothing in
// the schema can.
func (s *Store) SupersedeCandle(ctx context.Context, instrumentID int64, openTime clock.EventTime, corr Correction) (Candle, error) {
	if err := corr.validate(); err != nil {
		return Candle{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Candle{}, fmt.Errorf("marketdata: supersede candle instrument=%d open_time=%s: begin tx: %w", instrumentID, openTime, err)
	}
	defer func() { _ = tx.Rollback() }()
	qtx := s.q.WithTx(tx)

	old, err := qtx.GetCurrentCandle(ctx, store.GetCurrentCandleParams{
		InstrumentID: instrumentID,
		OpenTime:     openTime.Time(),
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Candle{}, fmt.Errorf("marketdata: supersede candle instrument=%d open_time=%s: %w", instrumentID, openTime, ErrCandleNotFound)
	case err != nil:
		return Candle{}, fmt.Errorf("marketdata: supersede candle instrument=%d open_time=%s: %w", instrumentID, openTime, err)
	case !old.IsClosed:
		return Candle{}, fmt.Errorf("marketdata: supersede candle instrument=%d open_time=%s: %w", instrumentID, openTime, ErrCandleStillForming)
	}

	now := s.clk.Now()

	// Step 1: begin superseding the old row. Exactly one row must match --
	// anything else means a concurrent process already touched it since
	// the read above (GetCurrentCandle and this UPDATE are not atomic with
	// each other), and that is a real conflict to surface, not something
	// to paper over by proceeding anyway.
	affected, err := qtx.BeginSupersedeCandle(ctx, store.BeginSupersedeCandleParams{
		ID:           old.ID,
		SupersededAt: nullTimeFrom(now),
	})
	if err != nil {
		return Candle{}, fmt.Errorf("marketdata: supersede candle id=%d step 1 (begin): %w", old.ID, err)
	}
	if affected != 1 {
		return Candle{}, fmt.Errorf("marketdata: supersede candle id=%d step 1 (begin): expected to affect exactly 1 row, affected %d (concurrent modification)", old.ID, affected)
	}

	// Step 2: insert the correcting revision. Now legal: step 1 removed
	// the old row from idx_candles_current.
	newRevision := old.Revision + 1
	newRow, err := qtx.InsertCorrectingCandle(ctx, store.InsertCorrectingCandleParams{
		InstrumentID: instrumentID,
		OpenTime:     openTime.Time(),
		CloseTime:    openTime.Time().Add(time.Minute),
		Open:         int64(corr.Open),
		High:         int64(corr.High),
		Low:          int64(corr.Low),
		Close:        int64(corr.Close),
		Volume:       int64(corr.Volume),
		Revision:     newRevision,
		TickAligned:  corr.TickAligned,
		Provider:     corr.Provider,
		IngestedAt:   corr.IngestedAt.Time(),
	})
	if err != nil {
		return Candle{}, fmt.Errorf("marketdata: supersede candle id=%d step 2 (insert revision %d): %w", old.ID, newRevision, err)
	}

	// Step 3: resolve the old row's pointer to the new revision. Only now
	// does the old row become fully terminal.
	affected, err = qtx.ResolveSupersedeCandle(ctx, store.ResolveSupersedeCandleParams{
		ID:           old.ID,
		SupersededBy: sql.NullInt64{Int64: newRow.ID, Valid: true},
	})
	if err != nil {
		return Candle{}, fmt.Errorf("marketdata: supersede candle id=%d step 3 (resolve): %w", old.ID, err)
	}
	if affected != 1 {
		return Candle{}, fmt.Errorf("marketdata: supersede candle id=%d step 3 (resolve): expected to affect exactly 1 row, affected %d", old.ID, affected)
	}

	if err := tx.Commit(); err != nil {
		return Candle{}, fmt.Errorf("marketdata: supersede candle id=%d: commit: %w", old.ID, err)
	}
	return fromCandleRow(newRow), nil
}

// GetCurrent returns the current (non-superseded) candle at
// (instrumentID, openTime), or ErrCandleNotFound.
func (s *Store) GetCurrent(ctx context.Context, instrumentID int64, openTime clock.EventTime) (Candle, error) {
	row, err := s.q.GetCurrentCandle(ctx, store.GetCurrentCandleParams{
		InstrumentID: instrumentID,
		OpenTime:     openTime.Time(),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Candle{}, fmt.Errorf("marketdata: get current candle instrument=%d open_time=%s: %w", instrumentID, openTime, ErrCandleNotFound)
		}
		return Candle{}, fmt.Errorf("marketdata: get current candle instrument=%d open_time=%s: %w", instrumentID, openTime, err)
	}
	return fromCandleRow(row), nil
}
