package hyperliquid

import (
	"fmt"
	"strings"
)

// validatePriceFormat reports whether raw complies with Hyperliquid's
// own perpetual price-precision rule, quoted directly from the
// primary source (hyperliquid.gitbook.io/hyperliquid-docs/
// for-developers/api/tick-and-lot-size): a price may have at most 5
// significant figures, and at most (6 - szDecimals) decimal places
// (6 is MAX_DECIMALS for perps), except that an integer-valued price
// is always allowed regardless of significant figures.
//
// This exists to check what StreamCandles actually receives against
// the rule (brief-hyperliquid-adapter.md's own real-run requirement),
// not to reject or reshape anything -- a violation is reported, never
// silently corrected or dropped; the Normalizer's own scale check is
// the real validation boundary (guide 1), this is observational.
func validatePriceFormat(raw string, szDecimals int32) error {
	if isIntegerValued(raw) {
		return nil
	}
	if sf := significantFigures(raw); sf > 5 {
		return fmt.Errorf("price %q has %d significant figures, want at most 5", raw, sf)
	}
	maxDecimals := maxDecimalsPerp - szDecimals
	if maxDecimals < 0 {
		maxDecimals = 0
	}
	if dp := decimalPlaces(raw); dp > int(maxDecimals) {
		return fmt.Errorf("price %q has %d decimal places, want at most %d (MAX_DECIMALS=%d - szDecimals=%d)", raw, dp, maxDecimals, maxDecimalsPerp, szDecimals)
	}
	return nil
}

// significantFigures counts raw's own significant digits: every digit
// except a leading zero before the first nonzero digit. The decimal
// point and a leading '-' are not digits. "0.001234" -> 4 ("1234").
// "1234.5" -> 5. "0" -> 1.
func significantFigures(raw string) int {
	count := 0
	seenNonZero := false
	for _, r := range raw {
		switch {
		case r == '.' || r == '-':
			continue
		case r == '0' && !seenNonZero:
			continue
		default:
			seenNonZero = true
			count++
		}
	}
	if count == 0 {
		return 1 // "0" or "0.0...0": one significant figure, the zero itself
	}
	return count
}

// decimalPlaces returns how many digits follow raw's own decimal
// point, or 0 if it has none.
func decimalPlaces(raw string) int {
	i := strings.IndexByte(raw, '.')
	if i < 0 {
		return 0
	}
	return len(raw) - i - 1
}

// isIntegerValued reports whether raw represents a whole number --
// no decimal point, or a decimal point followed only by zeros.
func isIntegerValued(raw string) bool {
	i := strings.IndexByte(raw, '.')
	if i < 0 {
		return true
	}
	frac := raw[i+1:]
	return strings.Count(frac, "0") == len(frac)
}
