package marketdata

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/chibuike-kt/argus/internal/store"
)

// Chart history for the native intervals (L1.2): 15m, 1h and 4h read the
// stored native candles (ADR-006, "Higher timeframe candles"); a window with
// no stored native candle -- the one that just closed before the worker
// fetched it, or every window of a symbol whose native backfill is still
// running -- is filled from the 1 minute aggregate, and only when every
// minute of it is present and closed (Aggregate's own rule: never a guess,
// never a partial fill). A native candle wins on the same open time.

// NativeIntervalFor is the native interval of length d, if d is one.
func NativeIntervalFor(d time.Duration) (Interval, bool) {
	for _, iv := range NativeIntervals() {
		if dd, _ := iv.Duration(); dd == d {
			return iv, true
		}
	}
	return "", false
}

// nativeAsAggregate is a stored native candle in the aggregate shape the
// API serves: the same wire shape either way, its own revision.
func nativeAsAggregate(c Candle) AggregateOHLCV {
	return AggregateOHLCV{
		Window: Window{Start: c.OpenTime, End: c.CloseTime},
		Open:   c.Open, High: c.High, Low: c.Low, Close: c.Close,
		Volume: c.Volume, Revision: c.Revision,
	}
}

// mergeNativeHistory merges native candles and 1 minute aggregates into one
// series in the requested order, cut to limit: native wins on the same open
// time (keyed by UnixNano, never by time.Time); ascending keeps the oldest
// limit candles, descending the newest limit, newest first.
func mergeNativeHistory(native []Candle, aggs []AggregateOHLCV, desc bool, limit int) []AggregateOHLCV {
	byOpen := make(map[int64]AggregateOHLCV, len(native)+len(aggs))
	for _, a := range aggs {
		byOpen[a.Window.Start.Time().UnixNano()] = a
	}
	for _, c := range native {
		byOpen[c.OpenTime.Time().UnixNano()] = nativeAsAggregate(c)
	}
	opens := make([]int64, 0, len(byOpen))
	for k := range byOpen {
		opens = append(opens, k)
	}
	sort.Slice(opens, func(i, j int) bool {
		if desc {
			return opens[i] > opens[j]
		}
		return opens[i] < opens[j]
	})
	if len(opens) > limit {
		opens = opens[:limit]
	}
	out := make([]AggregateOHLCV, len(opens))
	for i, k := range opens {
		out[i] = byOpen[k]
	}
	return out
}

// NativeHistory returns closed candles of interval for instrumentID, at
// most limit, oldest first (desc false) or newest first (desc true):
//
//   - from and to both set: candles opening in [from, to);
//   - desc with to only, or neither ("the latest N"): the newest candles
//     opening before to (or before the window still forming at now).
//
// Ascending needs both bounds (the caller enforces it). Cost: one indexed
// read of at most limit native rows, plus one indexed read of the 1 minute
// rows under the windows that have no native candle -- at the recent edge
// that is a few minutes; for a symbol with no native history yet it is the
// same 1 minute read the aggregated intervals make.
func (s *Store) NativeHistory(ctx context.Context, interval Interval, instrumentID int64, from, to *time.Time, desc bool, limit32 int32, now time.Time) ([]AggregateOHLCV, error) {
	if err := interval.Validate(); err != nil {
		return nil, err
	}
	if interval == Interval1m {
		return nil, fmt.Errorf("marketdata: native history is for 15m, 1h and 4h, not %s", interval)
	}
	if limit32 < 1 {
		return nil, nil
	}
	limit := int(limit32)
	d := interval.mustDuration()

	var native []Candle
	var err error
	if desc {
		native, err = s.listClosedDescAt(ctx, interval, instrumentID, from, to, limit32)
	} else {
		if from == nil || to == nil {
			return nil, errors.New("marketdata: ascending native history needs from and to")
		}
		native, err = s.listClosedAscAt(ctx, interval, instrumentID, *from, *to, limit32)
	}
	if err != nil {
		return nil, err
	}

	// The windows the 1 minute aggregate may fill: the span this page can
	// cover, minus the windows a native candle already has.
	hi := interval.WindowStart(now) // the window still forming never qualifies
	if to != nil && to.Before(hi) {
		hi = *to
	}
	var lo time.Time
	full := len(native) == limit
	switch {
	case desc && full:
		lo = native[len(native)-1].OpenTime.Time() // older windows would be cut anyway
	case desc && from != nil:
		lo = *from
	case desc:
		lo = hi.Add(-time.Duration(limit) * d)
	default: // ascending: [from, to), up to the newest native candle when the page is full
		lo = *from
		if full {
			if end := native[len(native)-1].OpenTime.Time().Add(d); end.Before(hi) {
				hi = end
			}
		}
	}
	if from != nil && lo.Before(*from) {
		lo = *from
	}
	have := make(map[int64]bool, len(native))
	for _, c := range native {
		have[c.OpenTime.Time().UnixNano()] = true
	}
	var missing []Window
	for _, w := range WindowsInRange(lo, hi, d, desc, limit) {
		if !have[w.Start.Time().UnixNano()] {
			missing = append(missing, w)
		}
	}
	var aggs []AggregateOHLCV
	if len(missing) > 0 {
		if aggs, err = s.AggregateRange(ctx, instrumentID, missing); err != nil {
			return nil, err
		}
	}
	return mergeNativeHistory(native, aggs, desc, limit), nil
}

