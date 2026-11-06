// Package setups is a feature module boundary (see ADR-003).
// Setup lifecycle, invalidation, re-entry and immutable events (ADR-010, ADR-011),
// per docs/strategy/strategy-spec.md section 15.
package setups

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
	"github.com/chibuike-kt/argus/internal/quant"
)

// State is a setup's persisted lifecycle state (spec section 15.2).
// Expired and Cancelled are deliberately not represented here -- v1
// does not implement either (spec section 15.2's own "not implemented
// in v1" note; ADR-010 leaves both open).
type State int

const (
	// StateDetected is the first state a setup is ever created in: the
	// first step where TradePlan.Status is no longer PlanNone for this
	// identity.
	StateDetected State = iota
	// StateActionable mirrors analysis.PlanActionable.
	StateActionable
	// StateTriggered mirrors analysis.PlanTriggered.
	StateTriggered
	// StateInvalidated mirrors analysis.PlanInvalidated. Terminal: an
	// identity that reaches this state never transitions again (spec
	// section 15.3).
	StateInvalidated
)

func (s State) String() string {
	switch s {
	case StateActionable:
		return "actionable"
	case StateTriggered:
		return "triggered"
	case StateInvalidated:
		return "invalidated"
	default:
		return "detected"
	}
}

func parseState(s string) (State, error) {
	switch s {
	case "detected":
		return StateDetected, nil
	case "actionable":
		return StateActionable, nil
	case "triggered":
		return StateTriggered, nil
	case "invalidated":
		return StateInvalidated, nil
	default:
		return 0, fmt.Errorf("setups: unknown state %q", s)
	}
}

// stateFromPlanStatus maps analysis.PlanStatus to the lifecycle state it
// corresponds to once a setup already exists (spec section 15.2's
// table). analysis.PlanNone has no corresponding State -- callers must
// not call this when plan.Status == PlanNone, since there is no
// lifecycle state for "no plan"; Reconcile itself never does.
func stateFromPlanStatus(s analysis.PlanStatus) (State, error) {
	switch s {
	case analysis.PlanActionable:
		return StateActionable, nil
	case analysis.PlanTriggered:
		return StateTriggered, nil
	case analysis.PlanInvalidated:
		return StateInvalidated, nil
	default:
		return 0, fmt.Errorf("setups: plan status %v has no corresponding lifecycle state", s)
	}
}

// EventType distinguishes the five kinds of row setup_events holds
// (spec section 15.4). Unlike State, EventType has no "no setup yet"
// gap to worry about: EventDetected IS the creation event.
type EventType int

const (
	// EventDetected is the creation event: a setup's first-ever row.
	EventDetected EventType = iota
	// EventActionable mirrors a transition into StateActionable.
	EventActionable
	// EventTriggered mirrors a transition into StateTriggered.
	EventTriggered
	// EventInvalidated mirrors a transition into StateInvalidated.
	EventInvalidated
	// EventAmendment is a correction to the same identity's POI source-
	// candle revision, with no lifecycle-state change (spec section
	// 15.3-15.4).
	EventAmendment
)

func (t EventType) String() string {
	switch t {
	case EventActionable:
		return "actionable"
	case EventTriggered:
		return "triggered"
	case EventInvalidated:
		return "invalidated"
	case EventAmendment:
		return "amendment"
	default:
		return "detected"
	}
}

func parseEventType(s string) (EventType, error) {
	switch s {
	case "detected":
		return EventDetected, nil
	case "actionable":
		return EventActionable, nil
	case "triggered":
		return EventTriggered, nil
	case "invalidated":
		return EventInvalidated, nil
	case "amendment":
		return EventAmendment, nil
	default:
		return 0, fmt.Errorf("setups: unknown event type %q", s)
	}
}

// eventTypeFromState is EventType's constructor for a lifecycle
// transition (never for EventAmendment, which Reconcile constructs
// directly): the event fired when a setup (newly created or
// transitioning) reaches state s.
func eventTypeFromState(s State) EventType {
	switch s {
	case StateActionable:
		return EventActionable
	case StateTriggered:
		return EventTriggered
	case StateInvalidated:
		return EventInvalidated
	default:
		return EventDetected
	}
}

// TargetSnapshot is one analysis.Target, JSON-shaped: RR is carried as
// its exact big.Rat string form (RatString, e.g. "3/2"), never a float,
// so the snapshot round-trips the same exact ratio ResolveTradePlan
// computed (guide 13) rather than an approximation of it.
type TargetSnapshot struct {
	Level quant.Price `json:"level"`
	RR    string      `json:"rr"`
}

// POISnapshot is analysis.POI, JSON-shaped -- every field evidence
// requires (ADR-011: "POI and structure evidence") to reconstruct which
// candle, structural swing, and discount/structural checks produced
// this plan.
type POISnapshot struct {
	Type                     string      `json:"type"`
	Direction                string      `json:"direction"`
	Low                      quant.Price `json:"low"`
	High                     quant.Price `json:"high"`
	OrderBlockCandleID       int64       `json:"order_block_candle_id"`
	OrderBlockCandleRevision int32       `json:"order_block_candle_revision"`
	OrderBlockOpenTime       time.Time   `json:"order_block_open_time"`
	QMRFailCandleID          int64       `json:"qmr_fail_candle_id"`
	QMRFailCandleRevision    int32       `json:"qmr_fail_candle_revision"`
	StructuralSwingID        int64       `json:"structural_swing_id"`
	InDiscount               bool        `json:"in_discount"`
}

