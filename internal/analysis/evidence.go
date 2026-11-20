package analysis

import (
	"context"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/quant"
)

// Evidence chain (product overhaul brief, Phase 6.1, D6): the ordered
// steps of the story behind one trade plan, recorded at decision time
// inside the immutable setup event so the chart and the "Why this setup"
// panel can show it later without re-running history.
//
// Every step is built from what the resolvers themselves used
// (poiTrace, the plan's own targets and anchor), never from a separate,
// parallel lookup that could disagree with the decision. Prices stay
// scaled integers (quant.Price), the internal form plan_snapshot already
// uses; the API renders them at the instrument's scale. Built in a fixed
// order from deterministic inputs, so the same candles give the same
// evidence (ADR-019).

// EvidenceSchemaVersion is the evidence JSON's own shape version.
const EvidenceSchemaVersion = 1

// EvidenceResult is one step's outcome. A setup exists only when every
// required step was met, so required steps record "met"; a step the
// selection tried and rejected (the QMR, when an order block was chosen
// instead) records "not_met" with a reason code.
type EvidenceResult string

// The step results.
const (
	EvidenceMet         EvidenceResult = "met"
	EvidenceNotMet      EvidenceResult = "not_met"
	EvidenceUnevaluable EvidenceResult = "unevaluable"
)

// Step kinds, in story order.
const (
	StepSwing               = "swing"
	StepSweep               = "sweep"
	StepBreak               = "break"
	StepPremiumDiscount     = "premium_discount"
	StepStructuralLiquidity = "structural_liquidity"
	StepQMR                 = "qmr"
	StepPOI                 = "poi"
	StepPlan                = "plan"
)

// EvidenceCandle names a candle a step came from: its open time, and its
// row id and revision when the step records them.
type EvidenceCandle struct {
	Role     string    `json:"role"`
	OpenTime time.Time `json:"open_time"`
	CandleID int64     `json:"candle_id,omitempty"`
	Revision int32     `json:"revision,omitempty"`
}

// EvidenceLevel is a price a step names (a swing's price, a zone edge,
// the equilibrium, entry, stop, a target).
type EvidenceLevel struct {
	Name    string      `json:"name"`
	Price   quant.Price `json:"price"`
	SwingID int64       `json:"swing_id,omitempty"`
	// Time is when the level was set: a swing's pivot candle open time.
	Time *time.Time `json:"time,omitempty"`
}

