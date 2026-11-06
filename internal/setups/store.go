package setups

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/store"
)

// Store wraps the generated setups queries (setups, setup_events) --
// the same pattern analysis.Store and instruments.Store use over
// internal/store.
type Store struct {
	q *store.Queries
}

// NewStore wraps db (a *sql.DB or, inside a transaction, a *sql.Tx).
func NewStore(db store.DBTX) *Store {
	return &Store{q: store.New(db)}
}

// WithTx returns a Store bound to tx, so Reconcile can run its lookup,
// its setups write, and its setup_events append as one atomic unit, the
// same pattern analysis.Engine's per-candle step uses.
func (s *Store) WithTx(tx *sql.Tx) *Store {
	return &Store{q: s.q.WithTx(tx)}
}

// GetByIdentity returns the current setup for id, or ErrSetupNotFound.
func (s *Store) GetByIdentity(ctx context.Context, instrumentID int64, timeframe string, id identity) (Setup, error) {
	row, err := s.q.GetSetupByIdentity(ctx, store.GetSetupByIdentityParams{
		InstrumentID:      instrumentID,
		Timeframe:         timeframe,
		PoiType:           id.POIType,
		PoiSourceCandleID: id.POISourceCandleID,
		Direction:         id.Direction,
		StrategyVersion:   id.StrategyVersion,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Setup{}, ErrSetupNotFound
		}
		return Setup{}, fmt.Errorf("setups: get by identity: %w", err)
	}
	return fromSetupRow(row)
}

// GetByID returns a setup directly by id, or ErrSetupNotFound.
func (s *Store) GetByID(ctx context.Context, id int64) (Setup, error) {
	row, err := s.q.GetSetupByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Setup{}, ErrSetupNotFound
		}
		return Setup{}, fmt.Errorf("setups: get by id %d: %w", id, err)
	}
	return fromSetupRow(row)
}

// newSetup describes the row to insert for a brand-new identity.
type newSetup struct {
	InstrumentID            int64
	Timeframe               string
	Identity                identity
	State                   State
	POISourceCandleRevision int32
	Plan                    PlanSnapshot
	EvaluationTime          time.Time
	AlgorithmVersion        string
}

// Insert creates the row for a brand-new identity (spec section 15.1).
// LastEventID is set afterward by Update, once the paired event row
// exists -- see Reconcile.
func (s *Store) Insert(ctx context.Context, n newSetup, now time.Time) (Setup, error) {
	snapshot, err := marshalPlanSnapshot(n.Plan)
	if err != nil {
		return Setup{}, err
	}
	row, err := s.q.InsertSetup(ctx, store.InsertSetupParams{
		InstrumentID:            n.InstrumentID,
		Timeframe:               n.Timeframe,
		PoiType:                 n.Identity.POIType,
		PoiSourceCandleID:       n.Identity.POISourceCandleID,
		Direction:               n.Identity.Direction,
		StrategyVersion:         n.Identity.StrategyVersion,
		State:                   n.State.String(),
		PoiSourceCandleRevision: n.POISourceCandleRevision,
		PlanSnapshot:            snapshot,
		EvaluationTime:          n.EvaluationTime,
		AlgorithmVersion:        n.AlgorithmVersion,
		UpdatedAt:               now,
	})
	if err != nil {
		return Setup{}, fmt.Errorf("setups: insert: %w", err)
	}
	return fromSetupRow(row)
}

// update describes the fields a reconciliation step writes back to an
// existing setups row -- always the full projection, since setups is
// written in place (000008's comment on the table), never through a
// supersede handshake.
type update struct {
	State                   State
	POISourceCandleRevision int32
	Plan                    PlanSnapshot
	LastEventID             int64
	EvaluationTime          time.Time
	AlgorithmVersion        string
}

// Update writes u back to setup id. This is setups' one and only write
// path once a row exists, for both a lifecycle transition and an
// amendment -- callers set State unchanged for an amendment.
func (s *Store) Update(ctx context.Context, id int64, u update, now time.Time) (Setup, error) {
	snapshot, err := marshalPlanSnapshot(u.Plan)
	if err != nil {
		return Setup{}, err
	}
	row, err := s.q.UpdateSetup(ctx, store.UpdateSetupParams{
		ID:                      id,
		State:                   u.State.String(),
		PoiSourceCandleRevision: u.POISourceCandleRevision,
		PlanSnapshot:            snapshot,
		LastEventID:             sql.NullInt64{Int64: u.LastEventID, Valid: true},
		EvaluationTime:          u.EvaluationTime,
		AlgorithmVersion:        u.AlgorithmVersion,
		UpdatedAt:               now,
	})
	if err != nil {
		return Setup{}, fmt.Errorf("setups: update %d: %w", id, err)
	}
	return fromSetupRow(row)
}