// PlanSnapshot is analysis.TradePlan, JSON-shaped (spec section 15.4,
// ADR-011: "proposed entry, stop-loss and targets" plus the POI
// evidence that produced them, captured whole at this evaluation step).
type PlanSnapshot struct {
	Status           string           `json:"status"`
	Direction        string           `json:"direction"`
	Entry            quant.Price      `json:"entry"`
	Stop             quant.Price      `json:"stop"`
	Targets          []TargetSnapshot `json:"targets"`
	POI              POISnapshot      `json:"poi"`
	ProtectedSwingID int64            `json:"protected_swing_id"`
}

// newPlanSnapshot converts a resolved TradePlan into its persisted
// shape. Called only with a plan whose Status != PlanNone (Reconcile's
// own precondition for ever persisting anything, spec section 15.1).
func newPlanSnapshot(plan analysis.TradePlan) PlanSnapshot {
	targets := make([]TargetSnapshot, len(plan.Targets))
	for i, t := range plan.Targets {
		rr := "0"
		if t.RR != nil {
			rr = t.RR.RatString()
		}
		targets[i] = TargetSnapshot{Level: t.Level, RR: rr}
	}
	return PlanSnapshot{
		Status:    plan.Status.String(),
		Direction: plan.Direction.String(),
		Entry:     plan.Entry,
		Stop:      plan.Stop,
		Targets:   targets,
		POI: POISnapshot{
			Type:                     plan.POI.Type.String(),
			Direction:                plan.POI.Direction.String(),
			Low:                      plan.POI.Low,
			High:                     plan.POI.High,
			OrderBlockCandleID:       plan.POI.OrderBlockCandleID,
			OrderBlockCandleRevision: plan.POI.OrderBlockCandleRevision,
			OrderBlockOpenTime:       plan.POI.OrderBlockOpenTime,
			QMRFailCandleID:          plan.POI.QMRFailCandleID,
			QMRFailCandleRevision:    plan.POI.QMRFailCandleRevision,
			StructuralSwingID:        plan.POI.StructuralSwingID,
			InDiscount:               plan.POI.InDiscount,
		},
		ProtectedSwingID: plan.ProtectedSwingID,
	}
}

func marshalPlanSnapshot(s PlanSnapshot) (json.RawMessage, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("setups: marshal plan snapshot: %w", err)
	}
	return json.RawMessage(b), nil
}

func unmarshalPlanSnapshot(raw json.RawMessage) (PlanSnapshot, error) {
	var s PlanSnapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return PlanSnapshot{}, fmt.Errorf("setups: unmarshal plan snapshot: %w", err)
	}
	return s, nil
}

// identity is spec section 15.3's candidate identity tuple, minus
// instrument_id/timeframe (callers already have those; Reconcile takes
// them as its own parameters, the same shape analysis.Engine's own
// methods use).
type identity struct {
	POIType           string
	POISourceCandleID int64
	Direction         string
	StrategyVersion   string
}

// identityFrom computes a plan's identity (spec section 15.3).
// poiSourceCandleID/Revision is OrderBlockCandleID/Revision when the
// POI is the order block itself, or QMRFailCandleID/Revision when it is
// a QMR -- section 13's own two source-candle fields, nothing new.
// Callers must not call this when poi.Type == POINone: there is no
// identity for "no POI" (Reconcile itself never does).
func identityFrom(poi analysis.POI, strategyVersion string) (id identity, poiSourceCandleRevision int32, err error) {
	switch poi.Type {
	case analysis.POIOrderBlock:
		return identity{
			POIType:           poi.Type.String(),
			POISourceCandleID: poi.OrderBlockCandleID,
			Direction:         poi.Direction.String(),
			StrategyVersion:   strategyVersion,
		}, poi.OrderBlockCandleRevision, nil
	case analysis.POIQMR:
		return identity{
			POIType:           poi.Type.String(),
			POISourceCandleID: poi.QMRFailCandleID,
			Direction:         poi.Direction.String(),
			StrategyVersion:   strategyVersion,
		}, poi.QMRFailCandleRevision, nil
	default:
		return identity{}, 0, fmt.Errorf("setups: POI type %v has no identity", poi.Type)
	}
}

// Setup is the domain view of a setups row: the mutable current-state
// projection for one identity (spec section 15.3).
type Setup struct {
	ID                      int64
	InstrumentID            int64
	Timeframe               string
	POIType                 string
	POISourceCandleID       int64
	Direction               string
	StrategyVersion         string
	State                   State
	POISourceCandleRevision int32
	Plan                    PlanSnapshot
	LastEventID             *int64
	EvaluationTime          time.Time
	AlgorithmVersion        string
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

// SetupEvent is the domain view of a setup_events row: one immutable
// entry in a setup's history (spec section 15.4).
type SetupEvent struct {
	ID                      int64
	SetupID                 int64
	Type                    EventType
	Plan                    PlanSnapshot
	POISourceCandleID       int64
	POISourceCandleRevision int32
	EvaluationTime          time.Time
	AlgorithmVersion        string
	CreatedAt               time.Time
}
