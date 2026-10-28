package analysis

import (
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/quant"
)

func TestPassesLegSpanShapeTest(t *testing.T) {
	const interval = time.Minute
	base := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	at := func(minute int) time.Time { return base.Add(time.Duration(minute) * interval) }

	tests := []struct {
		name                                     string
		precedingMin, candidateMin, followingMin int
		minLegSpan                               int
		want                                     bool
	}{
		{"single-candle down-leg rejected", 39, 40, 48, 3, false},
		{"single-candle up-leg rejected", 32, 40, 41, 3, false},
		{"both legs single-candle rejected", 39, 40, 41, 3, false},
		{"short down-leg (2 < 3) rejected", 38, 40, 48, 3, false},
		{"short up-leg (2 < 3) rejected", 32, 40, 42, 3, false},
		{"both legs exactly N accepted", 37, 40, 43, 3, true},
		{"both legs well beyond N accepted", 2, 40, 60, 3, true},
		{"legs exactly at a smaller configured N accepted", 38, 40, 42, 2, true},
		{"legs below a smaller configured N rejected", 39, 40, 41, 2, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := passesLegSpanShapeTest(at(tt.precedingMin), at(tt.candidateMin), at(tt.followingMin), interval, tt.minLegSpan)
			if got != tt.want {
				t.Fatalf("passesLegSpanShapeTest(preceding=%d, candidate=%d, following=%d, N=%d) = %v, want %v",
					tt.precedingMin, tt.candidateMin, tt.followingMin, tt.minLegSpan, got, tt.want)
			}
		})
	}
}

// TestStructuralLiquidity_EquilibriumReuse documents that the second
// fib's equilibrium is section 8's Range.Equilibrium itself -- the
// exact formula (spec section 9: "computed exactly as in section 8") --
// not a second, independently-written copy of the truncating midpoint
// that could silently drift from it.
func TestStructuralLiquidity_EquilibriumReuse(t *testing.T) {
	fib := Range{Low: quant.Price(70), High: quant.Price(125)}
	if got := fib.Equilibrium(); got != 97 {
		t.Fatalf("second fib [70,125].Equilibrium() = %d, want 97 (70 + (125-70)/2, truncating toward low)", got)
	}
}
