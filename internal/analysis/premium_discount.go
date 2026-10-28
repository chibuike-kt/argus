package analysis

import (
	"context"

	"github.com/chibuike-kt/argus/internal/quant"
)

// Zone classifies a price relative to a structure's equilibrium (spec
// section 8).
type Zone int

const (
	// ZoneDiscount is strictly below equilibrium.
	ZoneDiscount Zone = iota
	// ZonePremium is strictly above equilibrium.
	ZonePremium
	// ZoneEquilibrium is exactly at equilibrium -- neither discount nor premium.
	ZoneEquilibrium
)

func (z Zone) String() string {
	switch z {
	case ZonePremium:
		return "premium"
	case ZoneEquilibrium:
		return "equilibrium"
	default:
		return "discount"
	}
}

// Range is the active structure's premium/discount range (spec section
// 8): in a bullish structure, [protected low, latest confirmed swing
// high]; bearish, [latest confirmed swing low, protected high]. High is
// always strictly greater than Low -- ResolveActiveRange never returns
// a Range for which that does not hold.
type Range struct {
	Low  quant.Price
	High quant.Price
}

// Equilibrium returns the range's 50% level: low + (high-low)/2,
// integer division truncating toward Low. Subtracting first keeps this
// overflow-safe without any extra bounds checking: since High > Low
// and both are non-negative (quant.Price's own invariant), High-Low is
// itself a valid, non-negative int64, and adding at most that much back
// to Low can never exceed High, so the result is always representable
// whenever the two endpoints already are. Callers must have already
// validated High > Low (ResolveActiveRange is the one path that
// constructs a Range, and it does).
func (r Range) Equilibrium() quant.Price {
	return r.Low + (r.High-r.Low)/2
}

// Classify returns price's zone relative to r's equilibrium (spec
// section 8): strictly below is Discount, strictly above is Premium,
// exactly at is Equilibrium.
func (r Range) Classify(price quant.Price) Zone {
	eq := r.Equilibrium()
	switch {
	case price < eq:
		return ZoneDiscount
	case price > eq:
		return ZonePremium
	default:
		return ZoneEquilibrium
	}
}

// ResolveActiveRange resolves the active premium/discount range from
// state (spec section 8). ok is false -- a distinct "undefined"
// result, never an error -- when:
//   - direction is UNDETERMINED,
//   - the required protected anchor on the trend side is absent
//     (bullish needs ProtectedLowSwingID, bearish needs
//     ProtectedHighSwingID) -- the anchor is read directly from state
//     and is NEVER substituted with the latest opposing reference swing
//     when absent, per the spec's explicit prohibition on that fallback,
//   - the opposing reference swing is absent, or
//   - the two endpoints do not form a positive range (High > Low fails).
//
// This is the only function that constructs a Range; every Range it
// returns (ok == true) already satisfies High > Low, so Equilibrium
// and Classify never need to re-check it.
func ResolveActiveRange(ctx context.Context, store *Store, state StateSnapshot) (Range, bool, error) {
	var lowID, highID *int64
	switch state.Direction {
	case DirectionBullish:
		lowID, highID = state.ProtectedLowSwingID, state.ReferenceSwingHighID
	case DirectionBearish:
		lowID, highID = state.ReferenceSwingLowID, state.ProtectedHighSwingID
	default: // DirectionUndetermined
		return Range{}, false, nil
	}
	if lowID == nil || highID == nil {
		return Range{}, false, nil
	}

	low, err := store.GetSwingByID(ctx, *lowID)
	if err != nil {
		return Range{}, false, err
	}
	high, err := store.GetSwingByID(ctx, *highID)
	if err != nil {
		return Range{}, false, err
	}

	if high.Price <= low.Price {
		return Range{}, false, nil
	}
	return Range{Low: low.Price, High: high.Price}, true, nil
}

// InDiscount reports whether price is in the discount zone of the
// active structure resolved from state -- the function downstream POI
// logic calls (spec section 8's own example). ok mirrors
// ResolveActiveRange's: false means the structure is undefined, not
// that price is or is not in discount.
func InDiscount(ctx context.Context, store *Store, state StateSnapshot, price quant.Price) (inDiscount, ok bool, err error) {
	r, ok, err := ResolveActiveRange(ctx, store, state)
	if err != nil || !ok {
		return false, ok, err
	}
	return r.Classify(price) == ZoneDiscount, true, nil
}