func (s *Store) listClosedAscAt(ctx context.Context, interval Interval, instrumentID int64, from, to time.Time, limit int32) ([]Candle, error) {
	rows, err := s.q.ListClosedCandlesInRangeLimited(ctx, store.ListClosedCandlesInRangeLimitedParams{
		InstrumentID: instrumentID, OpenTime: from, OpenTime_2: to, Limit: limit, Interval: string(interval),
	})
	if err != nil {
		return nil, fmt.Errorf("marketdata: list closed %s candles instrument=%d [%s,%s): %w", interval, instrumentID, from, to, err)
	}
	out := make([]Candle, len(rows))
	for i, row := range rows {
		out[i] = fromCandleRow(row)
	}
	return out, nil
}

func (s *Store) listClosedDescAt(ctx context.Context, interval Interval, instrumentID int64, from, to *time.Time, limit int32) ([]Candle, error) {
	rows, err := s.q.ListClosedCandlesInRangeLimitedDesc(ctx, store.ListClosedCandlesInRangeLimitedDescParams{
		InstrumentID: instrumentID, FromTime: toNullTime(from), ToTime: toNullTime(to), Limit: limit, Interval: string(interval),
	})
	if err != nil {
		return nil, fmt.Errorf("marketdata: list closed %s candles desc instrument=%d [%v,%v): %w", interval, instrumentID, from, to, err)
	}
	out := make([]Candle, len(rows))
	for i, row := range rows {
		out[i] = fromCandleRow(row)
	}
	return out, nil
}

// LatestJoinWindow is the join point with the live stream for an interval
// of length d (the response's last_closed_open_time, and the hub's
// snapshot): for a native interval, the later of the newest stored native
// closed candle and the latest complete 1 minute aggregate window (the one
// the chart serves for that window: native on a tie, with its own
// revision); for every other interval, LatestCompleteWindow unchanged.
// Using one function for both the endpoint and the hub keeps the history
// and the live stream on the same join point.
func (s *Store) LatestJoinWindow(ctx context.Context, instrumentID int64, d time.Duration) (*AggregateOHLCV, error) {
	agg, err := s.LatestCompleteWindow(ctx, instrumentID, d)
	if err != nil {
		return nil, err
	}
	iv, ok := NativeIntervalFor(d)
	if !ok {
		return agg, nil
	}
	native, err := s.GetLatestClosedAt(ctx, iv, instrumentID)
	if errors.Is(err, ErrCandleNotFound) {
		return agg, nil
	}
	if err != nil {
		return nil, err
	}
	if agg != nil && agg.Window.Start.Time().After(native.OpenTime.Time()) {
		return agg, nil
	}
	n := nativeAsAggregate(native)
	return &n, nil
}
