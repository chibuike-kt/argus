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

// GetDrawings returns userID's drawings on (instrumentID, timeframe), or
// [] at version 0.
func (s *Store) GetDrawings(ctx context.Context, userID, instrumentID int64, timeframe string) (Doc, error) {
	row, err := s.q.GetUserDrawings(ctx, store.GetUserDrawingsParams{UserID: userID, InstrumentID: instrumentID, Timeframe: timeframe})
	if errors.Is(err, sql.ErrNoRows) {
		return Doc{Data: emptyArray}, nil
	}
	if err != nil {
		return Doc{}, fmt.Errorf("preferences: get drawings user=%d instrument=%d tf=%s: %w", userID, instrumentID, timeframe, err)
	}
	t := row.UpdatedAt.UTC()
	return Doc{Data: row.Data, Version: row.Version, UpdatedAt: &t}, nil
}

// PutDrawings validates raw and stores it as the next version of the
// drawings on (instrumentID, timeframe), if baseVersion is current.
func (s *Store) PutDrawings(ctx context.Context, userID, instrumentID int64, timeframe string, raw []byte, baseVersion int32) (Doc, error) {
	if !IsTimeframe(timeframe) {
		return Doc{}, fmt.Errorf("%w: timeframe %q", ErrInvalid, timeframe)
	}
	data, err := ValidateDrawings(raw)
	if err != nil {
		return Doc{}, err
	}
	now := s.clk.Now()
	var row store.UserDrawing
	if baseVersion == 0 {
		row, err = s.q.InsertUserDrawings(ctx, store.InsertUserDrawingsParams{
			UserID: userID, InstrumentID: instrumentID, Timeframe: timeframe, Data: data, UpdatedAt: now,
		})
	} else {
		row, err = s.q.UpdateUserDrawings(ctx, store.UpdateUserDrawingsParams{
			UserID: userID, InstrumentID: instrumentID, Timeframe: timeframe, Data: data, Version: baseVersion, UpdatedAt: now,
		})
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Doc{}, fmt.Errorf("%w: drawings user=%d instrument=%d tf=%s base version %d is not current",
			ErrVersionConflict, userID, instrumentID, timeframe, baseVersion)
	}
	if err != nil {
		return Doc{}, fmt.Errorf("preferences: put drawings user=%d instrument=%d tf=%s: %w", userID, instrumentID, timeframe, err)
	}
	t := row.UpdatedAt.UTC()
	return Doc{Data: row.Data, Version: row.Version, UpdatedAt: &t}, nil
}