// newSetupEvent describes an immutable event row to append (spec
// section 15.4).
type newSetupEvent struct {
	SetupID                 int64
	Type                    EventType
	Plan                    PlanSnapshot
	POISourceCandleID       int64
	POISourceCandleRevision int32
	EvaluationTime          time.Time
	AlgorithmVersion        string
}

// InsertEvent appends a new setup_events row. There is no supersede or
// retraction here (000008's comment on the table): every call to this
// is a pure append.
func (s *Store) InsertEvent(ctx context.Context, n newSetupEvent) (SetupEvent, error) {
	snapshot, err := marshalPlanSnapshot(n.Plan)
	if err != nil {
		return SetupEvent{}, err
	}
	row, err := s.q.InsertSetupEvent(ctx, store.InsertSetupEventParams{
		SetupID:                 n.SetupID,
		EventType:               n.Type.String(),
		PlanSnapshot:            snapshot,
		PoiSourceCandleID:       n.POISourceCandleID,
		PoiSourceCandleRevision: n.POISourceCandleRevision,
		EvaluationTime:          n.EvaluationTime,
		AlgorithmVersion:        n.AlgorithmVersion,
	})
	if err != nil {
		return SetupEvent{}, fmt.Errorf("setups: insert event: %w", err)
	}
	return fromSetupEventRow(row)
}

// ListByInstrument lists every setup for (instrumentID, timeframe),
// most recently updated first.
func (s *Store) ListByInstrument(ctx context.Context, instrumentID int64, timeframe string) ([]Setup, error) {
	rows, err := s.q.ListSetupsByInstrument(ctx, store.ListSetupsByInstrumentParams{
		InstrumentID: instrumentID,
		Timeframe:    timeframe,
	})
	if err != nil {
		return nil, fmt.Errorf("setups: list by instrument: %w", err)
	}
	out := make([]Setup, 0, len(rows))
	for _, row := range rows {
		su, err := fromSetupRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, su)
	}
	return out, nil
}

// ListByInstrumentFiltered is ListByInstrument's own filtered sibling
// -- GET /setups' read path (internal/httpapi, worker-hardening brief
// Part C). Same ordering (most recently updated first, unchanged).
// state, when non-nil, restricts the result to that one lifecycle
// state; nil means no filter, matching ListByInstrument's own
// unfiltered behavior exactly. limit is enforced in the query itself.
func (s *Store) ListByInstrumentFiltered(ctx context.Context, instrumentID int64, timeframe string, state *State, limit int32) ([]Setup, error) {
	var stateFilter sql.NullString
	if state != nil {
		stateFilter = sql.NullString{String: state.String(), Valid: true}
	}
	rows, err := s.q.ListSetupsByInstrumentFiltered(ctx, store.ListSetupsByInstrumentFilteredParams{
		InstrumentID: instrumentID, Timeframe: timeframe, State: stateFilter, Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("setups: list by instrument filtered: %w", err)
	}
	out := make([]Setup, 0, len(rows))
	for _, row := range rows {
		su, err := fromSetupRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, su)
	}
	return out, nil
}

