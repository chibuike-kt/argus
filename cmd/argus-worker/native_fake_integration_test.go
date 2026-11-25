//go:build integration

package main

import "time"

// fakeWireCandle is a candleSnapshot item as the real exchange sends it.
type fakeWireCandle struct {
	T  int64  `json:"t"`
	C  int64  `json:"T"`
	S  string `json:"s"`
	I  string `json:"i"`
	O  string `json:"o"`
	H  string `json:"h"`
	L  string `json:"l"`
	Cl string `json:"c"`
	V  string `json:"v"`
	N  int64  `json:"n"`
}

// fakeNativeIntervals maps candleSnapshot interval names to their length.
var fakeNativeIntervals = map[string]time.Duration{"1m": time.Minute, "15m": 15 * time.Minute, "1h": time.Hour, "4h": 4 * time.Hour}

// fakeNativeHistory is how far back the fakes serve native candles when a
// test sets no history of its own: a recently listed symbol, so a native
// first-run backfill stays a few dozen rows, not the 5,000 per interval
// the real exchange serves.
const fakeNativeHistory = 6 * time.Hour

// fakeNativeCandles serves candleSnapshot for interval like the real
// exchange: every candle whose window overlaps [startMs, endMs], starting
// with the one containing startMs, up to the one still forming, and none
// older than history (0: fakeNativeHistory). Every value is price, volume 1.
func fakeNativeCandles(coin, interval string, startMs, endMs int64, price string, history time.Duration) []fakeWireCandle {
	d, ok := fakeNativeIntervals[interval]
	if !ok {
		return []fakeWireCandle{}
	}
	if history <= 0 {
		history = fakeNativeHistory
	}
	now := time.Now().UTC()
	start := time.UnixMilli(startMs).UTC()
	if listed := now.Add(-history); start.Before(listed) {
		start = listed
	}
	end := time.UnixMilli(endMs).UTC()
	if end.After(now) {
		end = now
	}
	out := []fakeWireCandle{}
	for m := start.Truncate(d); !m.After(end); m = m.Add(d) {
		out = append(out, fakeWireCandle{T: m.UnixMilli(), C: m.Add(d).UnixMilli() - 1, S: coin, I: interval,
			O: price, H: price, L: price, Cl: price, V: "1", N: 1})
	}
	return out
}
