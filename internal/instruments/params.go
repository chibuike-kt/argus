package instruments

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/chibuike-kt/argus/internal/quant"
)

// Sentinel errors for NewInstrumentParams.validate. Use errors.Is.
var (
	ErrProviderRequired     = errors.New("instruments: provider is required")
	ErrNativeSymbolRequired = errors.New("instruments: native symbol is required")
	ErrAssetClassRequired   = errors.New("instruments: asset class is required")

	// ErrPriceScaleRequired and ErrQtyScaleRequired are returned when the
	// caller leaves PriceScale or QtyScale nil. The database also refuses
	// an insert that omits these (000002 dropped their defaults), but
	// failing here gives a clear, specific error before a round trip to
	// the database, and the two checks are not fully redundant: a nil
	// pointer at this layer and a NULL column at the database layer are
	// the same mistake caught at two boundaries, not one hiding the other.
	ErrPriceScaleRequired = errors.New("instruments: price scale must be set explicitly")
	ErrQtyScaleRequired   = errors.New("instruments: qty scale must be set explicitly")

	ErrTickSizeNegative    = errors.New("instruments: tick size must not be negative")
	ErrQtyStepSizeNegative = errors.New("instruments: qty step size must not be negative")
)

// NewInstrumentParams are the required inputs to register a new
// instrument. PriceScale and QtyScale are pointers specifically so Insert
// can distinguish "the caller explicitly chose scale 0" from "the caller
// never set it": quant.Scale's zero value, 0, is itself a valid scale, so
// a plain (non-pointer) field could never make that distinction (guide
// section 1: a boundary where zero must be distinguished from absent needs
// a pointer, the same reasoning a PATCH body needs it for an optional
// field).
//
// TickSize and QtyStepSize are plain, required quant values: scale 0 is a
// real choice that needs the pointer treatment above, but a tick/step size
// of exactly 0 is not a meaningful "forgot to set it" case the way scale 0
// is -- it is simply invalid, and validate() rejects it directly.
type NewInstrumentParams struct {
	Provider     string
	NativeSymbol string
	AssetClass   string
	BaseAsset    *string
	QuoteAsset   *string
	ContractType *string

	PriceScale *quant.Scale
	QtyScale   *quant.Scale

	TickSize     quant.Price  // scaled by *PriceScale
	QtyStepSize  quant.Qty    // scaled by *QtyScale
	ContractSize *quant.Qty   // scaled by *QtyScale; nil where not applicable
	PipSize      *quant.Price // scaled by *PriceScale; nil for non-forex instruments

	SupportedIntervals []string
	Metadata           json.RawMessage
}

func (p NewInstrumentParams) validate() error {
	if p.Provider == "" {
		return ErrProviderRequired
	}
	if p.NativeSymbol == "" {
		return ErrNativeSymbolRequired
	}
	if p.AssetClass == "" {
		return ErrAssetClassRequired
	}
	if p.PriceScale == nil {
		return ErrPriceScaleRequired
	}
	if p.QtyScale == nil {
		return ErrQtyScaleRequired
	}
	if err := p.PriceScale.Validate(); err != nil {
		return fmt.Errorf("instruments: price scale: %w", err)
	}
	if err := p.QtyScale.Validate(); err != nil {
		return fmt.Errorf("instruments: qty scale: %w", err)
	}
	// quant.Price/quant.Qty are plain int64 under the hood: their
	// non-negative invariant is enforced by the quant package's own
	// constructors, not by the type, so a value built by direct
	// conversion (rather than quant.ParsePrice/ParseQty) could still be
	// negative here. Catch that at this boundary too.
	if p.TickSize < 0 {
		return ErrTickSizeNegative
	}
	if p.QtyStepSize < 0 {
		return ErrQtyStepSizeNegative
	}
	return nil
}
