package hyperliquid

import "testing"

func TestValidatePriceFormat_DocExamples(t *testing.T) {
	// Every case here is quoted directly from the primary source
	// (tick-and-lot-size), not invented.
	cases := []struct {
		raw        string
		szDecimals int32
		wantErr    bool
	}{
		{"1234.5", 0, false},   // "1234.5 is valid"
		{"1234.56", 0, true},   // "...but 1234.56 is not (too many significant figures)"
		{"123456", 0, false},   // "123456 is a valid price even though 12345.6 is not"
		{"12345.6", 0, true},   // the other half of that same example
		{"0.001234", 0, false}, // "0.001234 is valid"
		{"0.0012345", 0, true}, // "...but 0.0012345 is not (more than 6 decimal places)"
		{"0.01234", 1, false},  // "If szDecimals = 1, 0.01234 is valid"
		{"0.012345", 1, true},  // "...but 0.012345 is not (more than 6 - szDecimals decimal places)"
		// Note: a third-party (Chainstack) BTC example claiming
		// "97000.5 valid, 97000.55 invalid" at szDecimals=5 was
		// deliberately NOT used here -- 97000.5 has 6 significant
		// digits (9,7,0,0,0,5), which contradicts the primary source's
		// own "12345.6 is not [valid]" example (also 6 digits) just
		// above. Trusting the primary page's own examples over a
		// third-party one that appears inconsistent with them.
	}
	for _, c := range cases {
		err := validatePriceFormat(c.raw, c.szDecimals)
		if c.wantErr && err == nil {
			t.Errorf("validatePriceFormat(%q, szDecimals=%d) = nil, want an error", c.raw, c.szDecimals)
		}
		if !c.wantErr && err != nil {
			t.Errorf("validatePriceFormat(%q, szDecimals=%d) = %v, want nil", c.raw, c.szDecimals, err)
		}
	}
}

func TestValidatePriceFormat_IntegerExemptRegardlessOfSignificantFigures(t *testing.T) {
	// "Integer prices are always allowed, regardless of the number of
	// significant figures" -- 123456 (6 sig figs) is valid per the
	// doc's own example above; this checks a few more, including a
	// trailing-zeros decimal form of the same value.
	for _, raw := range []string{"1234567", "100000", "100000.0", "100000.00"} {
		if err := validatePriceFormat(raw, 0); err != nil {
			t.Errorf("validatePriceFormat(%q) = %v, want nil (integer-valued, exempt)", raw, err)
		}
	}
}

func TestSignificantFigures(t *testing.T) {
	cases := map[string]int{
		"0":        1,
		"0.0":      1,
		"1234.5":   5,
		"0.001234": 4,
		"100":      3,
		"-5.5":     2,
	}
	for raw, want := range cases {
		if got := significantFigures(raw); got != want {
			t.Errorf("significantFigures(%q) = %d, want %d", raw, got, want)
		}
	}
}

func TestDecimalPlaces(t *testing.T) {
	cases := map[string]int{
		"100":      0,
		"100.5":    1,
		"0.001234": 6,
	}
	for raw, want := range cases {
		if got := decimalPlaces(raw); got != want {
			t.Errorf("decimalPlaces(%q) = %d, want %d", raw, got, want)
		}
	}
}

func TestIsIntegerValued(t *testing.T) {
	cases := map[string]bool{
		"100":      true,
		"100.0":    true,
		"100.00":   true,
		"100.5":    false,
		"0.001234": false,
	}
	for raw, want := range cases {
		if got := isIntegerValued(raw); got != want {
			t.Errorf("isIntegerValued(%q) = %v, want %v", raw, got, want)
		}
	}
}
