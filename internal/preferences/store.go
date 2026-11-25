package preferences

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/store"
)

// Doc is a stored document and its version. Version 0 means nothing is
// stored yet (Data is then the empty document: {} or []).
type Doc struct {
	Data      json.RawMessage
	Version   int32
	UpdatedAt *time.Time
}

// Store reads and writes per-user preferences and drawings with
// optimistic concurrency: a write names the version it was based on and
// fails with ErrVersionConflict when that is no longer current.
type Store struct {
	q   *store.Queries
	clk clock.Clock
}

// NewStore wires db and an injected clock (updated_at is set from it).
func NewStore(db store.DBTX, clk clock.Clock) *Store {
	return &Store{q: store.New(db), clk: clk}
}

var (
	emptyObject = json.RawMessage(`{}`)
	emptyArray  = json.RawMessage(`[]`)
)

// GetPreferences returns userID's preferences, or {} at version 0.
func (s *Store) GetPreferences(ctx context.Context, userID int64) (Doc, error) {
	row, err := s.q.GetUserPreferences(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return Doc{Data: emptyObject}, nil
	}
	if err != nil {
		return Doc{}, fmt.Errorf("preferences: get user=%d: %w", userID, err)
	}
	t := row.UpdatedAt.UTC()
	return Doc{Data: row.Data, Version: row.Version, UpdatedAt: &t}, nil
}

// PutPreferences validates raw and stores it as the next version, if
// baseVersion is the current one (0 when nothing is stored yet).
func (s *Store) PutPreferences(ctx context.Context, userID int64, raw []byte, baseVersion int32) (Doc, error) {
	data, err := ValidatePreferences(raw)
	if err != nil {
		return Doc{}, err
	}
	now := s.clk.Now()
	var row store.UserPreference
	if baseVersion == 0 {
		row, err = s.q.InsertUserPreferences(ctx, store.InsertUserPreferencesParams{UserID: userID, Data: data, UpdatedAt: now})
	} else {
		row, err = s.q.UpdateUserPreferences(ctx, store.UpdateUserPreferencesParams{UserID: userID, Data: data, Version: baseVersion, UpdatedAt: now})
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Doc{}, fmt.Errorf("%w: user=%d base version %d is not current", ErrVersionConflict, userID, baseVersion)
	}
	if err != nil {
		return Doc{}, fmt.Errorf("preferences: put user=%d: %w", userID, err)
	}
	t := row.UpdatedAt.UTC()
	return Doc{Data: row.Data, Version: row.Version, UpdatedAt: &t}, nil
}

// GetDrawings returns userID's drawings on instrumentID, every timeframe
// (owner decision D17), or [] at version 0 when there are none.
//
// The first read of an instrument with no instrument document yet merges
// the per timeframe documents stored before D17 (MergeTimeframeDrawings)
// into one, stored as version 1. Concurrent first reads are safe: the
// insert is ON CONFLICT DO NOTHING and every caller then reads the one row
// that won. The old documents are read only, never changed.
func (s *Store) GetDrawings(ctx context.Context, userID, instrumentID int64) (Doc, error) {
	row, err := s.q.GetUserInstrumentDrawings(ctx, store.GetUserInstrumentDrawingsParams{UserID: userID, InstrumentID: instrumentID})
	if errors.Is(err, sql.ErrNoRows) {
		merged, mergeErr := s.mergeTimeframeDocs(ctx, userID, instrumentID)
		if mergeErr != nil {
			return Doc{}, mergeErr
		}
		if !merged {
			return Doc{Data: emptyArray}, nil
		}
		row, err = s.q.GetUserInstrumentDrawings(ctx, store.GetUserInstrumentDrawingsParams{UserID: userID, InstrumentID: instrumentID})
	}
	if err != nil {
		return Doc{}, fmt.Errorf("preferences: get drawings user=%d instrument=%d: %w", userID, instrumentID, err)
	}
	t := row.UpdatedAt.UTC()
	return Doc{Data: row.Data, Version: row.Version, UpdatedAt: &t}, nil
}

// mergeTimeframeDocs stores the merge of userID's per timeframe documents
// on instrumentID as the instrument document (version 1), if there are
// any. merged is true when an instrument document exists afterwards
// (this call's, or a concurrent one's).
func (s *Store) mergeTimeframeDocs(ctx context.Context, userID, instrumentID int64) (merged bool, err error) {
	rows, err := s.q.ListUserDrawingsAllTimeframes(ctx, store.ListUserDrawingsAllTimeframesParams{UserID: userID, InstrumentID: instrumentID})
	if err != nil {
		return false, fmt.Errorf("preferences: read per timeframe drawings user=%d instrument=%d: %w", userID, instrumentID, err)
	}
	if len(rows) == 0 {
		return false, nil
	}
	docs := make([]TimeframeDoc, len(rows))
	for i, r := range rows {
		docs[i] = TimeframeDoc{Timeframe: r.Timeframe, Data: r.Data, UpdatedAt: r.UpdatedAt}
	}
	data, err := MergeTimeframeDrawings(docs)
	if err != nil {
		return false, err
	}
	_, err = s.q.InsertUserInstrumentDrawings(ctx, store.InsertUserInstrumentDrawingsParams{
		UserID: userID, InstrumentID: instrumentID, Data: data, UpdatedAt: s.clk.Now(),
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("preferences: store merged drawings user=%d instrument=%d: %w", userID, instrumentID, err)
	}
	return true, nil // inserted, or a concurrent first read inserted first
}

// PutDrawings validates raw and stores it as the next version of
// userID's drawings on instrumentID, if baseVersion is current. A first
// write (baseVersion 0) merges the per timeframe documents first, so it
// can never create an instrument document that hides them: when there are
// any, the write is stale (ErrVersionConflict) and the client re-reads
// the merged document.
func (s *Store) PutDrawings(ctx context.Context, userID, instrumentID int64, raw []byte, baseVersion int32) (Doc, error) {
	data, err := ValidateDrawings(raw)
	if err != nil {
		return Doc{}, err
	}
	now := s.clk.Now()
	var row store.UserInstrumentDrawing
	if baseVersion == 0 {
		if _, err := s.mergeTimeframeDocs(ctx, userID, instrumentID); err != nil {
			return Doc{}, err
		}
		row, err = s.q.InsertUserInstrumentDrawings(ctx, store.InsertUserInstrumentDrawingsParams{
			UserID: userID, InstrumentID: instrumentID, Data: data, UpdatedAt: now,
		})
	} else {
		row, err = s.q.UpdateUserInstrumentDrawings(ctx, store.UpdateUserInstrumentDrawingsParams{
			UserID: userID, InstrumentID: instrumentID, Data: data, Version: baseVersion, UpdatedAt: now,
		})
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Doc{}, fmt.Errorf("%w: drawings user=%d instrument=%d base version %d is not current",
			ErrVersionConflict, userID, instrumentID, baseVersion)
	}
	if err != nil {
		return Doc{}, fmt.Errorf("preferences: put drawings user=%d instrument=%d: %w", userID, instrumentID, err)
	}
	t := row.UpdatedAt.UTC()
	return Doc{Data: row.Data, Version: row.Version, UpdatedAt: &t}, nil
}
