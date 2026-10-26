package quant

import (
	"encoding/json"
	"fmt"
)

// Amount is the wire representation of a Price or Qty: an exact decimal
// string plus the scale it was rendered at. API and storage boundaries
// exchange Amount, never a bare JSON number (approved decision) — the
// Value field's Go type is string, so encoding/json already refuses to
// decode a JSON number into it, which is what stops a client's float64
// parser from silently losing precision.
type Amount struct {
	Value string `json:"value"`
	Scale Scale  `json:"scale"`
}

// PriceAmount renders p as a wire Amount at scale.
func PriceAmount(p Price, scale Scale) Amount {
	return Amount{Value: p.String(scale), Scale: scale}
}

// QtyAmount renders q as a wire Amount at scale.
func QtyAmount(q Qty, scale Scale) Amount {
	return Amount{Value: q.String(scale), Scale: scale}
}

// Price decodes a into a Price at its own scale. It is exact: a.Scale is
// authoritative and no rounding happens here.
func (a Amount) Price() (Price, error) {
	if err := a.Scale.Validate(); err != nil {
		return 0, fmt.Errorf("amount scale: %w", err)
	}
	p, err := ParsePrice(a.Value, a.Scale)
	if err != nil {
		return 0, fmt.Errorf("amount to price: %w", err)
	}
	return p, nil
}

// Qty decodes a into a Qty at its own scale. Same rules as Price.
func (a Amount) Qty() (Qty, error) {
	if err := a.Scale.Validate(); err != nil {
		return 0, fmt.Errorf("amount scale: %w", err)
	}
	q, err := ParseQty(a.Value, a.Scale)
	if err != nil {
		return 0, fmt.Errorf("amount to qty: %w", err)
	}
	return q, nil
}

// UnmarshalJSON decodes an Amount and validates its scale at the boundary
// (guide section 1), wrapping encoding/json's native type-mismatch error
// (e.g. a JSON number where a decimal string was required) with context.
func (a *Amount) UnmarshalJSON(data []byte) error {
	type wire Amount // avoid recursing back into this method
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("decode amount: %w", err)
	}
	if err := w.Scale.Validate(); err != nil {
		return fmt.Errorf("decode amount: %w", err)
	}
	*a = Amount(w)
	return nil
}
