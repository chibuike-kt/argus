package hyperliquid

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

var _ ingestion.NativeProvider = (*Provider)(nil)

// wireInterval maps a stored interval to candleSnapshot's own interval
// names (info endpoint docs, "Candle snapshot": 1m, 15m, 1h, 4h among
// them). Explicit rather than string(i), so a new stored interval cannot
// reach the exchange under a name nobody checked.
func wireInterval(i marketdata.Interval) (string, bool) {
	switch i {
	case marketdata.Interval1m:
		return "1m", true
	case marketdata.Interval15m:
		return "15m", true
	case marketdata.Interval1h:
		return "1h", true
	case marketdata.Interval4h:
		return "4h", true
	default:
		return "", false
	}
}

// HistoricalIntervalCandles pages candleSnapshot for interval across
// [start, end) and returns every candle whose window overlaps it, oldest
// first, each open time exactly once (ingestion.NativeProvider).
//
// Paging (L1.1 decision 4): a request whose startTime falls inside a
// window returns the candle containing it (seen on the real endpoint, L1.0:
// a 4h request from 13:00 started with the 12:00 candle), so advancing the
// cursor by one minute, as the 1 minute pager does, would start every later
// page with a repeat of the previous page's last candle. The cursor
// therefore advances to the last returned open time plus one interval, and
// every candle is de-duplicated by its open time (keyed by UnixMilli, never
// by time.Time) in case a server repeats one anyway.
//
// Closure is not decided here: IsFinal is left false and the caller
// (ingestion.Service.IngestNative) stores only candles whose close time
// has passed. Every request is weighed by the shared REST limiter like any
// other call (20 plus 1 per 60 items returned).
func (p *Provider) HistoricalIntervalCandles(ctx context.Context, symbol string, interval marketdata.Interval, start, end time.Time) ([]ingestion.ProviderCandle, error) {
	wi, ok := wireInterval(interval)
	if !ok {
		return nil, fmt.Errorf("hyperliquid: historical %s candles %s: %w", interval, symbol, marketdata.ErrUnknownInterval)
	}
	d, _ := interval.Duration()
	seen := make(map[int64]wireCandle)
	cursor := start
	for cursor.Before(end) {
		var page []wireCandle
		req := candleSnapshotRequest{
			Type: "candleSnapshot",
			Req: candleSnapshotReq{
				Coin:      symbol,
				Interval:  wi,
				StartTime: cursor.UnixMilli(),
				EndTime:   end.UnixMilli(),
			},
		}
		if err := p.postInfo(ctx, 20, req, &page); err != nil {
			return nil, fmt.Errorf("hyperliquid: historical %s candles %s [%s,%s): %w", interval, symbol, cursor, end, err)
		}
		if len(page) == 0 {
			break
		}
		var lastOpen int64
		for _, c := range page {
			seen[c.T] = c
			if c.T > lastOpen {
				lastOpen = c.T
			}
		}
		next := time.UnixMilli(lastOpen).Add(d)
		if !next.After(cursor) {
			// Defensive, as in HistoricalCandles: a page that does not move
			// the cursor forward would otherwise loop forever.
			break
		}
		cursor = next
	}
	opens := make([]int64, 0, len(seen))
	for t := range seen {
		opens = append(opens, t)
	}
	sort.Slice(opens, func(i, j int) bool { return opens[i] < opens[j] })
	out := make([]ingestion.ProviderCandle, len(opens))
	for i, t := range opens {
		out[i] = toProviderCandle(seen[t], symbol)
	}
	return out, nil
}
