package analysis

import (
	"context"
	"fmt"
	"time"
)

// InducementStatus is section 11's classification: present (with the
// exposed level) or absent. It is exactly the mirror of section 9's
// StructuralLiquidityStatus -- the two are mutually exclusive by
// construction, never independently detected.
type InducementStatus int

const (
	// InducementAbsent means the structure has structural liquidity
	// (StructuralPresent); there is no inducement.
	InducementAbsent InducementStatus = iota
	// InducementPresent means Swing identifies the inducement level.
	InducementPresent
)

func (s InducementStatus) String() string {
	if s == InducementPresent {
		return "inducement-present"
	}
	return "inducement-absent"
}

// InducementLiquidity is the result of classifying the active
// structure's inducement (spec section 11). Swing is only meaningful
// when Status == InducementPresent.
type InducementLiquidity struct {
	Status InducementStatus
	Swing  Swing
}

// ResolveInducementLiquidity resolves the active structure's inducement
// liquidity (spec section 11): the structural-absent branch of section
// 9 with a name and an exposed level, not a separate detector. It adds
// no new detection -- it calls ResolveStructuralLiquidity and relabels
// its result:
//   - StructuralAbsent means inducement is present, and its level is
//     the protected anchor (bullish: the protected low; bearish: the
//     protected high) -- read via the same secondFibLowAnchor/
//     secondFibHighAnchor sources section 9's second fib uses, never a
//     separately derived value.
//   - StructuralPresent means inducement is not present (mutual
//     exclusivity: a structure is exactly one of the two).
//
// ok is false -- undefined, never InducementAbsent -- exactly when
// ResolveStructuralLiquidity's own ok is false (no active structure or
// no second fib). Point-in-time safety is inherited entirely from
// ResolveStructuralLiquidity; asOf is passed through unchanged.
func ResolveInducementLiquidity(ctx context.Context, store *Store, candles CandleSource, state StateSnapshot, asOf time.Time, interval time.Duration, minLegSpan int) (InducementLiquidity, bool, error) {
	structural, ok, err := ResolveStructuralLiquidity(ctx, store, candles, state, asOf, interval, minLegSpan)
	if err != nil || !ok {
		return InducementLiquidity{}, ok, err
	}
	if structural.Status == StructuralPresent {
		return InducementLiquidity{Status: InducementAbsent}, true, nil
	}

	var anchorID *int64
	switch state.Direction {
	case DirectionBullish:
		anchorID = secondFibLowAnchor(state)
	case DirectionBearish:
		anchorID = secondFibHighAnchor(state)
	}
	if anchorID == nil {
		// Invariant violation, not a legitimate outcome: ResolveStructuralLiquidity
		// only returns ok=true when ResolveSecondFib already succeeded,
		// which requires this anchor to be non-nil. CLAUDE.md rule 3 --
		// stop and flag rather than guess at a substitute.
		return InducementLiquidity{}, false, fmt.Errorf("analysis: structural-absent with no protected anchor for direction %s", state.Direction)
	}

	swing, err := store.GetSwingByID(ctx, *anchorID)
	if err != nil {
		return InducementLiquidity{}, false, err
	}
	return InducementLiquidity{Status: InducementPresent, Swing: swing}, true, nil
}
