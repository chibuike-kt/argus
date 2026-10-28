package analysis

import (
	"testing"

	"github.com/chibuike-kt/argus/internal/quant"
)

func TestRange_Equilibrium(t *testing.T) {
	tests := []struct {
		name      string
		low, high int64
		wantEq    int64
	}{
		{"even high-low: exact midpoint", 100, 110, 105},
		{"odd high-low: truncates toward low", 100, 111, 105},
		{"odd high-low, adjacent values: truncates toward low", 100, 101, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Range{Low: quant.Price(tt.low), High: quant.Price(tt.high)}
			if got := int64(r.Equilibrium()); got != tt.wantEq {
				t.Fatalf("Equilibrium() = %d, want %d", got, tt.wantEq)
			}
		})
	}
}

func TestRange_Classify_Bullish(t *testing.T) {
	// A bullish-shaped range: protected low 100, latest confirmed high
	// 130 -- equilibrium 115.
	r := Range{Low: 100, High: 130}
	tests := []struct {
		name  string
		price int64
		want  Zone
	}{
		{"strictly below equilibrium is discount", 110, ZoneDiscount},
		{"exactly at equilibrium", 115, ZoneEquilibrium},
		{"strictly above equilibrium is premium", 120, ZonePremium},
		{"at the range low is discount", 100, ZoneDiscount},
		{"at the range high is premium", 130, ZonePremium},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.Classify(quant.Price(tt.price)); got != tt.want {
				t.Fatalf("Classify(%d) = %v, want %v", tt.price, got, tt.want)
			}
		})
	}
}

func TestRange_Classify_Bearish(t *testing.T) {
	// A bearish-shaped range: latest confirmed low 170, protected high
	// 230 -- equilibrium 200.
	r := Range{Low: 170, High: 230}
	tests := []struct {
		name  string
		price int64
		want  Zone
	}{
		{"strictly below equilibrium is discount", 190, ZoneDiscount},
		{"exactly at equilibrium", 200, ZoneEquilibrium},
		{"strictly above equilibrium is premium", 210, ZonePremium},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.Classify(quant.Price(tt.price)); got != tt.want {
				t.Fatalf("Classify(%d) = %v, want %v", tt.price, got, tt.want)
			}
		})
	}
}
