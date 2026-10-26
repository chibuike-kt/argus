// Package quant defines ARGUS's scaled-integer price and quantity
// primitives (guide section 13; ADR-006, ADR-012).
//
// Money, prices and quantities are never float64. Price and Qty are named
// int64 types holding a value scaled by a fixed number of decimal digits.
// The scale itself is a per-instrument fact (ADR-006), not a per-value one:
// a Price or Qty does not carry its own scale, so every function here that
// parses, formats, rescales or combines values takes the relevant Scale
// explicitly. A named integer type alone does not stop two instruments'
// values from being mixed up — passing Scale explicitly at every call site
// is the actual guard, and callers must not compare or combine a Price or
// Qty across two different scales without an explicit Rescale first.
//
// This package has no dependency on any feature module. Feature modules
// (marketdata, instruments, analysis, setups, ...) depend on it, never the
// other way around.
package quant

import "fmt"

// Scale is the number of digits after the decimal point that a Price or Qty
// value is scaled by. Price(6425050) at Scale(2) represents 64250.50.
//
// Once an instrument has stored candles, its Scale is immutable. Changing
// an instrument's precision is a new instrument identity, never an in-place
// update (approved decision, ADR-006) — reinterpreting existing history at
// a different scale would silently rewrite it.
type Scale int32

const (
	// MinScale is the smallest supported scale: integer values only.
	MinScale Scale = 0

	// MaxScale bounds Scale so that realistic instrument magnitudes stay
	// well inside int64 (max ~9.22e18). It is a defensive boundary for this
	// primitive layer, not a validated instrument-precision policy: if a
	// real provider's precision does not fit, that must be caught and
	// flagged when the instrument registry validates provider metadata
	// (ADR-006, ADR-007), not silently truncated here.
	MaxScale Scale = 12
)

// Validate reports whether s is within the supported range.
func (s Scale) Validate() error {
	if s < MinScale || s > MaxScale {
		return fmt.Errorf("scale %d out of range [%d, %d]", s, MinScale, MaxScale)
	}
	return nil
}
