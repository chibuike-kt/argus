package quant

import (
	"math/big"
	"testing"
)

func TestRoundingMode_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mode    RoundingMode
		wantErr bool
	}{
		{"unspecified is invalid", RoundingModeUnspecified, true},
		{"down", RoundDown, false},
		{"up", RoundUp, false},
		{"half up", RoundHalfUp, false},
		{"half even", RoundHalfEven, false},
		{"out of range", RoundingMode(99), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.mode.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRoundMagnitude(t *testing.T) {
	// factor is always 100 (two discarded digits) in these cases; remainder
	// is the discarded value out of that factor.
	factor := big.NewInt(100)
	quotient := big.NewInt(42)

	tests := []struct {
		name      string
		remainder int64
		mode      RoundingMode
		want      int64
	}{
		{"no remainder: unchanged regardless of mode", 0, RoundUp, 42},
		{"round down: always truncates", 99, RoundDown, 42},
		{"round up: any nonzero remainder rounds up", 1, RoundUp, 43},
		{"half up: below half truncates", 49, RoundHalfUp, 42},
		{"half up: exactly half rounds up", 50, RoundHalfUp, 43},
		{"half up: above half rounds up", 51, RoundHalfUp, 43},
		{"half even: below half truncates", 49, RoundHalfEven, 42},
		{"half even: exactly half, quotient even, stays even", 50, RoundHalfEven, 42},
		{"half even: above half rounds up", 51, RoundHalfEven, 43},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := roundMagnitude(quotient, big.NewInt(tt.remainder), factor, tt.mode)
			if got.Int64() != tt.want {
				t.Fatalf("roundMagnitude(42, %d, 100, mode=%d) = %d, want %d", tt.remainder, tt.mode, got.Int64(), tt.want)
			}
		})
	}
}

func TestRoundMagnitude_HalfEven_RoundsToEvenNeighbor(t *testing.T) {
	factor := big.NewInt(100)
	remainder := big.NewInt(50) // exactly half

	// Odd quotient at exactly half rounds up to the even neighbor.
	oddQuotient := big.NewInt(43)
	if got := roundMagnitude(oddQuotient, remainder, factor, RoundHalfEven); got.Int64() != 44 {
		t.Fatalf("odd quotient at half: got %d, want 44", got.Int64())
	}

	// Even quotient at exactly half stays put.
	evenQuotient := big.NewInt(44)
	if got := roundMagnitude(evenQuotient, remainder, factor, RoundHalfEven); got.Int64() != 44 {
		t.Fatalf("even quotient at half: got %d, want 44", got.Int64())
	}
}
