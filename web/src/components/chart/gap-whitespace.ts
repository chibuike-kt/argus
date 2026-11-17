import type { CandlestickData, Time, WhitespaceData } from "lightweight-charts";

// Lightweight Charts places consecutive data points side by side whatever
// their times, so a run of missing candles would vanish and the time axis
// would jump. withGapWhitespace inserts a whitespace point (time only, no
// price) for every missing interval slot between two candles, so a hole in
// the data shows as a hole on the chart.
//
// Display only: it never invents a price, and never touches the stored data
// or the analysis -- the result is only ever handed to series.setData.
//
// bars must be ascending by time, all at intervalSec spacing when nothing is
// missing. A slot is "missing" when the next bar's time is more than one
// interval after the previous one. O(n + inserted).
export function withGapWhitespace(
  bars: CandlestickData<Time>[],
  intervalSec: number,
): (CandlestickData<Time> | WhitespaceData<Time>)[] {
  if (bars.length < 2 || intervalSec <= 0) return bars;
  const out: (CandlestickData<Time> | WhitespaceData<Time>)[] = [];
  let prev: number | null = null;
  for (const bar of bars) {
    const t = bar.time as number;
    if (prev !== null) {
      for (let slot = prev + intervalSec; slot < t; slot += intervalSec) {
        out.push({ time: slot as Time });
      }
    }
    out.push(bar);
    prev = t;
  }
  return out;
}

// INTERVAL_SECONDS is each chart timeframe's bar spacing.
export const INTERVAL_SECONDS: Record<string, number> = {
  "1m": 60,
  "5m": 300,
  "15m": 900,
  "1h": 3600,
  "4h": 14_400,
  "1d": 86_400,
};
