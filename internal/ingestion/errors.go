package ingestion

import "errors"

// Sentinel errors. Use errors.Is. Most are returned wrapped with context
// (provider, symbol, field, value) rather than bare.
var (
	// ErrNotMinuteAligned is returned when a ProviderCandle's OpenTime is
	// not aligned to a 1-minute boundary.
	ErrNotMinuteAligned = errors.New("ingestion: open_time is not aligned to a 1-minute boundary")

	// ErrOHLCInconsistent is returned when high/low do not bound open and
	// close.
	ErrOHLCInconsistent = errors.New("ingestion: high/low must bound open and close")

	// ErrScaleMismatch wraps quant.ErrPrecisionLoss specifically: the
	// provider sent a value with more decimal precision than this
	// instrument's registered price_scale or qty_scale allows. This is
	// never silently rounded (guide 1, 13) -- it is a real discrepancy
	// between what the provider sends and what our registry believes
	// about this instrument, and it is surfaced as a rejection rather
	// than quarantined for later replay (an explicit, flagged scope
	// decision for this slice; see normalize.go's package doc).
	ErrScaleMismatch = errors.New("ingestion: value exceeds instrument's registered scale")
)
