package setups

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
)

// Reconcile is this package's one entry point: given the already-
// computed analysis.TradePlan for (instrumentID, timeframe) at asOf, it
// persists whatever spec section 15 says should happen -- creating a
// new setup, appending an amendment, appending a lifecycle transition,
// or nothing at all -- and returns the current setup afterward plus the
// event just appended, if any (nil when nothing changed).
//
// version is used both as this reconciliation's strategy_version
// (identity, spec section 15.3) and its algorithm_version (evidence):
// this codebase has only ever had one such versioning axis
// (analysis.Config.AlgorithmVersion, the same string every other
// evidence table here already records), and section 15.3's "strategy
// version" identity component is satisfied by it until a real
// distinction between the two is needed. Callers pass the same version
// string they already pass to analysis.Engine.
//
// Transactionality: Reconcile does not begin its own transaction --
// store must already be bound to one (store.WithTx), the same contract
// analysis.Engine's own step functions have with analysis.Store, so a
// caller can commit or roll back this call together with whatever
// produced plan (engine.go's own per-candle step is the model to
// follow; see cmd/argus-worker's processing loop for the call site).
//
// plan.Status == PlanNone is always a no-op: spec section 15.1, a
// setup is created only the first time Status != PlanNone, and there
// is no identity to compute from a POI of POINone (identityFrom's own
// precondition) -- so a previously-live identity whose POI stops
// resolving without an invalidation firing first (a case the brief
// that commissioned this package flagged as "shouldn't normally
// happen, but if you hit it, log it and leave the setup as-is") cannot
// actually be detected here: there is no POI to derive that identity
// from any more. Reconcile cannot look up a setup it has no identity
// for; a caller wanting that detection would need to track the
// previous step's identity itself and compare it across calls, which
// is more machinery than this brief asked for. Flagged, not guessed.
func Reconcile(ctx context.Context, s *Store, instrumentID int64, timeframe string, plan analysis.TradePlan, version string, asOf, now time.Time) (Setup, *SetupEvent, error) {
	if plan.Status == analysis.PlanNone {
		return Setup{}, nil, nil
	}

	id, poiRevision, err := identityFrom(plan.POI, version)
	if err != nil {
		return Setup{}, nil, fmt.Errorf("setups: reconcile: %w", err)
	}
	snapshot := newPlanSnapshot(plan)

	existing, err := s.GetByIdentity(ctx, instrumentID, timeframe, id)
	switch {
	case errors.Is(err, ErrSetupNotFound):
		return reconcileNew(ctx, s, instrumentID, timeframe, id, poiRevision, snapshot, plan.Status, version, asOf, now)
	case err != nil:
		return Setup{}, nil, fmt.Errorf("setups: reconcile: %w", err)
	default:
		return reconcileExisting(ctx, s, existing, plan.Status, poiRevision, snapshot, version, asOf, now)
	}
}

// reconcileNew creates a brand-new identity, always starting in
// StateDetected with its paired EventDetected row (spec section 15.1),
// then -- creation state, spec section 15.2, batch 8 -- if status's own
// mapped state is not StateDetected, applies that transition in the
// same step, exactly as reconcileExisting's own "stateChanged" branch
// does: one further event for the mapped state, never the intermediate
// states the plan was never actually observed in. A setup is never left
// persisted in a state that disagrees with the plan it was created
// from.
//
// status is never PlanNone here (Reconcile's own guard); stateFromPlanStatus
// therefore never fails for it.
func reconcileNew(ctx context.Context, s *Store, instrumentID int64, timeframe string, id identity, poiRevision int32, snapshot PlanSnapshot, status analysis.PlanStatus, version string, asOf, now time.Time) (Setup, *SetupEvent, error) {
	created, err := s.Insert(ctx, newSetup{
		InstrumentID:            instrumentID,
		Timeframe:               timeframe,
		Identity:                id,
		State:                   StateDetected,
		POISourceCandleRevision: poiRevision,
		Plan:                    snapshot,
		EvaluationTime:          asOf,
		AlgorithmVersion:        version,
	}, now)
	if err != nil {
		return Setup{}, nil, err
	}

	detected, err := s.InsertEvent(ctx, newSetupEvent{
		SetupID:                 created.ID,
		Type:                    EventDetected,
		Plan:                    snapshot,
		POISourceCandleID:       id.POISourceCandleID,
		POISourceCandleRevision: poiRevision,
		EvaluationTime:          asOf,
		AlgorithmVersion:        version,
	})
	if err != nil {
		return Setup{}, nil, err
	}

	finalState := StateDetected
	lastEvent := detected

	targetState, err := stateFromPlanStatus(status)
	if err != nil {
		return Setup{}, nil, fmt.Errorf("setups: reconcile new setup: %w", err)
	}
	if targetState != StateDetected {
		ev, err := s.InsertEvent(ctx, newSetupEvent{
			SetupID:                 created.ID,
			Type:                    eventTypeFromState(targetState),
			Plan:                    snapshot,
			POISourceCandleID:       id.POISourceCandleID,
			POISourceCandleRevision: poiRevision,
			EvaluationTime:          asOf,
			AlgorithmVersion:        version,
		})
		if err != nil {
			return Setup{}, nil, err
		}
		finalState = targetState
		lastEvent = ev
	}

	updated, err := s.Update(ctx, created.ID, update{
		State:                   finalState,
		POISourceCandleRevision: poiRevision,
		Plan:                    snapshot,
		LastEventID:             lastEvent.ID,
		EvaluationTime:          asOf,
		AlgorithmVersion:        version,
	}, now)
	if err != nil {
		return Setup{}, nil, err
	}
	return updated, &lastEvent, nil
}

