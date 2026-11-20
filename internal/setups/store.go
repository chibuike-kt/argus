package setups

import (
	"context"
	"database/sql"
	"encoding/json"
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
	FoundMode               FoundMode
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
		FoundMode:               string(foundModeOrUnknown(n.FoundMode)),
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
	// Evidence is the chain to record with this event; nil records none.
	Evidence json.RawMessage
}

func foundModeOrUnknown(m FoundMode) FoundMode {
	if m == "" {
		return FoundUnknown
	}
	return m
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
		Evidence:                evidenceOrNull(n.Evidence),
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
			FoundMode:               row.FoundMode,
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
	mode, err := parseFoundMode(row.FoundMode)
	if err != nil {
		return Setup{}, fmt.Errorf("setups: setup row %d: %w", row.ID, err)
	}
	su.FoundMode = mode
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
		Evidence:                recordedEvidence(row.Evidence),
	}, nil
}

// recordedEvidence is the stored evidence, or nil for "not recorded"
// (the JSON null default).
func recordedEvidence(e json.RawMessage) json.RawMessage {
	if len(e) == 0 || string(e) == "null" {
		return nil
	}
	return e
}

func evidenceOrNull(e json.RawMessage) json.RawMessage {
	if len(e) == 0 {
		return evidenceJSONNull
	}
	return e
}

// LatestEvidence returns setupID's newest recorded evidence and the event
// that carried it; ok is false when none was recorded (every setup
// created before Phase 6.1).
func (s *Store) LatestEvidence(ctx context.Context, setupID int64) (evidence json.RawMessage, eventType string, at time.Time, ok bool, err error) {
	row, err := s.q.GetLatestSetupEvidence(ctx, setupID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", time.Time{}, false, nil
	}
	if err != nil {
		return nil, "", time.Time{}, false, fmt.Errorf("setups: latest evidence %d: %w", setupID, err)
	}
	return row.Evidence, row.EventType, row.EvaluationTime.UTC(), true, nil
}

// Lifecycle is when a setup was detected, triggered and invalidated (its
// events' evaluation times; nil when that never happened) and whether
// any of its events recorded evidence.
type Lifecycle struct {
	DetectedAt       *time.Time
	TriggeredAt      *time.Time
	InvalidatedAt    *time.Time
	EvidenceRecorded bool
}

// LifecycleByID summarises setupIDs' event histories in one statement.
// A setup with no events is absent from the map.
func (s *Store) LifecycleByID(ctx context.Context, setupIDs []int64) (map[int64]Lifecycle, error) {
	out := make(map[int64]Lifecycle, len(setupIDs))
	if len(setupIDs) == 0 {
		return out, nil
	}
	rows, err := s.q.ListSetupLifecycle(ctx, setupIDs)
	if err != nil {
		return nil, fmt.Errorf("setups: lifecycle: %w", err)
	}
	at := func(has bool, t time.Time) *time.Time {
		if !has {
			return nil
		}
		u := t.UTC()
		return &u
	}
	for _, r := range rows {
		out[r.SetupID] = Lifecycle{
			DetectedAt:       at(r.HasDetected, r.DetectedAt),
			TriggeredAt:      at(r.HasTriggered, r.TriggeredAt),
			InvalidatedAt:    at(r.HasInvalidated, r.InvalidatedAt),
			EvidenceRecorded: r.EvidenceRecorded,
		}
	}
	return out, nil
}

// RecordedEvidence is one setup's newest recorded evidence.
type RecordedEvidence struct {
	Evidence  json.RawMessage
	EventType string
	At        time.Time
}

// LatestEvidenceByID is LatestEvidence for many setups in one statement;
// a setup with none recorded is absent from the map.
func (s *Store) LatestEvidenceByID(ctx context.Context, setupIDs []int64) (map[int64]RecordedEvidence, error) {
	out := make(map[int64]RecordedEvidence, len(setupIDs))
	if len(setupIDs) == 0 {
		return out, nil
	}
	rows, err := s.q.ListLatestSetupEvidence(ctx, setupIDs)
	if err != nil {
		return nil, fmt.Errorf("setups: latest evidence: %w", err)
	}
	for _, r := range rows {
		out[r.SetupID] = RecordedEvidence{Evidence: r.Evidence, EventType: r.EventType, At: r.EvaluationTime.UTC()}
	}
	return out, nil
}
