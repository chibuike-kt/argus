package marketdata

import (
	"testing"

	"github.com/chibuike-kt/argus/internal/quant"
)

func TestBidAsk_IsCrossed(t *testing.T) {
	tests := []struct {
		name string
		bid  int64
		ask  int64
		want bool
	}{
		{"normal market: ask above bid", 100, 101, false},
		{"locked market: ask equals bid", 100, 100, false},
		{"crossed market: ask below bid", 101, 100, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := BidAsk{Bid: quant.Price(tt.bid), Ask: quant.Price(tt.ask)}
			if got := q.IsCrossed(); got != tt.want {
				t.Errorf("IsCrossed() = %v, want %v", got, tt.want)
			}
		})
	}
}
