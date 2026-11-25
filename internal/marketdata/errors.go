package marketdata

import "errors"

// Sentinel errors. Use errors.Is.
var (
	ErrInstrumentIDRequired = errors.New("marketdata: instrument id is required")
	ErrProviderRequired     = errors.New("marketdata: provider is required")
	ErrNotMinuteAligned     = errors.New("marketdata: open_time is not aligned to a 1-minute boundary")
	ErrOHLCNegative         = errors.New("marketdata: open/high/low/close must not be negative")
	ErrVolumeNegative       = errors.New("marketdata: volume must not be negative")
	ErrOHLCInconsistent     = errors.New("marketdata: high/low must bound open and close")

	// ErrCandleNotFound is returned when no current candle exists at the
	// given (instrument, open_time).
	ErrCandleNotFound = errors.New("marketdata: candle not found")

	// ErrCandleAlreadyClosed is returned by UpsertForming when the current
	// candle at that minute is already closed: a forming value never
	// overwrites a closed candle (it is older information than the close).
	// A caller holding a FINAL value goes through the correction path.
	ErrCandleAlreadyClosed = errors.New("marketdata: candle is already closed; a forming value cannot overwrite it")

	// ErrCandleStillForming is returned by SupersedeCandle: only a closed
	// candle can be corrected (000003) -- a still-forming one should be
	// overwritten with UpsertForming instead.
	ErrCandleStillForming = errors.New("marketdata: candle is still forming, cannot be superseded")

	// ErrGapNotFound and ErrGapAlreadyResolved distinguish the two reasons
	// ResolveGap can affect zero rows.
	ErrGapNotFound        = errors.New("marketdata: gap not found")
	ErrGapAlreadyResolved = errors.New("marketdata: gap is already resolved")
	ErrResolutionRequired = errors.New("marketdata: resolution is required")

	// ErrStaleQuote is returned by UpsertQuote when the incoming quote is
	// older than the one already stored (guide 16: an out-of-order
	// upstream message must not silently become the current value).
	ErrStaleQuote = errors.New("marketdata: quote is older than the stored latest quote")

	// ErrUnknownInterval is returned for an interval the candles table does
	// not store (migration 000020 allows 1m, 15m, 1h and 4h).
	ErrUnknownInterval = errors.New("marketdata: unknown candle interval")

	// ErrNotIntervalAligned is returned when a native candle's open time is
	// not a boundary of its interval.
	ErrNotIntervalAligned = errors.New("marketdata: open_time is not aligned to its interval")

	// ErrNativeCandleNotClosed is returned by InsertClosed when the candle's
	// close time has not passed yet: only closed native candles are stored.
	ErrNativeCandleNotClosed = errors.New("marketdata: native candle has not closed yet")

	// ErrQuoteNotFound is returned when no latest quote exists yet for an
	// instrument.
	ErrQuoteNotFound = errors.New("marketdata: quote not found")
)
