package instruments

import (
	"errors"
	"testing"

	"github.com/chibuike-kt/argus/internal/quant"
)

func validParams() NewInstrumentParams {
	priceScale := quant.Scale(2)
	qtyScale := quant.Scale(6)
	return NewInstrumentParams{
		Provider:     "binance",
		NativeSymbol: "BTCUSDT",
		AssetClass:   "crypto_perp",
		PriceScale:   &priceScale,
		QtyScale:     &qtyScale,
		TickSize:     1,
		QtyStepSize:  1,
	}
}

func TestNewInstrumentParams_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(p *NewInstrumentParams)
		wantErr error
	}{
		{"valid params pass", func(*NewInstrumentParams) {}, nil},
		{"missing provider", func(p *NewInstrumentParams) { p.Provider = "" }, ErrProviderRequired},
		{"missing native symbol", func(p *NewInstrumentParams) { p.NativeSymbol = "" }, ErrNativeSymbolRequired},
		{"missing asset class", func(p *NewInstrumentParams) { p.AssetClass = "" }, ErrAssetClassRequired},
		{
			"nil price scale -- the specific invariant being tested: a forgotten scale must fail, not default to 0",
			func(p *NewInstrumentParams) { p.PriceScale = nil },
			ErrPriceScaleRequired,
		},
		{"nil qty scale", func(p *NewInstrumentParams) { p.QtyScale = nil }, ErrQtyScaleRequired},
		{"negative tick size", func(p *NewInstrumentParams) { p.TickSize = -1 }, ErrTickSizeNegative},
		{"negative qty step size", func(p *NewInstrumentParams) { p.QtyStepSize = -1 }, ErrQtyStepSizeNegative},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validParams()
			tt.mutate(&p)
			err := p.validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewInstrumentParams_Validate_ScaleZeroIsAValidExplicitChoice(t *testing.T) {
	// Scale 0 is a real, valid choice (integer-only prices) once the
	// caller has actually set the pointer -- it must not be rejected the
	// way a nil pointer is. This is the case the pointer design exists to
	// get right: zero-but-set must behave differently from unset.
	p := validParams()
	zero := quant.Scale(0)
	p.PriceScale = &zero
	if err := p.validate(); err != nil {
		t.Fatalf("validate() with explicit scale 0 = %v, want nil", err)
	}
}

func TestNewInstrumentParams_Validate_ScaleOutOfRange(t *testing.T) {
	p := validParams()
	tooHigh := quant.Scale(13)
	p.PriceScale = &tooHigh
	if err := p.validate(); err == nil {
		t.Fatal("validate() with out-of-range scale = nil, want an error")
	}
}
