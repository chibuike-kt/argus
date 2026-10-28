package analysis

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DefaultMinLegSpan is the shape test's N (spec section 9): each of a
// candidate's two legs must span at least this many candles. The spec
// marks this value itself provisional ("a backtesting tuning knob"),
// never the shape of the rule, so it is a plain exported constant
// callers can override, not a literal buried in the walk.
const DefaultMinLegSpan = 3

// StructuralLiquidityStatus is section 9's classification: present
// (with the identified swing) or absent (the inducement case handled
// in a later batch).
type StructuralLiquidityStatus int

const (
	// StructuralAbsent means no confirmed swing in the qualifying half
	// of the second fib is both intact and shape-qualifying. This is a
	// defined, legitimate classification -- distinct from "undefined"
	// (ResolveStructuralLiquidity's ok == false), which means there was
	// no second fib to classify against at all.
	StructuralAbsent StructuralLiquidityStatus = iota
	// StructuralPresent means Swing identifies the structural liquidity.
	StructuralPresent
)

func (s StructuralLiquidityStatus) String() string {
	if s == StructuralPresent {
		return "structural-present"
	}
	return "structural-absent"
}

// StructuralLiquidity is the result of classifying the active
// structure's second fib (spec section 9). Swing is only meaningful
// when Status == StructuralPresent.
type StructuralLiquidity struct {
	Status StructuralLiquidityStatus
	Swing  Swing
}

// secondFibLowAnchor resolves the second fib's low anchor for a
// bullish structure (spec section 9): "[Provisional] The low anchor
// (bullish) is the protected low, the reversal-origin extreme, not the
// swept swing." This is the one open item the spec flags as still to
// verify against a marked chart -- kept as this single, clearly-named
// source so swapping it for the swept swing (via the sweep event
// EarliestSweepValidatedEventByProtectedSwing's SweepEventID points at)
// is a one-line change here, not a hunt through the call sites below.
func secondFibLowAnchor(state StateSnapshot) *int64 {
	return state.ProtectedLowSwingID
}

// secondFibHighAnchor is secondFibLowAnchor's bearish mirror: the
// protected high, provisionally, per the same spec note.
func secondFibHighAnchor(state StateSnapshot) *int64 {
	return state.ProtectedHighSwingID
}

// ResolveSecondFib resolves the active structure's second fib (spec
// section 9): bullish is [protected low, broken swing high]; bearish is
// [broken swing low, protected high]. The broken swing is found from
// the earliest current, sweep-validated structure event whose
// protected_swing_id is the trend-side protected anchor -- the break
// that first used this anchor, per spec ("an establishing CHOCH plus
// continuation BOSs all referencing this anchor: use the establishing
// one, earliest by trigger time"). ok is false -- undefined, never
// StructuralAbsent -- when:
//   - direction is UNDETERMINED,
//   - the trend-side protected anchor is absent (read directly from
//     state via secondFibLowAnchor/secondFibHighAnchor, never
//     substituted with a different swing when absent),
//   - no sweep-validated break currently references that anchor (the
//     anchor may have been established or superseded without any break
//     having occurred against it yet), or
//   - the two endpoints do not form a positive range (High > Low fails).
func ResolveSecondFib(ctx context.Context, store *Store, state StateSnapshot) (Range, bool, error) {
	var anchorID *int64
	switch state.Direction {
	case DirectionBullish:
		anchorID = secondFibLowAnchor(state)
	case DirectionBearish:
		anchorID = secondFibHighAnchor(state)
	default: // DirectionUndetermined
		return Range{}, false, nil
	}
	if anchorID == nil {
		return Range{}, false, nil
	}

	breakEvent, err := store.EarliestSweepValidatedEventByProtectedSwing(ctx, state.InstrumentID, state.Timeframe, *anchorID)
	switch {
	case errors.Is(err, ErrEventNotFound):
		return Range{}, false, nil
	case err != nil:
		return Range{}, false, err
	}
	if len(breakEvent.ReferenceSwingIDs) == 0 {
		// Invariant violation, not a legitimate "undefined" outcome: a
		// sweep-validated event is always a BOS or CHOCH (establishment
		// is never tagged), and every BOS/CHOCH records exactly one
		// reference swing id (decideBreak's single refID). CLAUDE.md
		// rule 3 -- stop and flag rather than guess at a substitute.
		return Range{}, false, fmt.Errorf("analysis: sweep-validated event %d has no reference swing id", breakEvent.ID)
	}
	brokenSwingID := breakEvent.ReferenceSwingIDs[0]

	var lowID, highID int64
	switch state.Direction {
	case DirectionBullish:
		lowID, highID = *anchorID, brokenSwingID
	default: // DirectionBearish
		lowID, highID = brokenSwingID, *anchorID
	}

	low, err := store.GetSwingByID(ctx, lowID)
	if err != nil {
		return Range{}, false, err
	}
	high, err := store.GetSwingByID(ctx, highID)
	if err != nil {
		return Range{}, false, err
	}
	if high.Price <= low.Price {
		return Range{}, false, nil
	}
	return Range{Low: low.Price, High: high.Price}, true, nil
}

