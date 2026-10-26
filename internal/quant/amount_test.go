package quant

import (
	"errors"
	"math"
	"testing"
)

func TestParsePrice(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		scale   Scale
		want    Price
		wantErr error
	}{
		{"whole number", "64250", 2, 6425000, nil},
		{"exact fraction", "64250.50", 2, 6425050, nil},
		{"fewer fractional digits than scale", "1.5", 4, 15000, nil},
		{"zero", "0", 2, 0, nil},
		{"zero with fraction", "0.00", 2, 0, nil},
		{"trailing zero digits beyond scale are not precision loss", "1.500", 2, 150, nil},
		{"scale zero, integer only", "12345", 0, 12345, nil},

		{"negative rejected", "-1.50", 2, 0, ErrNegativeValue},
		{"excess fractional digits rejected", "1.505", 2, 0, ErrPrecisionLoss},
		{"NaN rejected", "NaN", 2, 0, ErrMalformedDecimal},
		{"Inf rejected", "Inf", 2, 0, ErrMalformedDecimal},
		{"exponent rejected", "1e10", 2, 0, ErrMalformedDecimal},
		{"empty string rejected", "", 2, 0, ErrMalformedDecimal},
		{"leading dot rejected", ".5", 2, 0, ErrMalformedDecimal},
		{"trailing dot rejected", "5.", 2, 0, ErrMalformedDecimal},
		{"whitespace rejected", " 1.5", 2, 0, ErrMalformedDecimal},
		{"double dot rejected", "1..5", 2, 0, ErrMalformedDecimal},
		{"overflow rejected", "99999999999999999999", 0, 0, ErrOverflow},
		{"invalid scale rejected", "1.5", MaxScale + 1, 0, nil}, // checked separately below
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePrice(tt.input, tt.scale)
			if tt.name == "invalid scale rejected" {
				if err == nil {
					t.Fatalf("ParsePrice(%q, %d) = nil error, want error", tt.input, tt.scale)
				}
				return
			}
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ParsePrice(%q, %d) error = %v, want %v", tt.input, tt.scale, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePrice(%q, %d) unexpected error: %v", tt.input, tt.scale, err)
			}
			if got != tt.want {
				t.Fatalf("ParsePrice(%q, %d) = %d, want %d", tt.input, tt.scale, got, tt.want)
			}
		})
	}
}

func TestParseQty_SameRulesAsPrice(t *testing.T) {
	got, err := ParseQty("10.25", 2)
	if err != nil {
		t.Fatalf("ParseQty: %v", err)
	}
	if got != 1025 {
		t.Fatalf("ParseQty(10.25, 2) = %d, want 1025", got)
	}

	if _, err := ParseQty("-1", 2); !errors.Is(err, ErrNegativeValue) {
		t.Fatalf("ParseQty(-1) error = %v, want ErrNegativeValue", err)
	}
}