// EvidenceStep is one step of the story.
type EvidenceStep struct {
	Kind string `json:"kind"`
	// Role tells two steps of the same kind apart ("swept", "protected").
	Role    string         `json:"role,omitempty"`
	Section string         `json:"section"`
	Result  EvidenceResult `json:"result"`
	// Reason is a code for a not_met or unevaluable result.
	Reason  string            `json:"reason,omitempty"`
	Candles []EvidenceCandle  `json:"candles,omitempty"`
	Levels  []EvidenceLevel   `json:"levels,omitempty"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}

// PlanEvidence is the evidence chain for one plan. StrategyVersion and
// AlgorithmVersion are set by the setups layer, which knows them.
type PlanEvidence struct {
	Schema           int            `json:"schema"`
	InstrumentID     int64          `json:"instrument_id"`
	Timeframe        string         `json:"timeframe"`
	AsOf             time.Time      `json:"as_of"`
	Direction        string         `json:"direction"`
	StrategyVersion  string         `json:"strategy_version,omitempty"`
	AlgorithmVersion string         `json:"algorithm_version,omitempty"`
	Steps            []EvidenceStep `json:"steps"`
}

// targetSource is one target level and where it came from (spec section
// 14: the confirmed opposing swings, or transactional liquidity, section
// 10).
type targetSource struct {
	Price  quant.Price
	Swing  Swing
	Source string // "opposing_swing" or "transactional_liquidity"
}

func swingLevel(name string, sw Swing) EvidenceLevel {
	t := sw.PivotTime.Time()
	return EvidenceLevel{Name: name, Price: sw.Price, SwingID: sw.ID, Time: &t}
}

// zoneWord is section 8's side for direction: the buy zone of a bullish
// structure is discount, the sell zone of a bearish one is premium.
func zoneWord(d Direction) string {
	if d == DirectionBearish {
		return "premium"
	}
	return "discount"
}

// buildEvidence assembles the chain for a plan from the trace of what its
// resolution used. It reads only rows the decision itself referenced (the
// break's sweep, the swings named by id), by id.
func buildEvidence(ctx context.Context, store *Store, state StateSnapshot, asOf time.Time, poi POI, tr poiTrace, anchor Swing, entry, stop quant.Price, targets []Target, sources []targetSource) (*PlanEvidence, error) {
	ev := &PlanEvidence{
		Schema:       EvidenceSchemaVersion,
		InstrumentID: state.InstrumentID,
		Timeframe:    state.Timeframe,
		AsOf:         asOf.UTC(),
		Direction:    poi.Direction.String(),
	}
	brk := tr.orderBlock.breakEvent

	// 1-2. The sweep that validated the break, and the swing it took.
	if brk.SweepEventID != nil {
		sweep, err := store.GetSweepByID(ctx, *brk.SweepEventID)
		if err != nil {
			return nil, fmt.Errorf("analysis: evidence: sweep %d: %w", *brk.SweepEventID, err)
		}
		swept, err := store.GetSwingByID(ctx, sweep.SweptSwingID)
		if err != nil {
			return nil, fmt.Errorf("analysis: evidence: swept swing %d: %w", sweep.SweptSwingID, err)
		}
		ev.Steps = append(ev.Steps,
			EvidenceStep{
				Kind: StepSwing, Role: "swept", Section: "1", Result: EvidenceMet,
				Candles: []EvidenceCandle{{Role: "pivot", OpenTime: swept.PivotTime.Time()}},
				Levels:  []EvidenceLevel{swingLevel("swing", swept)},
				Attrs:   map[string]string{"swing_type": swept.Type.String()},
			},
			EvidenceStep{
				Kind: StepSweep, Section: "6", Result: EvidenceMet,
				Candles: []EvidenceCandle{{Role: "trigger", OpenTime: sweep.TriggerOpenTime, CandleID: sweep.TriggerCandleID}},
				Levels: []EvidenceLevel{
					swingLevel("swept_level", swept),
					{Name: "penetration_extreme", Price: sweep.PenetrationExtreme},
					{Name: "reclaim_close", Price: sweep.ReclaimClose},
				},
				Attrs: map[string]string{"side": sweep.Side.String()},
			},
		)
	} else {
		// The break the order block used is sweep-validated by
		// construction (LatestSweepValidatedEventKnownAsOf), so a missing
		// sweep id is recorded as unevaluable, never filled in.
		ev.Steps = append(ev.Steps, EvidenceStep{Kind: StepSweep, Section: "6", Result: EvidenceUnevaluable, Reason: "break_has_no_sweep_id"})
	}

	// 3. The protected swing: the structure's origin and the plan's stop.
	ev.Steps = append(ev.Steps, EvidenceStep{
		Kind: StepSwing, Role: "protected", Section: "7", Result: EvidenceMet,
		Candles: []EvidenceCandle{{Role: "pivot", OpenTime: anchor.PivotTime.Time()}},
		Levels:  []EvidenceLevel{swingLevel("swing", anchor)},
		Attrs:   map[string]string{"swing_type": anchor.Type.String()},
	})

	// 4. The break the order block was anchored to (the one this code
	// used: ResolveOrderBlock's LatestSweepValidatedEventKnownAsOf).
	breakStep := EvidenceStep{
		Kind: StepBreak, Section: "7", Result: EvidenceMet,
		Candles: []EvidenceCandle{{Role: "trigger", OpenTime: brk.TriggerOpenTime, CandleID: brk.TriggerCandleID}},
		Attrs: map[string]string{
			"break_type":       brk.Type.String(),
			"direction_before": brk.DirectionBefore.String(),
			"direction_after":  brk.DirectionAfter.String(),
			"structure_event":  fmt.Sprint(brk.ID),
		},
	}
	for i, id := range brk.ReferenceSwingIDs {
		sw, err := store.GetSwingByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("analysis: evidence: broken swing %d: %w", id, err)
		}
		name := "broken_swing"
		if i > 0 {
			name = fmt.Sprintf("broken_swing_%d", i+1)
		}
		breakStep.Levels = append(breakStep.Levels, swingLevel(name, sw))
	}
	ev.Steps = append(ev.Steps, breakStep)

	// 5. Premium or discount of the active range.
	if tr.rangeOK && tr.rangeLowID != nil && tr.rangeHighID != nil {
		low, err := store.GetSwingByID(ctx, *tr.rangeLowID)
		if err != nil {
			return nil, fmt.Errorf("analysis: evidence: range low %d: %w", *tr.rangeLowID, err)
		}
		high, err := store.GetSwingByID(ctx, *tr.rangeHighID)
		if err != nil {
			return nil, fmt.Errorf("analysis: evidence: range high %d: %w", *tr.rangeHighID, err)
		}
		res := EvidenceMet
		if !tr.inDiscount {
			res = EvidenceNotMet
		}
		ev.Steps = append(ev.Steps, EvidenceStep{
			Kind: StepPremiumDiscount, Section: "8", Result: res,
			Levels: []EvidenceLevel{
				swingLevel("range_low", low),
				swingLevel("range_high", high),
				{Name: "equilibrium", Price: tr.activeRange.Equilibrium()},
			},
			Attrs: map[string]string{"zone": zoneWord(poi.Direction)},
		})
	} else {
		ev.Steps = append(ev.Steps, EvidenceStep{Kind: StepPremiumDiscount, Section: "8", Result: EvidenceUnevaluable, Reason: "no_active_range"})
	}

	// 6. Structural liquidity backing the zone.
	ev.Steps = append(ev.Steps, EvidenceStep{
		Kind: StepStructuralLiquidity, Section: "9", Result: EvidenceMet,
		Candles: []EvidenceCandle{{Role: "pivot", OpenTime: tr.structural.Swing.PivotTime.Time()}},
		Levels:  []EvidenceLevel{swingLevel("structural_swing", tr.structural.Swing)},
	})

	// 7. The QMR check (section 13 tries it before the order block).
	qmr := EvidenceStep{Kind: StepQMR, Section: "13"}
	switch {
	case poi.Type == POIQMR:
		qmr.Result = EvidenceMet
		qmr.Candles = []EvidenceCandle{{Role: "fail", CandleID: poi.QMRFailCandleID, Revision: poi.QMRFailCandleRevision}}
	case !tr.aboveStructural:
		qmr.Result, qmr.Reason = EvidenceNotMet, "zone_not_beyond_structural_liquidity"
	case tr.qmrChecked && !tr.qmr.Failed:
		qmr.Result, qmr.Reason = EvidenceNotMet, "order_block_not_failed"
	case tr.qmrChecked && tr.qmr.Spent:
		qmr.Result, qmr.Reason = EvidenceNotMet, "qmr_spent"
	default:
		qmr.Result, qmr.Reason = EvidenceUnevaluable, "qmr_not_checked"
	}
	ev.Steps = append(ev.Steps, qmr)

	// 8. The point of interest itself.
	ev.Steps = append(ev.Steps, EvidenceStep{
		Kind: StepPOI, Section: "12", Result: EvidenceMet,
		Candles: []EvidenceCandle{{Role: "formation", OpenTime: poi.OrderBlockOpenTime, CandleID: poi.OrderBlockCandleID, Revision: poi.OrderBlockCandleRevision}},
		Levels: []EvidenceLevel{
			{Name: "zone_low", Price: poi.Low},
			{Name: "zone_high", Price: poi.High},
		},
		Attrs: map[string]string{"poi_type": poi.Type.String()},
	})

	// 9. The plan: entry at the zone edge, stop at the protected swing,
	// each target with the swing it came from and its R:R.
	plan := EvidenceStep{
		Kind: StepPlan, Section: "14", Result: EvidenceMet,
		Levels: []EvidenceLevel{
			{Name: "entry", Price: entry},
			{Name: "stop", Price: stop, SwingID: anchor.ID},
		},
		Attrs: map[string]string{},
	}
	for i, t := range targets {
		lvl := EvidenceLevel{Name: fmt.Sprintf("target_%d", i+1), Price: t.Level}
		if i < len(sources) {
			lvl.SwingID = sources[i].Swing.ID
			pt := sources[i].Swing.PivotTime.Time()
			lvl.Time = &pt
			plan.Attrs[fmt.Sprintf("target_%d_source", i+1)] = sources[i].Source
		}
		if t.RR != nil {
			plan.Attrs[fmt.Sprintf("target_%d_rr", i+1)] = t.RR.RatString()
		}
		plan.Levels = append(plan.Levels, lvl)
	}
	ev.Steps = append(ev.Steps, plan)
	return ev, nil
}