// passesLegSpanShapeTest is section 9's shape test: a candidate's two
// legs (the move in from the preceding opposing swing, and the move out
// to the following opposing swing) must each span at least minLegSpan
// candles, counted as whole intervals between the two pivot times. A
// single-candle extreme -- adjacent pivots one interval apart -- spans
// 1 and never qualifies against any minLegSpan >= 2.
func passesLegSpanShapeTest(precedingPivot, candidatePivot, followingPivot time.Time, interval time.Duration, minLegSpan int) bool {
	downLeg := int(candidatePivot.Sub(precedingPivot) / interval)
	upLeg := int(followingPivot.Sub(candidatePivot) / interval)
	return downLeg >= minLegSpan && upLeg >= minLegSpan
}

// isIntact is section 9's intactness check: no finalized candle with
// open_time at or after the swing's confirmation has traded through it
// (low below the swing's price for a bullish candidate, high above for
// a bearish one). candles must cover [swing.ConfirmedAt, asOf) and need
// not be pre-filtered to closed candles; isIntact does that itself, so
// a still-forming candle at the live edge never disqualifies a swing
// that is otherwise intact (no look-ahead either way, guide 16).
func isIntact(swingType SwingType, price int64, candles []CandleInput) bool {
	for _, c := range candles {
		if !c.IsClosed {
			continue
		}
		if swingType == SwingLow && c.Low < price {
			return false
		}
		if swingType == SwingHigh && c.High > price {
			return false
		}
	}
	return true
}

// structuralLiquidityScanBatch bounds each ListLatestSwings page while
// walking candidates back through time; evaluateStructuralCandidates
// pages further back only when a batch is exhausted without a
// qualifying swing, so this only trades DB round trips for memory on a
// structure with an unusually long candidate tail.
const structuralLiquidityScanBatch = 50

