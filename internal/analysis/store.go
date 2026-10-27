package analysis

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/store"
)

// Store wraps the generated analysis queries (confirmed_swings,
// structure_events, structure_state) -- the same pattern
// instruments.Store and marketdata.Store use over internal/store.
type Store struct {
	q *store.Queries
}

// NewStore wraps db (a *sql.DB or, inside a transaction, a *sql.Tx).
func NewStore(db store.DBTX) *Store {
	return &Store{q: store.New(db)}
}

// WithTx returns a Store bound to tx, so a caller can run several of
// its methods as one atomic unit (Engine's per-candle step: a swing
// change, a structure-event change, and the state update all commit or
// roll back together).
func (s *Store) WithTx(tx *sql.Tx) *Store {
	return &Store{q: s.q.WithTx(tx)}
}

// --- confirmed_swings ---

// GetCurrentSwing returns the current confirmed swing at the given
// position, or ErrSwingNotFound.
func (s *Store) GetCurrentSwing(ctx context.Context, instrumentID int64, timeframe string, swingType SwingType, pivotTime clock.EventTime) (Swing, error) {
	row, err := s.q.GetCurrentSwing(ctx, store.GetCurrentSwingParams{
		InstrumentID: instrumentID,
		Timeframe:    timeframe,
		SwingType:    swingType.String(),
		PivotTime:    pivotTime.Time(),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Swing{}, ErrSwingNotFound
		}
		return Swing{}, fmt.Errorf("analysis: get current swing: %w", err)
	}
	return fromSwingRow(row)
}

// GetSwingByID looks up a swing directly by id (current or superseded),
// used to resolve a structure_state reference pointer back to its price.
func (s *Store) GetSwingByID(ctx context.Context, id int64) (Swing, error) {
	row, err := s.q.GetSwingByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Swing{}, ErrSwingNotFound
		}
		return Swing{}, fmt.Errorf("analysis: get swing by id %d: %w", id, err)
	}
	return fromSwingRow(row)
}

// InsertSwing inserts a new confirmed swing where none currently exists.
func (s *Store) InsertSwing(ctx context.Context, n NewSwing) (Swing, error) {
	row, err := s.q.InsertSwing(ctx, store.InsertSwingParams{
		InstrumentID:     n.InstrumentID,
		Timeframe:        n.Timeframe,
		SwingType:        n.Type.String(),
		PivotTime:        n.PivotTime.Time(),
		Price:            int64(n.Price),
		ConfirmedAt:      n.ConfirmedAt.Time(),
		EvaluationTime:   n.EvaluationTime.Time(),
		AlgorithmVersion: n.AlgorithmVersion,
		SourceCandleIds:  n.SourceCandleIDs,
	})
	if err != nil {
		return Swing{}, fmt.Errorf("analysis: insert swing: %w", err)
	}
	return fromSwingRow(row)
}

