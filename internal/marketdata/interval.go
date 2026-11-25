package marketdata

import (
	"fmt"
	"time"
)

// Interval names a stored candle series (candles.interval, migration
// 000020). 1m is the canonical live series; 15m, 1h and 4h are the native
// higher timeframe series kept from the provider (ADR-006, "Higher
// timeframe candles", decision D11). 5m stays derived from 1m and is never
// stored; 1d is later. The string values are the database values.
type Interval string

// The stored intervals (migration 000020's candles_interval_allowed).
const (
	Interval1m  Interval = "1m"
	Interval15m Interval = "15m"
	Interval1h  Interval = "1h"
	Interval4h  Interval = "4h"
)

// NativeIntervals are the higher timeframe series stored natively, in
// ascending length.
func NativeIntervals() []Interval {
	return []Interval{Interval15m, Interval1h, Interval4h}
}

// Duration is the interval's length; false for anything not stored.
func (i Interval) Duration() (time.Duration, bool) {
	switch i {
	case Interval1m:
		return time.Minute, true
	case Interval15m:
		return 15 * time.Minute, true
	case Interval1h:
		return time.Hour, true
	case Interval4h:
		return 4 * time.Hour, true
	default:
		return 0, false
	}
}

// mustDuration is Duration for an interval already validated by the caller.
func (i Interval) mustDuration() time.Duration {
	d, ok := i.Duration()
	if !ok {
		panic(fmt.Sprintf("marketdata: interval %q is not a stored interval", string(i)))
	}
	return d
}

// Validate fails for any interval the candles table does not store.
func (i Interval) Validate() error {
	if _, ok := i.Duration(); !ok {
		return fmt.Errorf("marketdata: interval %q: %w", string(i), ErrUnknownInterval)
	}
	return nil
}

// Aligned reports whether t is a boundary of i: a whole multiple of the
// interval since the Unix epoch, the same rule as migration 000020's
// candles_open_time_interval_aligned (epoch modulo, so time zones never
// matter). 4h boundaries are 00, 04, 08, 12, 16 and 20 UTC.
func (i Interval) Aligned(t time.Time) bool {
	d, ok := i.Duration()
	if !ok {
		return false
	}
	return t.UnixNano()%int64(d) == 0
}

// WindowStart is the start of the interval window containing t.
func (i Interval) WindowStart(t time.Time) time.Time {
	d := i.mustDuration()
	return time.Unix(0, t.UnixNano()-t.UnixNano()%int64(d)).UTC()
}
