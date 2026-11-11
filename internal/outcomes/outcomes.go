package outcomes

import (
	"fmt"
	"math/big"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
	"github.com/chibuike-kt/argus/internal/quant"
)

// RulesVersion identifies the simulation rules this package implements
// (strategy spec section 16). Bump it whenever a rule changes; an already
// computed Outcome keeps whatever version it was computed under --
// Outcomes are append only (ADR-011), never rewritten in place by a later
// rules version.
const RulesVersion = "outcome-rules-v1"

// Status is the headline result of simulating a trade plan against
// finalized candle history (spec section 16).
type Status int

const (
	// StatusNeverFilled means no candle ever traded through the entry by
	// at least one tick, and the plan was never invalidated either.
	StatusNeverFilled Status = iota
	// StatusNeverFilledInvalidated means the plan was invalidated before
	// any candle filled it: no trade happened, no loss is recorded.
	StatusNeverFilledInvalidated
	// StatusStopped means the stop level was reached before (or without)
	// any target being reached first.
	StatusStopped
	// StatusTargetHit means at least one target was reached before the
	// stop (or without a stop ever being reached). The headline R is
	// RAtFirstTarget: section 16 assumes the whole position is closed at
	// the first target, no partial exits.
	StatusTargetHit
	// StatusOpen means the plan filled but neither the stop nor any
	// target was reached by the end of the available data.
	StatusOpen
)

// String renders the exact lower_snake_case token the spec and reports
// use.
func (s Status) String() string {
	switch s {
	case StatusNeverFilledInvalidated:
		return "never_filled_invalidated"
	case StatusStopped:
		return "stopped"
	case StatusTargetHit:
		return "target_hit"
	case StatusOpen:
		return "open"
	default:
		return "never_filled"
	}
}

// MarshalText renders Status the same lower_snake_case way String does,
// so a JSON report (encoding/json uses MarshalText when present) carries
// "target_hit", not a bare integer.
func (s Status) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// CandleRef names the specific candle, at the specific revision, that
// produced one fact of an Outcome -- the evidence ADR-011 requires so a
// caller can tell an outcome computed before a correction apart from the
// one computed after it.
type CandleRef struct {
	ID        int64
	OpenTime  time.Time
	CloseTime time.Time
	Revision  int32
}

func candleRef(c analysis.CandleInput) CandleRef {
	return CandleRef{ID: c.ID, OpenTime: c.OpenTime, CloseTime: c.CloseTime, Revision: c.Revision}
}

// TargetFact is one target level's own realized fact: which candle first
// reached it. Index is the target's position in the plan's own Targets
// slice (0 is the nearest target).
type TargetFact struct {
	Index  int
	Candle CandleRef
}

// Outcome is section 16's result of simulating one trade plan against
// finalized candle history as of a point in time.
type Outcome struct {
	Status       Status
	RulesVersion string

	// Fill is the candle that first traded through the entry by at
	// least one tick. Nil for StatusNeverFilled and
	// StatusNeverFilledInvalidated.
	Fill *CandleRef

	// FirstTarget is the first target level reached, and the candle
	// that reached it. Nil unless Status == StatusTargetHit.
	FirstTarget *TargetFact
	// LaterTargets are further targets reached after FirstTarget and
	// before the stop (or before the data ends) -- recorded as evidence;
	// they do not change RAtFirstTarget, since the position is assumed
	// closed in full at the first target (ADR-014: no partial exits).
	LaterTargets []TargetFact
	// RAtFirstTarget is the plan's own R:R for FirstTarget.Index,
	// carried here so a caller never has to re-index into the plan.
	// Nil unless Status == StatusTargetHit.
	RAtFirstTarget *big.Rat

	// Stop is the candle that reached the stop level, when that is what
	// determined the outcome. Nil when a target was already reached
	// first (a later stop touch no longer changes anything: the
	// position was already closed at the first target) and nil for
	// every status other than StatusStopped.
	Stop *CandleRef
	// AmbiguousResolvedAsStop is set when a single candle reached both
	// the stop and a target that had not yet been reached, and the data
	// cannot establish which happened first inside that candle: the
	// conservative rule takes the stop (spec section 16, ADR-014).
	AmbiguousResolvedAsStop bool

	// LastConsideredCandle is the latest finalized candle (close_time
	// <= asOf) Evaluate actually looked at, regardless of Status -- the
	// "candle revision this outcome was computed from" the spec asks
	// every outcome to carry, including never_filled and open, which
	// have no other decisive candle of their own.
	LastConsideredCandle *CandleRef
}

// tradesThrough reports whether c trades through level by at least tick
// in direction (bullish: low <= level-tick; bearish: high >= level+tick).
func tradesThrough(direction analysis.Direction, level, tick quant.Price, c analysis.CandleInput) (bool, error) {
	switch direction {
	case analysis.DirectionBullish:
		threshold, err := level.Sub(tick)
		if err != nil {
			return false, fmt.Errorf("outcomes: entry minus tick: %w", err)
		}
		return c.Low <= int64(threshold), nil
	case analysis.DirectionBearish:
		threshold, err := level.Add(tick)
		if err != nil {
			return false, fmt.Errorf("outcomes: entry plus tick: %w", err)
		}
		return c.High >= int64(threshold), nil
	default:
		return false, ErrDirectionUndetermined
	}
}