// SupersedeSwing replaces old with a new swing (step 1: begin; insert;
// step 2: resolve), or, when replacement is nil, retracts old with no
// replacement (step 1 only -- old never progresses past "superseded,
// unresolved").
func (s *Store) SupersedeSwing(ctx context.Context, oldID int64, now time.Time, replacement *NewSwing) (*Swing, error) {
	affected, err := s.q.BeginSupersedeSwing(ctx, store.BeginSupersedeSwingParams{
		ID:           oldID,
		SupersededAt: nullTimeFrom(now),
	})
	if err != nil {
		return nil, fmt.Errorf("analysis: supersede swing %d step 1 (begin): %w", oldID, err)
	}
	if affected != 1 {
		return nil, fmt.Errorf("analysis: supersede swing %d step 1 (begin): expected to affect 1 row, affected %d (concurrent modification)", oldID, affected)
	}

	if replacement == nil {
		return nil, nil
	}

	newRow, err := s.InsertSwing(ctx, *replacement)
	if err != nil {
		return nil, fmt.Errorf("analysis: supersede swing %d step 2 (insert): %w", oldID, err)
	}

	affected, err = s.q.ResolveSupersedeSwing(ctx, store.ResolveSupersedeSwingParams{
		ID:           oldID,
		SupersededBy: sql.NullInt64{Int64: newRow.ID, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("analysis: supersede swing %d step 3 (resolve): %w", oldID, err)
	}
	if affected != 1 {
		return nil, fmt.Errorf("analysis: supersede swing %d step 3 (resolve): expected to affect 1 row, affected %d", oldID, affected)
	}
	return &newRow, nil
}

// ListLatestSwings returns the latest limit current swings of swingType
// for (instrumentID, timeframe) with pivot_time <= maxPivotTime,
// ordered by pivot_time descending.
//
// maxPivotTime is not optional padding: during a correction replay, a
// query with no upper bound can return a swing the walk has not
// reconciled yet in this pass -- a stale row from before the
// correction that is only still "current" because the replay has not
// reached its position to supersede or retract it. Callers pass the
// latest position their own current processing step is entitled to
// see; see decideEstablishmentCandidate for the exact bound.
func (s *Store) ListLatestSwings(ctx context.Context, instrumentID int64, timeframe string, swingType SwingType, maxPivotTime time.Time, limit int32) ([]Swing, error) {
	rows, err := s.q.ListLatestCurrentSwings(ctx, store.ListLatestCurrentSwingsParams{
		InstrumentID: instrumentID,
		Timeframe:    timeframe,
		SwingType:    swingType.String(),
		PivotTime:    maxPivotTime,
		Limit:        limit,
	})
	if err != nil {
		return nil, fmt.Errorf("analysis: list latest swings: %w", err)
	}
	out := make([]Swing, 0, len(rows))
	for _, row := range rows {
		sw, err := fromSwingRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, sw)
	}
	return out, nil
}

// LatestSwingBefore returns the latest current swing of swingType with
// pivot_time strictly before 'before', or ErrSwingNotFound.
//
// pivot_time is only a safe "already reconciled by the walk" bound
// relative to the POSITION currently being reconciled (reconcileSwing's
// retraction fallback is the one caller: 'before' there is the position
// being reconciled right now, and anything with an earlier pivot_time
// is provably already handled in this same walk). It is NOT safe as a
// bound relative to an arbitrary resume instant like affectedFrom,
// because confirmation lags occurrence by (RightBars+1)*interval -- use
// LatestSwingConfirmedBefore for that (reconstructStateAsOf's case).
func (s *Store) LatestSwingBefore(ctx context.Context, instrumentID int64, timeframe string, swingType SwingType, before time.Time) (Swing, error) {
	row, err := s.q.LatestCurrentSwingBefore(ctx, store.LatestCurrentSwingBeforeParams{
		InstrumentID: instrumentID,
		Timeframe:    timeframe,
		SwingType:    swingType.String(),
		PivotTime:    before,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Swing{}, ErrSwingNotFound
		}
		return Swing{}, fmt.Errorf("analysis: latest swing before %s: %w", before, err)
	}
	return fromSwingRow(row)
}

// LatestSwingConfirmedBefore returns the latest current swing of
// swingType with confirmed_at <= before, or ErrSwingNotFound. Used by
// reconstructStateAsOf to seed a correction replay's resume state: a
// swing only becomes a usable reference once CONFIRMED, not merely
// once it occurs, so the bound here is confirmed_at, never pivot_time.
func (s *Store) LatestSwingConfirmedBefore(ctx context.Context, instrumentID int64, timeframe string, swingType SwingType, before time.Time) (Swing, error) {
	row, err := s.q.LatestCurrentSwingConfirmedBefore(ctx, store.LatestCurrentSwingConfirmedBeforeParams{
		InstrumentID: instrumentID,
		Timeframe:    timeframe,
		SwingType:    swingType.String(),
		ConfirmedAt:  before,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Swing{}, ErrSwingNotFound
		}
		return Swing{}, fmt.Errorf("analysis: latest swing confirmed before %s: %w", before, err)
	}
	return fromSwingRow(row)
}

// --- structure_events ---

// GetCurrentEvent returns the current event for triggerCandleID, or
// ErrEventNotFound.
func (s *Store) GetCurrentEvent(ctx context.Context, instrumentID int64, timeframe string, triggerCandleID int64) (StructureEvent, error) {
	row, err := s.q.GetCurrentEventByTrigger(ctx, store.GetCurrentEventByTriggerParams{
		InstrumentID:    instrumentID,
		Timeframe:       timeframe,
		TriggerCandleID: triggerCandleID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StructureEvent{}, ErrEventNotFound
		}
		return StructureEvent{}, fmt.Errorf("analysis: get current event: %w", err)
	}
	return fromEventRow(row)
}

// InsertEvent inserts a new structure event where none currently exists
// for its trigger candle.
func (s *Store) InsertEvent(ctx context.Context, n NewStructureEvent) (StructureEvent, error) {
	row, err := s.q.InsertStructureEvent(ctx, store.InsertStructureEventParams{
		InstrumentID:      n.InstrumentID,
		Timeframe:         n.Timeframe,
		EventType:         n.Type.String(),
		DirectionBefore:   n.DirectionBefore.String(),
		DirectionAfter:    n.DirectionAfter.String(),
		TriggerCandleID:   n.TriggerCandleID,
		ReferenceSwingIds: n.ReferenceSwingIDs,
		EvaluationTime:    n.EvaluationTime.Time(),
		AlgorithmVersion:  n.AlgorithmVersion,
		SweepValidated:    n.SweepValidated,
		SweepEventID:      ptrToNullInt64(n.SweepEventID),
		ProtectedSwingID:  ptrToNullInt64(n.ProtectedSwingID),
	})
	if err != nil {
		return StructureEvent{}, fmt.Errorf("analysis: insert event: %w", err)
	}
	return fromEventRow(row)
}

// SupersedeEvent is InsertSwing/SupersedeSwing's counterpart for
// structure_events: replace, or (replacement == nil) retract with no
// replacement -- the case step 2 needs for a trigger candle whose close
// no longer produces an event under corrected data.
func (s *Store) SupersedeEvent(ctx context.Context, oldID int64, now time.Time, replacement *NewStructureEvent) (*StructureEvent, error) {
	affected, err := s.q.BeginSupersedeEvent(ctx, store.BeginSupersedeEventParams{
		ID:           oldID,
		SupersededAt: nullTimeFrom(now),
	})
	if err != nil {
		return nil, fmt.Errorf("analysis: supersede event %d step 1 (begin): %w", oldID, err)
	}
	if affected != 1 {
		return nil, fmt.Errorf("analysis: supersede event %d step 1 (begin): expected to affect 1 row, affected %d (concurrent modification)", oldID, affected)
	}

	if replacement == nil {
		return nil, nil
	}

	newRow, err := s.InsertEvent(ctx, *replacement)
	if err != nil {
		return nil, fmt.Errorf("analysis: supersede event %d step 2 (insert): %w", oldID, err)
	}

	affected, err = s.q.ResolveSupersedeEvent(ctx, store.ResolveSupersedeEventParams{
		ID:           oldID,
		SupersededBy: sql.NullInt64{Int64: newRow.ID, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("analysis: supersede event %d step 3 (resolve): %w", oldID, err)
	}
	if affected != 1 {
		return nil, fmt.Errorf("analysis: supersede event %d step 3 (resolve): expected to affect 1 row, affected %d", oldID, affected)
	}
	return &newRow, nil
}

// LatestEventBefore returns the latest current event whose trigger
// candle's open_time is strictly before 'before', or ErrEventNotFound.
// Used only for as-of reconstruction.
func (s *Store) LatestEventBefore(ctx context.Context, instrumentID int64, timeframe string, before time.Time) (StructureEvent, error) {
	row, err := s.q.LatestCurrentEventBefore(ctx, store.LatestCurrentEventBeforeParams{
		InstrumentID: instrumentID,
		Timeframe:    timeframe,
		OpenTime:     before,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StructureEvent{}, ErrEventNotFound
		}
		return StructureEvent{}, fmt.Errorf("analysis: latest event before %s: %w", before, err)
	}
	return fromEventRow(row)
}

// --- liquidity_sweeps ---

// GetCurrentSweepByTrigger returns the current liquidity sweep of side
// at triggerOpenTime, or ErrSweepNotFound. triggerOpenTime, not a
// candle id, is the position key -- stable across a correction to that
// exact candle (see 000006's comment on liquidity_sweeps).
func (s *Store) GetCurrentSweepByTrigger(ctx context.Context, instrumentID int64, timeframe string, triggerOpenTime time.Time, side SweepSide) (Sweep, error) {
	row, err := s.q.GetCurrentSweepByTrigger(ctx, store.GetCurrentSweepByTriggerParams{
		InstrumentID:    instrumentID,
		Timeframe:       timeframe,
		TriggerOpenTime: triggerOpenTime,
		Side:            side.String(),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Sweep{}, ErrSweepNotFound
		}
		return Sweep{}, fmt.Errorf("analysis: get current sweep: %w", err)
	}
	return fromSweepRow(row)
}

// InsertSweep inserts a new liquidity sweep where none currently exists
// for its (trigger open time, side).
func (s *Store) InsertSweep(ctx context.Context, n NewSweep) (Sweep, error) {
	row, err := s.q.InsertSweep(ctx, store.InsertSweepParams{
		InstrumentID:       n.InstrumentID,
		Timeframe:          n.Timeframe,
		Side:               n.Side.String(),
		SweptSwingID:       n.SweptSwingID,
		TriggerOpenTime:    n.TriggerOpenTime,
		TriggerCandleID:    n.TriggerCandleID,
		PenetrationExtreme: int64(n.PenetrationExtreme),
		ReclaimClose:       int64(n.ReclaimClose),
		EvaluationTime:     n.EvaluationTime.Time(),
		AlgorithmVersion:   n.AlgorithmVersion,
	})
	if err != nil {
		return Sweep{}, fmt.Errorf("analysis: insert sweep: %w", err)
	}
	return fromSweepRow(row)
}

// SupersedeSweep is SupersedeSwing/SupersedeEvent's counterpart for
// liquidity_sweeps: replace, or (replacement == nil) retract with no
// replacement -- the case a correction needs when a candle no longer
// sweeps under corrected data.
func (s *Store) SupersedeSweep(ctx context.Context, oldID int64, now time.Time, replacement *NewSweep) (*Sweep, error) {
	affected, err := s.q.BeginSupersedeSweep(ctx, store.BeginSupersedeSweepParams{
		ID:           oldID,
		SupersededAt: nullTimeFrom(now),
	})
	if err != nil {
		return nil, fmt.Errorf("analysis: supersede sweep %d step 1 (begin): %w", oldID, err)
	}
	if affected != 1 {
		return nil, fmt.Errorf("analysis: supersede sweep %d step 1 (begin): expected to affect 1 row, affected %d (concurrent modification)", oldID, affected)
	}

	if replacement == nil {
		return nil, nil
	}

	newRow, err := s.InsertSweep(ctx, *replacement)
	if err != nil {
		return nil, fmt.Errorf("analysis: supersede sweep %d step 2 (insert): %w", oldID, err)
	}

	affected, err = s.q.ResolveSupersedeSweep(ctx, store.ResolveSupersedeSweepParams{
		ID:           oldID,
		SupersededBy: sql.NullInt64{Int64: newRow.ID, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("analysis: supersede sweep %d step 3 (resolve): %w", oldID, err)
	}
	if affected != 1 {
		return nil, fmt.Errorf("analysis: supersede sweep %d step 3 (resolve): expected to affect 1 row, affected %d", oldID, affected)
	}
	return &newRow, nil
}

// --- structure_state ---

// GetState returns the current state projection, or a cold-start
// snapshot if none exists yet (never ErrStateNotFound to the caller --
// "no state yet" and "undetermined, cold start" are the same thing).
func (s *Store) GetState(ctx context.Context, instrumentID int64, timeframe string) (StateSnapshot, error) {
	row, err := s.q.GetStructureState(ctx, store.GetStructureStateParams{
		InstrumentID: instrumentID,
		Timeframe:    timeframe,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return coldStart(instrumentID, timeframe), nil
		}
		return StateSnapshot{}, fmt.Errorf("analysis: get state: %w", err)
	}
	return fromStateRow(row)
}

// SetState upserts the state projection.
func (s *Store) SetState(ctx context.Context, snap StateSnapshot, now time.Time) error {
	_, err := s.q.SetStructureState(ctx, store.SetStructureStateParams{
		InstrumentID:          snap.InstrumentID,
		Timeframe:             snap.Timeframe,
		Direction:             snap.Direction.String(),
		ReferenceSwingHighID:  ptrToNullInt64(snap.ReferenceSwingHighID),
		ReferenceSwingLowID:   ptrToNullInt64(snap.ReferenceSwingLowID),
		LastEventID:           ptrToNullInt64(snap.LastEventID),
		LastProcessedCandleID: ptrToNullInt64(snap.LastProcessedCandleID),
		UpdatedAt:             now,
	})
	if err != nil {
		return fmt.Errorf("analysis: set state: %w", err)
	}
	return nil
}