// ListEvents returns setupID's full history in creation order (spec
// section 15.4: reconstructing a setup's story).
func (s *Store) ListEvents(ctx context.Context, setupID int64) ([]SetupEvent, error) {
	rows, err := s.q.ListEventsForSetup(ctx, setupID)
	if err != nil {
		return nil, fmt.Errorf("setups: list events for setup %d: %w", setupID, err)
	}
	out := make([]SetupEvent, 0, len(rows))
	for _, row := range rows {
		ev, err := fromSetupEventRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, nil
}

// SetupWithPOIOpenTime pairs a domain Setup with its own POI formation
// candle's open_time -- ListNonTerminalForBreachCheck's own result
// shape (spec section 15.2, v0.12.1's stop-breach invalidation), which
// needs that time to enforce "the breaching candle must be strictly
// later than the setup's own POI formation candle" without a second
// round trip per setup.
type SetupWithPOIOpenTime struct {
	Setup
	POIOpenTime time.Time
}

// ListNonTerminalForBreachCheck returns every non-terminal setup
// (detected, actionable, triggered) for (instrument_id, timeframe),
// each paired with its own POI formation candle's open_time --
// InvalidateBreached's own read path. Invalidated setups are excluded
// by the query itself, not filtered here: that exclusion is this
// function's own terminal guard, the same property
// ListByInstrumentFiltered(state=invalidated) would need a second
// check to get right if it tried to do this job instead.
func (s *Store) ListNonTerminalForBreachCheck(ctx context.Context, instrumentID int64, timeframe string) ([]SetupWithPOIOpenTime, error) {
	rows, err := s.q.ListNonTerminalSetupsForBreachCheck(ctx, store.ListNonTerminalSetupsForBreachCheckParams{
		InstrumentID: instrumentID, Timeframe: timeframe,
	})
	if err != nil {
		return nil, fmt.Errorf("setups: list non-terminal for breach check: %w", err)
	}
	out := make([]SetupWithPOIOpenTime, 0, len(rows))
	for _, row := range rows {
		su, err := fromSetupRow(store.Setup{
			ID:                      row.ID,
			InstrumentID:            row.InstrumentID,
			Timeframe:               row.Timeframe,
			PoiType:                 row.PoiType,
			PoiSourceCandleID:       row.PoiSourceCandleID,
			Direction:               row.Direction,
			StrategyVersion:         row.StrategyVersion,
			State:                   row.State,
			PoiSourceCandleRevision: row.PoiSourceCandleRevision,
			PlanSnapshot:            row.PlanSnapshot,
			LastEventID:             row.LastEventID,
			EvaluationTime:          row.EvaluationTime,
			AlgorithmVersion:        row.AlgorithmVersion,
			CreatedAt:               row.CreatedAt,
			UpdatedAt:               row.UpdatedAt,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, SetupWithPOIOpenTime{Setup: su, POIOpenTime: row.PoiOpenTime})
	}
	return out, nil
}

func fromSetupRow(row store.Setup) (Setup, error) {
	state, err := parseState(row.State)
	if err != nil {
		return Setup{}, fmt.Errorf("setups: setup row %d: %w", row.ID, err)
	}
	plan, err := unmarshalPlanSnapshot(row.PlanSnapshot)
	if err != nil {
		return Setup{}, fmt.Errorf("setups: setup row %d: %w", row.ID, err)
	}
	su := Setup{
		ID:                      row.ID,
		InstrumentID:            row.InstrumentID,
		Timeframe:               row.Timeframe,
		POIType:                 row.PoiType,
		POISourceCandleID:       row.PoiSourceCandleID,
		Direction:               row.Direction,
		StrategyVersion:         row.StrategyVersion,
		State:                   state,
		POISourceCandleRevision: row.PoiSourceCandleRevision,
		Plan:                    plan,
		EvaluationTime:          row.EvaluationTime,
		AlgorithmVersion:        row.AlgorithmVersion,
		CreatedAt:               row.CreatedAt,
		UpdatedAt:               row.UpdatedAt,
	}
	if row.LastEventID.Valid {
		id := row.LastEventID.Int64
		su.LastEventID = &id
	}
	return su, nil
}

func fromSetupEventRow(row store.SetupEvent) (SetupEvent, error) {
	eventType, err := parseEventType(row.EventType)
	if err != nil {
		return SetupEvent{}, fmt.Errorf("setups: setup event row %d: %w", row.ID, err)
	}
	plan, err := unmarshalPlanSnapshot(row.PlanSnapshot)
	if err != nil {
		return SetupEvent{}, fmt.Errorf("setups: setup event row %d: %w", row.ID, err)
	}
	return SetupEvent{
		ID:                      row.ID,
		SetupID:                 row.SetupID,
		Type:                    eventType,
		Plan:                    plan,
		POISourceCandleID:       row.PoiSourceCandleID,
		POISourceCandleRevision: row.PoiSourceCandleRevision,
		EvaluationTime:          row.EvaluationTime,
		AlgorithmVersion:        row.AlgorithmVersion,
		CreatedAt:               row.CreatedAt,
	}, nil
}
