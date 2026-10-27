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

	// ErrSweepNotFound is returned when no current liquidity sweep
	// exists for the requested trigger candle and side.
	ErrSweepNotFound = errors.New("analysis: liquidity sweep not found")

	// ErrNoClosedCandles is returned by a CandleSource when an
	// instrument has no closed candles at all yet -- a genuinely empty
	// series, not a market-data failure.
	ErrNoClosedCandles = errors.New("analysis: instrument has no closed candles")
)