// reachesStop reports whether c's range reaches the stop (touch or
// through, no tick margin -- unlike tradesThrough): the stop sits below
// entry for a bullish plan, reached from below (candle low); above entry
// for a bearish plan, reached from above (candle high).
func reachesStop(direction analysis.Direction, stop quant.Price, c analysis.CandleInput) bool {
	switch direction {
	case analysis.DirectionBullish:
		return c.Low <= int64(stop)
	default: // bearish
		return c.High >= int64(stop)
	}
}

// reachesTarget reports whether c's range reaches a target level: targets
// sit above entry for a bullish plan, reached from above (candle high);
// below entry for a bearish plan, reached from below (candle low).
func reachesTarget(direction analysis.Direction, level quant.Price, c analysis.CandleInput) bool {
	switch direction {
	case analysis.DirectionBullish:
		return c.High >= int64(level)
	default: // bearish
		return c.Low <= int64(level)
	}
}

// Evaluate simulates plan against candles (the finalized candles after
// the plan was proposed, in order) as of asOf, following strategy spec
// section 16 exactly. invalidatedAt is the setup lifecycle's own
// invalidation time, if the plan was ever invalidated (nil otherwise);
// tick is the instrument's price tick. candles not yet closed, or whose
// CloseTime is after asOf, are ignored (point in time, never a forming
// candle).
//
// Evaluate is pure: it has no storage of its own. Append-only persistence
// of an Outcome across a later candle correction (ADR-011) is the
// caller's responsibility -- calling Evaluate again with corrected candle
// data simply produces a fresh, independent Outcome.
func Evaluate(plan analysis.TradePlan, candles []analysis.CandleInput, invalidatedAt *time.Time, tick quant.Price, asOf time.Time) (Outcome, error) {
	if plan.Status == analysis.PlanNone {
		return Outcome{}, ErrPlanHasNoStatus
	}
	if plan.Direction != analysis.DirectionBullish && plan.Direction != analysis.DirectionBearish {
		return Outcome{}, ErrDirectionUndetermined
	}

	out := Outcome{RulesVersion: RulesVersion}

	usable := make([]analysis.CandleInput, 0, len(candles))
	for _, c := range candles {
		if !c.IsClosed || c.CloseTime.After(asOf) {
			continue
		}
		usable = append(usable, c)
	}
	if len(usable) > 0 {
		ref := candleRef(usable[len(usable)-1])
		out.LastConsideredCandle = &ref
	}

	fillIdx := -1
	for i, c := range usable {
		filled, err := tradesThrough(plan.Direction, plan.Entry, tick, c)
		if err != nil {
			return Outcome{}, err
		}
		if filled {
			fillIdx = i
			break
		}
	}

	if fillIdx == -1 {
		if invalidatedAt != nil {
			out.Status = StatusNeverFilledInvalidated
		} else {
			out.Status = StatusNeverFilled
		}
		return out, nil
	}

	fillCandle := usable[fillIdx]
	if invalidatedAt != nil && invalidatedAt.Before(fillCandle.CloseTime) {
		out.Status = StatusNeverFilledInvalidated
		return out, nil
	}

	fillRef := candleRef(fillCandle)
	out.Fill = &fillRef

	// On the fill candle itself, only the stop is checked: no target is
	// credited on the fill candle, because the order of events inside
	// the candle is unknown (spec section 16).
	if reachesStop(plan.Direction, plan.Stop, fillCandle) {
		out.Status = StatusStopped
		out.Stop = &fillRef
		return out, nil
	}

	reached := make([]bool, len(plan.Targets))
	for i := fillIdx + 1; i < len(usable); i++ {
		c := usable[i]

		var newlyReached []int
		for ti, t := range plan.Targets {
			if reached[ti] {
				continue
			}
			if reachesTarget(plan.Direction, t.Level, c) {
				newlyReached = append(newlyReached, ti)
			}
		}

		if reachesStop(plan.Direction, plan.Stop, c) {
			// A win already locked in on an earlier candle is not
			// reopened by a later stop touch: the position was already
			// closed in full at the first target (no partial exits, no
			// stop moves -- ADR-014). Only when no target has been
			// reached yet does this stop touch decide the outcome.
			if out.FirstTarget == nil {
				ref := candleRef(c)
				out.Stop = &ref
				if len(newlyReached) > 0 {
					out.AmbiguousResolvedAsStop = true
				}
			}
			break
		}

		for _, ti := range newlyReached {
			reached[ti] = true
			ref := candleRef(c)
			fact := TargetFact{Index: ti, Candle: ref}
			if out.FirstTarget == nil {
				out.FirstTarget = &fact
			} else {
				out.LaterTargets = append(out.LaterTargets, fact)
			}
		}
	}

	switch {
	case out.FirstTarget != nil:
		out.Status = StatusTargetHit
		out.RAtFirstTarget = plan.Targets[out.FirstTarget.Index].RR
	case out.Stop != nil:
		out.Status = StatusStopped
	default:
		out.Status = StatusOpen
	}

	return out, nil
}
