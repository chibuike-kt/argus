package marketdata

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
)

// AggregateRange computes the derived-interval candle for every window
// in windows (in the order given -- WindowsInRange's own direction),
// silently excluding any window Aggregate would report as incomplete
// (brief: "incomplete bucket excluded from history", never guessed or
// partially filled).
//
// One range query total (adr-004-ws-contract-review.md addendum item
// B), not one per window: the previous version called Aggregate once
// per window, each its own round trip re-reading every minute that
// window covers. This fetches every 1-minute row across the full
// [min(windows[i].Start), max(windows[i].End)) span once, then reduces
// each window from that same in-memory set -- the number of database
// calls no longer grows with the number of bars requested.
//
// The in-memory reduction itself buckets those rows exactly once (via
// rowsByOpenTime), then calls reduceWindow per window against that one
// shared bucket. The version that first shipped the single query still
// called aggregateFromRows once per window, and that function rebuilt
// its own bucket from the full row set every time -- O(windows *
// total_rows) instead of O(total_rows) to bucket plus O(sum of window
// sizes) to reduce. Profiling a 500-bar hourly request showed that
// rebuild dominating the single query it was meant to replace (roughly
// 70% of the end-to-end call, more than the query itself).
func AggregateRange(ctx context.Context, r candleReader, instrumentID int64, windows []Window) ([]AggregateOHLCV, error) {
	if len(windows) == 0 {
		return []AggregateOHLCV{}, nil
	}

	start, end := windows[0].Start.Time(), windows[0].End.Time()
	for _, w := range windows[1:] {
		if w.Start.Time().Before(start) {
			start = w.Start.Time()
		}
		if w.End.Time().After(end) {
			end = w.End.Time()
		}
	}

	rows, err := r.ListCurrentCandlesInRange(ctx, instrumentID, start, end)
	if err != nil {
		return nil, fmt.Errorf("marketdata: aggregate range instrument=%d window=[%s,%s): %w", instrumentID, start, end, err)
	}

	present := rowsByOpenTime(rows)

	out := make([]AggregateOHLCV, 0, len(windows))
	for _, w := range windows {
		agg, incomplete, err := reduceWindow(instrumentID, w, present)
		if err != nil {
			return nil, err
		}
		if incomplete != nil {
			continue
		}
		out = append(out, *agg)
	}
	return out, nil
}

// AggregateRange is the free function of the same name, bound to this
// store's own candle data -- the method a caller outside this package
// (internal/httpapi) calls, so it never needs to know candleReader
// exists (unexported) or import internal/store to satisfy it: this
// method, like Aggregate/Window/AggregateOHLCV, speaks only in this
// package's own exported types.
func (s *Store) AggregateRange(ctx context.Context, instrumentID int64, windows []Window) ([]AggregateOHLCV, error) {
	return AggregateRange(ctx, s, instrumentID, windows)
}

// maxLatestWindowAttempts bounds LatestCompleteWindow's own backward
// search: an implementation choice (not a brief `[U]` business value),
// kept small because a real gap longer than this is already surfaced by
// gap detection elsewhere -- this join-point lookup's own job is to
// find the latest window quickly, not to search across an unbounded
// outage.
const maxLatestWindowAttempts = 6

// LatestCompleteWindow returns the most recent complete window of
// duration d, at or before the instrument's own latest closed 1-minute
// candle -- the per-interval join point
// (adr-004-ws-contract-review.md, review item 5): phase 1's REST
// response and the WebSocket snapshot payload both need "the latest
// closed bar of the chart's own interval," not the latest closed
// 1-minute candle regardless of what interval is showing. nil, nil when
// the instrument has no closed candle at all yet, or when no complete
// window can be found within maxLatestWindowAttempts steps back (a gap
// too wide to search through here, not guessed at -- CLAUDE.md rule 3).
func (s *Store) LatestCompleteWindow(ctx context.Context, instrumentID int64, d time.Duration) (*AggregateOHLCV, error) {
	latest, err := s.GetLatestClosed(ctx, instrumentID)
	if errors.Is(err, ErrCandleNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if d == time.Minute {
		return &AggregateOHLCV{
			Window: Window{Start: latest.OpenTime, End: latest.CloseTime},
			Open:   latest.Open, High: latest.High, Low: latest.Low, Close: latest.Close,
			Volume: latest.Volume, Revision: latest.Revision,
		}, nil
	}

	w := UTCWindow(latest.OpenTime, d)
	for attempt := 0; attempt < maxLatestWindowAttempts; attempt++ {
		agg, incomplete, err := Aggregate(ctx, s, instrumentID, w)
		if err != nil {
			return nil, err
		}
		if incomplete == nil {
			return agg, nil
		}
		w = Window{
			Start: clock.NewEventTime(w.Start.Time().Add(-d)),
			End:   w.Start,
		}
	}
	return nil, nil
}