func TestParsePriceRounded(t *testing.T) {
	tests := []struct {
		name  string
		input string
		scale Scale
		mode  RoundingMode
		want  Price
	}{
		{"round down truncates", "1.509", 2, RoundDown, 150},
		{"round up rounds away from zero", "1.501", 2, RoundUp, 151},
		{"half up rounds half up", "1.505", 2, RoundHalfUp, 151},
		{"half even rounds to even", "1.505", 2, RoundHalfEven, 150}, // 150 is even
		{"exact value needs no rounding", "1.50", 2, RoundDown, 150},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePriceRounded(tt.input, tt.scale, tt.mode)
			if err != nil {
				t.Fatalf("ParsePriceRounded(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParsePriceRounded(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}

	t.Run("unspecified mode rejected", func(t *testing.T) {
		if _, err := ParsePriceRounded("1.505", 2, RoundingModeUnspecified); err == nil {
			t.Fatal("expected error for unspecified rounding mode")
		}
	})
}

func TestPrice_String_RoundTrip(t *testing.T) {
	tests := []struct {
		value Price
		scale Scale
		want  string
	}{
		{6425050, 2, "64250.50"},
		{0, 2, "0.00"},
		{5, 4, "0.0005"},
		{12345, 0, "12345"},
	}
	for _, tt := range tests {
		got := tt.value.String(tt.scale)
		if got != tt.want {
			t.Fatalf("Price(%d).String(%d) = %q, want %q", tt.value, tt.scale, got, tt.want)
		}
		// Round trip: formatting then reparsing must yield the same value.
		reparsed, err := ParsePrice(got, tt.scale)
		if err != nil {
			t.Fatalf("reparse %q: %v", got, err)
		}
		if reparsed != tt.value {
			t.Fatalf("round trip: reparsed %d, want %d", reparsed, tt.value)
		}
	}
}

func TestPrice_Rescale(t *testing.T) {
	t.Run("increasing scale is always exact", func(t *testing.T) {
		p := Price(150) // 1.50 at scale 2
		got, err := p.Rescale(2, 4, RoundDown)
		if err != nil {
			t.Fatalf("Rescale: %v", err)
		}
		if got != 15000 { // 1.5000 at scale 4
			t.Fatalf("Rescale(up) = %d, want 15000", got)
		}
	})

	t.Run("decreasing scale requires rounding", func(t *testing.T) {
		p := Price(15055) // 1.5055 at scale 4
		got, err := p.Rescale(4, 2, RoundHalfUp)
		if err != nil {
			t.Fatalf("Rescale: %v", err)
		}
		if got != 151 { // 1.51 at scale 2
			t.Fatalf("Rescale(down, half up) = %d, want 151", got)
		}
	})

	t.Run("decreasing scale without a mode is rejected", func(t *testing.T) {
		p := Price(15055)
		if _, err := p.Rescale(4, 2, RoundingModeUnspecified); err == nil {
			t.Fatal("expected error for unspecified rounding mode")
		}
	})

	t.Run("same scale is a no-op", func(t *testing.T) {
		p := Price(150)
		got, err := p.Rescale(2, 2, RoundingModeUnspecified)
		if err != nil {
			t.Fatalf("Rescale same-scale: %v", err)
		}
		if got != p {
			t.Fatalf("Rescale(same scale) = %d, want %d", got, p)
		}
	})
}

func TestPrice_IsMultipleOf(t *testing.T) {
	tests := []struct {
		name string
		p    Price
		step Price
		want bool
	}{
		{"exact multiple", 150, 5, true},
		{"not a multiple", 152, 5, false},
		{"zero step means no constraint", 1, 0, true},
		{"zero value is always a multiple", 0, 5, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.IsMultipleOf(tt.step); got != tt.want {
				t.Errorf("Price(%d).IsMultipleOf(%d) = %v, want %v", tt.p, tt.step, got, tt.want)
			}
		})
	}
}

func TestQty_IsMultipleOf(t *testing.T) {
	if !Qty(100).IsMultipleOf(25) {
		t.Error("Qty(100).IsMultipleOf(25) = false, want true")
	}
	if Qty(101).IsMultipleOf(25) {
		t.Error("Qty(101).IsMultipleOf(25) = true, want false")
	}
	if !Qty(1).IsMultipleOf(0) {
		t.Error("Qty(1).IsMultipleOf(0) = false, want true (zero step means no constraint)")
	}
}

func TestPrice_AddSub(t *testing.T) {
	a, b := Price(100), Price(40)

	sum, err := a.Add(b)
	if err != nil || sum != 140 {
		t.Fatalf("Add = %d, %v; want 140, nil", sum, err)
	}

	diff, err := a.Sub(b)
	if err != nil || diff != 60 {
		t.Fatalf("Sub = %d, %v; want 60, nil", diff, err)
	}

	if _, err := b.Sub(a); !errors.Is(err, ErrNegativeValue) {
		t.Fatalf("Sub producing negative: err = %v, want ErrNegativeValue", err)
	}

	if _, err := Price(math.MaxInt64).Add(Price(1)); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Add overflow: err = %v, want ErrOverflow", err)
	}
}

func TestQty_AddSub(t *testing.T) {
	a, b := Qty(100), Qty(40)

	sum, err := a.Add(b)
	if err != nil || sum != 140 {
		t.Fatalf("Add = %d, %v; want 140, nil", sum, err)
	}

	if _, err := b.Sub(a); !errors.Is(err, ErrNegativeValue) {
		t.Fatalf("Sub producing negative: err = %v, want ErrNegativeValue", err)
	}
}

// FuzzParsePrice checks that ParsePrice never panics on arbitrary input and
// that every successful parse round-trips through String/ParsePrice to the
// same value (guide 10: fuzz parsers and boundary validation).
func FuzzParsePrice(f *testing.F) {
	seeds := []string{
		"", "0", "-0", "1.5", "-1.5", "NaN", "Inf", "-Inf", "1e10",
		".5", "5.", "1..5", "99999999999999999999999999", "0.00000000001",
		"  1", "1 ", "+1", "1,000", "0.0", "00001.00100",
	}
	for _, s := range seeds {
		f.Add(s, int32(2))
	}
	f.Fuzz(func(t *testing.T, s string, scaleInt int32) {
		scale := Scale(scaleInt)
		got, err := ParsePrice(s, scale)
		if err != nil {
			return // any rejection is acceptable; panicking is not
		}
		// A successful parse must round-trip through formatting and
		// reparsing to the identical value.
		formatted := got.String(scale)
		reparsed, err := ParsePrice(formatted, scale)
		if err != nil {
			t.Fatalf("round trip reparse of %q (from input %q) failed: %v", formatted, s, err)
		}
		if reparsed != got {
			t.Fatalf("round trip mismatch: input %q -> %d -> %q -> %d", s, got, formatted, reparsed)
		}
	})
}
