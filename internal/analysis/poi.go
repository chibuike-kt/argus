package analysis

import (
	"context"
	"time"

	"github.com/chibuike-kt/argus/internal/quant"
)

// POIType is section 13's selected point-of-interest type. Unlike the
// order block (section 12) and structural liquidity (section 9), POI
// selection has no separate "undefined" outcome: whenever a component
// it depends on is itself undefined (no active structure, no current
// order block, no valid range), that simply fails the relevant
// selection step and POI falls through to POINone, exactly as it does
// for a defined structural-absent structure. POI is always one of
// these three.
type POIType int

const (
	// POINone means the structure offers no POI: no order block, no
	// structural liquidity backing it (the inducement case), or
	// neither candidate POI is in discount.
	POINone POIType = iota
	// POIOrderBlock means the current order block (section 12) itself
	// is the selected POI.
	POIOrderBlock
	// POIQMR means the order block has failed (section 13) and the
	// resulting QMR is the selected POI.
	POIQMR
)

func (t POIType) String() string {
	switch t {
	case POIOrderBlock:
		return "order_block"
	case POIQMR:
		return "qmr"
	default:
		return "none"
	}
}

// POI is the result of section 13's POI selection. The candle/zone/
// evidence fields are only meaningful when Type != POINone.
// QMRFailCandleID and QMRFailCandleRevision are only meaningful when
// Type == POIQMR.
type POI struct {
	Type      POIType
	Direction Direction
	Low       quant.Price
	High      quant.Price

	OrderBlockCandleID       int64
	OrderBlockCandleRevision int32

	QMRFailCandleID       int64
	QMRFailCandleRevision int32

	// StructuralSwingID is the structural-liquidity swing (section 9)
	// that backed this selection -- the QMR's "above structural
	// liquidity" check, or the order-block fallback's structural-present
	// requirement.
	StructuralSwingID int64

	// InDiscount records the discount check (section 8) the selection
	// used: true for both POIOrderBlock and POIQMR (selection requires
	// it), false and not meaningful for POINone.
	InDiscount bool
}

// sweepsBoundary reports whether c sweeps ob's far boundary using
// section 6's sweep semantics, applied to the order-block zone per
// spec section 13: bullish demand block, c.Low strictly below ob.Low
// and c.Close strictly above it; bearish supply block, c.High strictly
// above ob.High and c.Close strictly below it. This is the fail
// trigger that turns an order block into a QMR.
func sweepsBoundary(direction Direction, ob OrderBlock, c CandleInput) bool {
	switch direction {
	case DirectionBullish:
		return c.Low < int64(ob.Low) && c.Close > int64(ob.Low)
	case DirectionBearish:
		return c.High > int64(ob.High) && c.Close < int64(ob.High)
	default:
		return false
	}
}

// leftZone reports whether c trades entirely beyond the zone's near
// boundary relative to the trend -- above ob.High for a bullish demand
// block, below ob.Low for a bearish supply block. This is the first
// half of the spent (already-mitigated) rule: the QMR is spent only
// once price has left the zone this way and then later returned
// (returnedToZone).
func leftZone(direction Direction, ob OrderBlock, c CandleInput) bool {
	switch direction {
	case DirectionBullish:
		return c.Low > int64(ob.High)
	case DirectionBearish:
		return c.High < int64(ob.Low)
	default:
		return false
	}
}

// returnedToZone reports whether c trades back into [ob.Low, ob.High]
// -- the second half of the spent rule. Any overlap counts as a
// return; the spec does not require the whole candle inside the zone,
// only that price is back in it. Direction-independent: the zone's
// bounds are static once the order block is identified.
func returnedToZone(ob OrderBlock, c CandleInput) bool {
	return c.Low <= int64(ob.High) && c.High >= int64(ob.Low)
}

// qmrState is resolveQMR's internal finding: whether the order block
// has failed into a QMR as of asOf, whether that QMR is already spent,
// and (when failed) the fail candle's evidence.
type qmrState struct {
	Failed        bool
	Spent         bool
	FailCandleID  int64
	FailCandleRev int32
}

