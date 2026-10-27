package analysis

import "errors"

// Sentinel errors. Use errors.Is.
var (
	// ErrSwingNotFound is returned when no current confirmed swing
	// exists at the requested position.
	ErrSwingNotFound = errors.New("analysis: swing not found")

	// ErrEventNotFound is returned when no current structure event
	// exists for the requested trigger candle.
	ErrEventNotFound = errors.New("analysis: structure event not found")

	// ErrNoClosedCandles is returned by a CandleSource when an
	// instrument has no closed candles at all yet -- a genuinely empty
	// series, not a market-data failure.
	ErrNoClosedCandles = errors.New("analysis: instrument has no closed candles")
)
