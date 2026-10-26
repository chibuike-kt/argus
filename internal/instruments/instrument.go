package instruments

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/store"
)

// Instrument is the domain view of a registered instrument. PriceScale and
// QtyScale are immutable facts about the row (enforced by
// trg_instruments_prevent_scale_update in 000002, not by this type); every
// Price/Qty-denominated field on this struct is already scaled by the
// matching one of the two.
type Instrument struct {
	ID                 int64
	Provider           string
	NativeSymbol       string
	AssetClass         string
	BaseAsset          *string
	QuoteAsset         *string
	ContractType       *string
	PriceScale         quant.Scale
	QtyScale           quant.Scale
	TickSize           quant.Price  // scaled by PriceScale
	QtyStepSize        quant.Qty    // scaled by QtyScale
	ContractSize       *quant.Qty   // scaled by QtyScale
	PipSize            *quant.Price // scaled by PriceScale
	SupportedIntervals []string
	Enabled            bool
	Metadata           json.RawMessage
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// fromRow converts a generated store row into the domain type, validating
// the scale columns on the way out. This is read-path, defense-in-depth
// validation: the schema's constraints and triggers are the actual
// guarantee, but a row written by a different code version or a direct
// psql edit should still be caught here rather than silently trusted.
func fromRow(row store.Instrument) (Instrument, error) {
	priceScale := quant.Scale(row.PriceScale)
	if err := priceScale.Validate(); err != nil {
		return Instrument{}, fmt.Errorf("instruments: row %d has invalid price_scale: %w", row.ID, err)
	}
	qtyScale := quant.Scale(row.QtyScale)
	if err := qtyScale.Validate(); err != nil {
		return Instrument{}, fmt.Errorf("instruments: row %d has invalid qty_scale: %w", row.ID, err)
	}

	inst := Instrument{
		ID:                 row.ID,
		Provider:           row.Provider,
		NativeSymbol:       row.NativeSymbol,
		AssetClass:         row.AssetClass,
		BaseAsset:          nullStringToPtr(row.BaseAsset),
		QuoteAsset:         nullStringToPtr(row.QuoteAsset),
		ContractType:       nullStringToPtr(row.ContractType),
		PriceScale:         priceScale,
		QtyScale:           qtyScale,
		TickSize:           quant.Price(row.TickSize),
		QtyStepSize:        quant.Qty(row.QtyStepSize),
		SupportedIntervals: row.SupportedIntervals,
		Enabled:            row.Enabled,
		Metadata:           row.Metadata,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}
	if row.ContractSize.Valid {
		v := quant.Qty(row.ContractSize.Int64)
		inst.ContractSize = &v
	}
	if row.PipSize.Valid {
		v := quant.Price(row.PipSize.Int64)
		inst.PipSize = &v
	}
	return inst, nil
}