// reconcileExisting reconciles a new evaluation step against an
// identity that already has a setups row (spec sections 15.3-15.4): an
// amendment when the POI's source-candle revision changed, a lifecycle
// transition when the mapped state changed, both if both are true in
// the same step, or neither (a pure no-op, the common case -- most
// evaluation steps change nothing for most identities).
//
// status is never PlanNone here (Reconcile's own guard); stateFromPlanStatus
// therefore never fails for it.
func reconcileExisting(ctx context.Context, s *Store, existing Setup, status analysis.PlanStatus, poiRevision int32, snapshot PlanSnapshot, version string, asOf, now time.Time) (Setup, *SetupEvent, error) {
	targetState, err := stateFromPlanStatus(status)
	if err != nil {
		return Setup{}, nil, fmt.Errorf("setups: reconcile existing setup %d: %w", existing.ID, err)
	}

	revisionChanged := existing.POISourceCandleRevision != poiRevision
	// Terminal (spec section 15.3): once invalidated, this identity
	// never transitions again, guarded explicitly rather than trusting
	// every future caller to never re-invoke Reconcile on it.
	terminal := existing.State == StateInvalidated
	stateChanged := !terminal && targetState != existing.State

	if !revisionChanged && !stateChanged {
		return existing, nil, nil
	}

	newState := existing.State
	lastEventID := int64(0)
	if existing.LastEventID != nil {
		lastEventID = *existing.LastEventID
	}
	var lastEvent SetupEvent

	if revisionChanged {
		ev, err := s.InsertEvent(ctx, newSetupEvent{
			SetupID:                 existing.ID,
			Type:                    EventAmendment,
			Plan:                    snapshot,
			POISourceCandleID:       existing.POISourceCandleID,
			POISourceCandleRevision: poiRevision,
			EvaluationTime:          asOf,
			AlgorithmVersion:        version,
		})
		if err != nil {
			return Setup{}, nil, err
		}
		lastEventID = ev.ID
		lastEvent = ev
	}

	if stateChanged {
		newState = targetState
		ev, err := s.InsertEvent(ctx, newSetupEvent{
			SetupID:                 existing.ID,
			Type:                    eventTypeFromState(targetState),
			Plan:                    snapshot,
			POISourceCandleID:       existing.POISourceCandleID,
			POISourceCandleRevision: poiRevision,
			EvaluationTime:          asOf,
			AlgorithmVersion:        version,
		})
		if err != nil {
			return Setup{}, nil, err
		}
		lastEventID = ev.ID
		lastEvent = ev
	}

	updated, err := s.Update(ctx, existing.ID, update{
		State:                   newState,
		POISourceCandleRevision: poiRevision,
		Plan:                    snapshot,
		LastEventID:             lastEventID,
		EvaluationTime:          asOf,
		AlgorithmVersion:        version,
	}, now)
	if err != nil {
		return Setup{}, nil, err
	}
	return updated, &lastEvent, nil
}
