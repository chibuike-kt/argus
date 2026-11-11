package marketdata

import "context"

// AggregateRange computes the derived-interval candle for every window
// in windows (in the order given -- WindowsInRange's own direction),
// silently excluding any window Aggregate reports as incomplete (brief:
// "incomplete bucket excluded from history", never guessed or
// partially filled). A single DB round trip per window: simple, and
// windows is already capped by the caller (WindowsInRange's own limit),
// so this cannot scale with the full requested range.
func AggregateRange(ctx context.Context, r candleReader, instrumentID int64, windows []Window) ([]AggregateOHLCV, error) {
	out := make([]AggregateOHLCV, 0, len(windows))
	for _, w := range windows {
		agg, incomplete, err := Aggregate(ctx, r, instrumentID, w)
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