// evaluateStructuralCandidates walks confirmed swings of swingType,
// most recent first, returning the first one that is in the qualifying
// half of fib, intact, and shape-qualifying -- section 9's "most recent
// confirmed swing low [or high], by pivot time, that is all of (a), (b),
// (c)". oppositeType is the type used for the shape test's leg
// endpoints (SwingHigh for a bullish/low candidate, SwingLow for a
// bearish/high candidate).
func evaluateStructuralCandidates(ctx context.Context, s *Store, candles CandleSource, instrumentID int64, timeframe string, swingType, oppositeType SwingType, asOf time.Time, interval time.Duration, minLegSpan int, inQualifyingHalf func(price int64) bool) (Swing, bool, error) {
	cursor := asOf
	for {
		// Point-in-time safe: confirmed_at <= asOf, not merely
		// pivot_time <= cursor. A swing can pivot before asOf but
		// confirm after it (confirmation lags the pivot by
		// RightBars+1 intervals); without this bound such a candidate
		// would reach isIntact with an empty [ConfirmedAt, asOf) scan
		// range and pass vacuously, wrongly appearing both confirmed
		// and intact as of an instant before it was actually known.
		batch, err := s.ListLatestSwingsConfirmedBefore(ctx, instrumentID, timeframe, swingType, cursor, asOf, structuralLiquidityScanBatch)
		if err != nil {
			return Swing{}, false, err
		}
		if len(batch) == 0 {
			return Swing{}, false, nil
		}

		for _, candidate := range batch {
			if !inQualifyingHalf(int64(candidate.Price)) {
				continue
			}

			// asOf here is not load-bearing (the candidate above is
			// already confirmed by asOf, and confirmation never
			// precedes pivot, so the preceding swing is necessarily
			// known by asOf too) -- added for symmetry with the
			// following-swing bound below, so the invariant is
			// explicit rather than accidental.
			preceding, err := s.LatestSwingBeforeKnownAsOf(ctx, instrumentID, timeframe, oppositeType, candidate.PivotTime.Time(), asOf)
			if errors.Is(err, ErrSwingNotFound) {
				continue // no down-leg yet: cannot satisfy the shape test
			}
			if err != nil {
				return Swing{}, false, err
			}
			// Point-in-time safe: the up-leg endpoint must itself be
			// known as of asOf, not merely pivoting after the
			// candidate -- otherwise the shape test could complete
			// using a swing that, at a historical asOf, has not yet
			// confirmed (no look-ahead, guide 16).
			following, err := s.SwingAfter(ctx, instrumentID, timeframe, oppositeType, candidate.PivotTime.Time(), asOf)
			if errors.Is(err, ErrSwingNotFound) {
				continue // no up-leg yet: cannot satisfy the shape test
			}
			if err != nil {
				return Swing{}, false, err
			}
			if !passesLegSpanShapeTest(preceding.PivotTime.Time(), candidate.PivotTime.Time(), following.PivotTime.Time(), interval, minLegSpan) {
				continue
			}

			scanCandles, err := candles.ListCandles(ctx, instrumentID, candidate.ConfirmedAt.Time(), asOf)
			if err != nil {
				return Swing{}, false, err
			}
			if !isIntact(swingType, int64(candidate.Price), scanCandles) {
				continue
			}

			return candidate, true, nil
		}

		if len(batch) < structuralLiquidityScanBatch {
			return Swing{}, false, nil
		}
		oldest := batch[len(batch)-1]
		prior, err := s.LatestSwingBefore(ctx, instrumentID, timeframe, swingType, oldest.PivotTime.Time())
		if errors.Is(err, ErrSwingNotFound) {
			return Swing{}, false, nil
		}
		if err != nil {
			return Swing{}, false, err
		}
		cursor = prior.PivotTime.Time()
	}
}

// ResolveStructuralLiquidity is the exported function downstream (POI,
// setup/entry) calls for the active structure's structural liquidity
// (spec section 9). asOf bounds both the intactness scan's end and
// which confirmed swings are eligible to be "the most recent" --
// callers pass the evaluation point explicitly (guide 13/16: time that
// affects logic is never read ambiently). ok is false only when there
// is no valid active structure or second fib (ResolveSecondFib's own
// guardrails); once a second fib exists, the result is always a
// defined StructuralLiquidity (present or absent), never undefined.
func ResolveStructuralLiquidity(ctx context.Context, store *Store, candles CandleSource, state StateSnapshot, asOf time.Time, interval time.Duration, minLegSpan int) (StructuralLiquidity, bool, error) {
	fib, ok, err := ResolveSecondFib(ctx, store, state)
	if err != nil || !ok {
		return StructuralLiquidity{}, ok, err
	}
	eq := fib.Equilibrium()

	var swingType, oppositeType SwingType
	var inQualifyingHalf func(price int64) bool
	switch state.Direction {
	case DirectionBullish:
		swingType, oppositeType = SwingLow, SwingHigh
		inQualifyingHalf = func(price int64) bool { return price <= int64(eq) }
	default: // DirectionBearish (ResolveSecondFib already excluded Undetermined)
		swingType, oppositeType = SwingHigh, SwingLow
		inQualifyingHalf = func(price int64) bool { return price >= int64(eq) }
	}

	candidate, found, err := evaluateStructuralCandidates(ctx, store, candles, state.InstrumentID, state.Timeframe, swingType, oppositeType, asOf, interval, minLegSpan, inQualifyingHalf)
	if err != nil {
		return StructuralLiquidity{}, false, err
	}
	if !found {
		return StructuralLiquidity{Status: StructuralAbsent}, true, nil
	}
	return StructuralLiquidity{Status: StructuralPresent, Swing: candidate}, true, nil
}