// resolveQMR walks candles strictly after ob's source candle, up to
// asOf, as a small sequential state machine: first the fail trigger
// (sweepsBoundary), then -- only once failed -- the spent rule's
// leave (leftZone) and later return (returnedToZone). ob.SourceOpenTime
// is used as the scan's lower bound rather than ob.SourceOpenTime plus
// one interval: the order block's own candle can satisfy none of
// sweepsBoundary, leftZone, or returnedToZone against its own zone (its
// low/high define that zone), so including it in the window is
// harmless and avoids a second lookup just to find the next candle's
// open_time.
//
// Point-in-time: every candle is bounded by close_time <= asOf (the
// defensive check below, same discipline as section 12); the fail and
// spent states this returns are therefore exactly what was knowable as
// of asOf, and recompute deterministically as later candles close or a
// correction replays.
func resolveQMR(ctx context.Context, candles CandleSource, state StateSnapshot, ob OrderBlock, asOf time.Time) (qmrState, error) {
	window, err := candles.ListCandles(ctx, state.InstrumentID, ob.SourceOpenTime, asOf)
	if err != nil {
		return qmrState{}, err
	}

	var result qmrState
	left := false
	for _, c := range window {
		if !c.IsClosed || c.CloseTime.After(asOf) {
			continue
		}
		if c.ID == ob.SourceCandleID {
			continue
		}

		if !result.Failed {
			if sweepsBoundary(state.Direction, ob, c) {
				result.Failed = true
				result.FailCandleID = c.ID
				result.FailCandleRev = c.Revision
			}
			continue
		}
		if !left {
			if leftZone(state.Direction, ob, c) {
				left = true
			}
			continue
		}
		if returnedToZone(ob, c) {
			result.Spent = true
			return result, nil
		}
	}
	return result, nil
}

// zoneInDiscount reports whether zone [low, high] sits in the active
// structure's discount half (spec section 8): at or below equilibrium
// for a bullish structure, at or above for bearish. This is the same
// inclusive convention section 9 uses for its own qualifying half, not
// section 8's Classify (which carves out a separate exact-equilibrium
// bucket) -- the whole zone must be on the discount side, so the test
// uses the zone boundary nearer to premium: High for bullish, Low for
// bearish.
func zoneInDiscount(r Range, direction Direction, low, high quant.Price) bool {
	eq := r.Equilibrium()
	switch direction {
	case DirectionBullish:
		return high <= eq
	case DirectionBearish:
		return low >= eq
	default:
		return false
	}
}

// ResolvePOI resolves the active structure's point of interest (spec
// section 13): the QMR when it exists, is unspent, sits in discount,
// and sits above the structural liquidity; otherwise the order block
// when structural liquidity backs it and it sits in discount; otherwise
// POINone. Unlike ResolveOrderBlock and ResolveStructuralLiquidity,
// this never reports "undefined" -- an undefined order block, an
// undefined range, or a structural-absent (inducement) structure all
// simply fail the relevant step and fall through to POINone, so the
// result is always a defined POI.
//
// Point-in-time: ResolveOrderBlock, ResolveStructuralLiquidity, and
// resolveQMR each bound their own candle reads by asOf; ResolvePOI adds
// no further candle reads of its own, so it inherits their discipline
// rather than needing its own.
func ResolvePOI(ctx context.Context, store *Store, candles CandleSource, state StateSnapshot, asOf time.Time, interval time.Duration, minLegSpan int) (POI, error) {
	none := POI{Type: POINone, Direction: state.Direction}

	ob, obOK, err := ResolveOrderBlock(ctx, store, candles, state, asOf)
	if err != nil {
		return POI{}, err
	}
	if !obOK || ob.Status != OrderBlockPresent {
		return none, nil
	}

	structLiq, slOK, err := ResolveStructuralLiquidity(ctx, store, candles, state, asOf, interval, minLegSpan)
	if err != nil {
		return POI{}, err
	}
	structuralPresent := slOK && structLiq.Status == StructuralPresent
	if !structuralPresent {
		// No structural liquidity: the inducement case. Neither the
		// QMR's "above structural liquidity" check nor the order
		// block's structural-present requirement can be satisfied, so
		// there is no POI regardless of the order block or any QMR.
		return none, nil
	}

	activeRange, rangeOK, err := ResolveActiveRange(ctx, store, state)
	if err != nil {
		return POI{}, err
	}
	inDiscount := rangeOK && zoneInDiscount(activeRange, state.Direction, ob.Low, ob.High)

	if inDiscount {
		aboveStructural := false
		switch state.Direction {
		case DirectionBullish:
			aboveStructural = ob.Low > structLiq.Swing.Price
		case DirectionBearish:
			aboveStructural = ob.High < structLiq.Swing.Price
		}
		if aboveStructural {
			qmr, err := resolveQMR(ctx, candles, state, ob, asOf)
			if err != nil {
				return POI{}, err
			}
			if qmr.Failed && !qmr.Spent {
				return POI{
					Type: POIQMR, Direction: state.Direction,
					Low: ob.Low, High: ob.High,
					OrderBlockCandleID: ob.SourceCandleID, OrderBlockCandleRevision: ob.SourceRevision,
					QMRFailCandleID: qmr.FailCandleID, QMRFailCandleRevision: qmr.FailCandleRev,
					StructuralSwingID: structLiq.Swing.ID,
					InDiscount:        true,
				}, nil
			}
		}
	}

	if inDiscount {
		return POI{
			Type: POIOrderBlock, Direction: state.Direction,
			Low: ob.Low, High: ob.High,
			OrderBlockCandleID: ob.SourceCandleID, OrderBlockCandleRevision: ob.SourceRevision,
			StructuralSwingID: structLiq.Swing.ID,
			InDiscount:        true,
		}, nil
	}

	return none, nil
}
