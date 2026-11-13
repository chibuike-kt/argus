package marketdata

import (
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
)

// WindowsInRange returns the UTCWindow-aligned windows of length d that
// cover [from, to), in the requested direction, capped at limit windows.
// Windows align to their own interval boundary (UTCWindow's own
// truncation), not to from/to themselves -- a derived candle at 5m
// always starts on a clean 5-minute boundary like any other candle at
// that interval, never at an arbitrary query time. A nil result means
// an invalid range (to not after from) or a non-positive limit.
//
// asc (desc=false): oldest first, starting at the aligned window
// containing from, stepping forward while a window's own Start is
// before to.
//
// desc: newest first, starting at the last aligned window that ends at
// or before to (never a window straddling to, which would still be
// forming as of that boundary), stepping backward while a window's own
// End is after from.
func WindowsInRange(from, to time.Time, d time.Duration, desc bool, limit int) []Window {
	if limit <= 0 || !to.After(from) {
		return nil
	}

	if desc {
		w := UTCWindow(clock.NewEventTime(to), d)
		if w.End.Time().After(to) {
			w = stepBack(w, d)
		}
		var out []Window
		for w.End.Time().After(from) && len(out) < limit {
			out = append(out, w)
			w = stepBack(w, d)
		}
		return out
	}

	w := UTCWindow(clock.NewEventTime(from), d)
	var out []Window
	for w.Start.Time().Before(to) && len(out) < limit {
		out = append(out, w)
		w = UTCWindow(clock.NewEventTime(w.End.Time()), d)
	}
	return out
}

// stepBack returns the window of length d immediately before w.
func stepBack(w Window, d time.Duration) Window {
	return UTCWindow(clock.NewEventTime(w.Start.Time().Add(-1)), d)
}
