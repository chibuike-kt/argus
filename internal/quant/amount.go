package quant

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

// Price is a scaled integer amount representing an instrument's price. It
// is always non-negative: "negative price" is not a representable state.
// It carries no scale of its own; the scale is an instrument fact supplied
// by the caller at every operation (see Scale).
type Price int64

// Qty is a scaled integer amount representing an instrument's quantity.
// Same non-negative and scale rules as Price. Direction (long/short,
// buy/sell) is a separate field elsewhere, never the sign of Qty.
type Qty int64

var (
	// ErrMalformedDecimal is returned when input is not a plain decimal
	// numeral: no exponents, no NaN/Inf tokens, no whitespace, at most one
	// leading '-' and at most one decimal point, with at least one digit
	// required on each side of the point when a point is present.
	ErrMalformedDecimal = errors.New("malformed decimal")

	// ErrNegativeValue is returned when a decimal string or arithmetic
	// result would be negative. Price and Qty are always non-negative.
	ErrNegativeValue = errors.New("price and quantity values must not be negative")

	// ErrPrecisionLoss is returned when a decimal string has more
	// fractional digits than the target scale allows. Parsing never rounds
	// implicitly (approved decision: rounding is an explicit, required mode
	// at every call site). Use ParsePriceRounded / ParseQtyRounded to round.
	ErrPrecisionLoss = errors.New("value has more fractional digits than scale allows")

	// ErrOverflow is returned when a scaled value would not fit in int64.
	ErrOverflow = errors.New("value overflows scaled int64")
)

// decimalPattern accepts an optional leading '-', one or more digits, and an
// optional '.' followed by one or more digits. It rejects exponents, "NaN",
// "Inf", whitespace, empty strings, and a bare trailing or leading point.
var decimalPattern = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// ParsePrice parses an exact, non-negative decimal string at the given
// scale. It never rounds: a string with more fractional digits than scale
// allows is rejected rather than truncated.
func ParsePrice(s string, scale Scale) (Price, error) {
	v, err := parseScaled(s, scale, false, RoundingModeUnspecified)
	if err != nil {
		return 0, fmt.Errorf("parse price %q at scale %d: %w", s, scale, err)
	}
	return Price(v), nil
}

// ParseQty parses an exact, non-negative decimal string at the given scale.
// Same rules as ParsePrice.
func ParseQty(s string, scale Scale) (Qty, error) {
	v, err := parseScaled(s, scale, false, RoundingModeUnspecified)
	if err != nil {
		return 0, fmt.Errorf("parse qty %q at scale %d: %w", s, scale, err)
	}
	return Qty(v), nil
}

// ParsePriceRounded parses a non-negative decimal string at the given
// scale, rounding to scale using mode when the string carries more
// fractional digits than scale allows. mode must be a valid, specific mode.
func ParsePriceRounded(s string, scale Scale, mode RoundingMode) (Price, error) {
	if err := mode.Validate(); err != nil {
		return 0, fmt.Errorf("parse price %q at scale %d: %w", s, scale, err)
	}
	v, err := parseScaled(s, scale, true, mode)
	if err != nil {
		return 0, fmt.Errorf("parse price %q at scale %d: %w", s, scale, err)
	}
	return Price(v), nil
}

// ParseQtyRounded parses a non-negative decimal string at the given scale,
// rounding as ParsePriceRounded does.
func ParseQtyRounded(s string, scale Scale, mode RoundingMode) (Qty, error) {
	if err := mode.Validate(); err != nil {
		return 0, fmt.Errorf("parse qty %q at scale %d: %w", s, scale, err)
	}
	v, err := parseScaled(s, scale, true, mode)
	if err != nil {
		return 0, fmt.Errorf("parse qty %q at scale %d: %w", s, scale, err)
	}
	return Qty(v), nil
}

// parseScaled parses a signed decimal string into a scaled int64 magnitude,
// enforcing non-negativity for Price/Qty. When rounding is false, excess
// fractional digits are a hard error (ErrPrecisionLoss); when true, they
// are resolved using mode (which the caller has already validated).
func parseScaled(s string, scale Scale, rounding bool, mode RoundingMode) (int64, error) {
	if err := scale.Validate(); err != nil {
		return 0, err
	}
	if !decimalPattern.MatchString(s) {
		return 0, ErrMalformedDecimal
	}
	if strings.HasPrefix(s, "-") {
		return 0, ErrNegativeValue
	}

	// strings.Cut returns fracPart == "" when there is no '.', which is the
	// value we want for an integer-only input.
	intPart, fracPart, _ := strings.Cut(s, ".")

	scaleInt := int(scale)
	var keptFrac, discarded string
	if len(fracPart) <= scaleInt {
		keptFrac = fracPart + strings.Repeat("0", scaleInt-len(fracPart))
	} else {
		keptFrac = fracPart[:scaleInt]
		discarded = fracPart[scaleInt:]
	}

	magnitude, ok := new(big.Int).SetString(intPart+keptFrac, 10)
	if !ok {
		return 0, ErrMalformedDecimal
	}

	if discarded != "" {
		trimmed := strings.TrimRight(discarded, "0")
		if trimmed != "" {
			if !rounding {
				return 0, ErrPrecisionLoss
			}
			remainder, ok := new(big.Int).SetString(discarded, 10)
			if !ok {
				return 0, ErrMalformedDecimal
			}
			factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(len(discarded))), nil)
			magnitude = roundMagnitude(magnitude, remainder, factor, mode)
		}
	}

	if !magnitude.IsInt64() {
		return 0, ErrOverflow
	}
	return magnitude.Int64(), nil
}

