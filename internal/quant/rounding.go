package quant

import (
	"fmt"
	"math/big"
)

// RoundingMode selects how a discarded fractional remainder is handled when
// parsing or rescaling would otherwise lose precision.
//
// There is deliberately no default: the zero value, RoundingModeUnspecified,
// is invalid, and every function that accepts a RoundingMode rejects it
// when rounding is actually needed. Which mode applies to stops, targets or
// position sizing is an open strategy decision (ADR-012); this package only
// provides the mechanism, not the policy.
type RoundingMode int

const (
	// RoundingModeUnspecified is the invalid zero value. It is never a
	// usable mode; it exists so an unset field fails loudly.
	RoundingModeUnspecified RoundingMode = iota

	// RoundDown truncates toward zero: discarded digits are simply dropped.
	RoundDown

	// RoundUp rounds away from zero whenever any discarded digit is
	// nonzero.
	RoundUp

	// RoundHalfUp rounds half away from zero: a discarded remainder of
	// exactly one half rounds away from zero.
	RoundHalfUp

	// RoundHalfEven rounds half to even ("banker's rounding"): a discarded
	// remainder of exactly one half rounds to whichever neighbor is even.
	RoundHalfEven
)

// Validate reports whether m is one of the defined, usable rounding modes.
func (m RoundingMode) Validate() error {
	switch m {
	case RoundDown, RoundUp, RoundHalfUp, RoundHalfEven:
		return nil
	default:
		return fmt.Errorf("rounding mode %d is invalid: an explicit mode is required, there is no default", m)
	}
}

// roundMagnitude adjusts quotient — a non-negative integer that already had
// remainder/factor divided out of it — according to mode. remainder and
// factor are non-negative with 0 <= remainder < factor and factor > 0. Sign
// is applied by the caller; this operates purely on magnitudes so
// RoundHalfUp and RoundUp here mean "away from zero" once sign is reapplied.
func roundMagnitude(quotient, remainder, factor *big.Int, mode RoundingMode) *big.Int {
	if remainder.Sign() == 0 {
		return quotient
	}
	one := big.NewInt(1)
	switch mode {
	case RoundDown:
		return quotient
	case RoundUp:
		return new(big.Int).Add(quotient, one)
	case RoundHalfUp:
		twice := new(big.Int).Lsh(remainder, 1)
		if twice.Cmp(factor) >= 0 {
			return new(big.Int).Add(quotient, one)
		}
		return quotient
	case RoundHalfEven:
		twice := new(big.Int).Lsh(remainder, 1)
		switch twice.Cmp(factor) {
		case 1:
			return new(big.Int).Add(quotient, one)
		case 0:
			if quotient.Bit(0) == 1 { // quotient is odd: round to the even neighbor
				return new(big.Int).Add(quotient, one)
			}
			return quotient
		default:
			return quotient
		}
	default:
		// Unreachable: callers validate mode before reaching here.
		return quotient
	}
}