// Rescale converts p from fromScale to toScale. Increasing scale is always
// exact. Decreasing scale discards precision and requires a valid,
// specific RoundingMode.
func (p Price) Rescale(fromScale, toScale Scale, mode RoundingMode) (Price, error) {
	v, err := rescaleInt64(int64(p), fromScale, toScale, mode)
	if err != nil {
		return 0, fmt.Errorf("rescale price from scale %d to %d: %w", fromScale, toScale, err)
	}
	return Price(v), nil
}

// Rescale converts q from fromScale to toScale. Same rules as Price.Rescale.
func (q Qty) Rescale(fromScale, toScale Scale, mode RoundingMode) (Qty, error) {
	v, err := rescaleInt64(int64(q), fromScale, toScale, mode)
	if err != nil {
		return 0, fmt.Errorf("rescale qty from scale %d to %d: %w", fromScale, toScale, err)
	}
	return Qty(v), nil
}

func rescaleInt64(v int64, fromScale, toScale Scale, mode RoundingMode) (int64, error) {
	if v < 0 {
		return 0, ErrNegativeValue
	}
	if err := fromScale.Validate(); err != nil {
		return 0, fmt.Errorf("from scale: %w", err)
	}
	if err := toScale.Validate(); err != nil {
		return 0, fmt.Errorf("to scale: %w", err)
	}

	diff := int(toScale) - int(fromScale)
	if diff == 0 {
		return v, nil
	}

	abs := big.NewInt(v)
	var result *big.Int
	if diff > 0 {
		factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(diff)), nil)
		result = new(big.Int).Mul(abs, factor)
	} else {
		if err := mode.Validate(); err != nil {
			return 0, err
		}
		factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-diff)), nil)
		quotient, remainder := new(big.Int).QuoRem(abs, factor, new(big.Int))
		result = roundMagnitude(quotient, remainder, factor, mode)
	}

	if !result.IsInt64() {
		return 0, ErrOverflow
	}
	return result.Int64(), nil
}

// String renders p as an exact, non-negative decimal string at scale. It
// never rounds.
func (p Price) String(scale Scale) string { return formatScaled(int64(p), scale) }

// String renders q as an exact, non-negative decimal string at scale. It
// never rounds.
func (q Qty) String(scale Scale) string { return formatScaled(int64(q), scale) }

func formatScaled(v int64, scale Scale) string {
	digits := big.NewInt(v).String() // v is always >= 0 by construction
	scaleInt := int(scale)
	if scaleInt == 0 {
		return digits
	}
	for len(digits) <= scaleInt {
		digits = "0" + digits
	}
	intPart := digits[:len(digits)-scaleInt]
	fracPart := digits[len(digits)-scaleInt:]
	return intPart + "." + fracPart
}

// Add returns p+q, or ErrOverflow if the result does not fit in int64.
func (p Price) Add(q Price) (Price, error) {
	v, err := addChecked(int64(p), int64(q))
	if err != nil {
		return 0, fmt.Errorf("price add: %w", err)
	}
	return Price(v), nil
}

// Sub returns p-q, or an error if the result would be negative or would
// not fit in int64.
func (p Price) Sub(q Price) (Price, error) {
	v, err := subChecked(int64(p), int64(q))
	if err != nil {
		return 0, fmt.Errorf("price sub: %w", err)
	}
	return Price(v), nil
}

// Add returns q+o, or ErrOverflow if the result does not fit in int64.
func (q Qty) Add(o Qty) (Qty, error) {
	v, err := addChecked(int64(q), int64(o))
	if err != nil {
		return 0, fmt.Errorf("qty add: %w", err)
	}
	return Qty(v), nil
}

// Sub returns q-o, or an error if the result would be negative or would not
// fit in int64.
func (q Qty) Sub(o Qty) (Qty, error) {
	v, err := subChecked(int64(q), int64(o))
	if err != nil {
		return 0, fmt.Errorf("qty sub: %w", err)
	}
	return Qty(v), nil
}

func addChecked(a, b int64) (int64, error) {
	sum := a + b
	if (b > 0 && sum < a) || (b < 0 && sum > a) {
		return 0, ErrOverflow
	}
	return sum, nil
}

func subChecked(a, b int64) (int64, error) {
	diff := a - b
	if (b < 0 && diff < a) || (b > 0 && diff > a) {
		return 0, ErrOverflow
	}
	if diff < 0 {
		return 0, ErrNegativeValue
	}
	return diff, nil
}
