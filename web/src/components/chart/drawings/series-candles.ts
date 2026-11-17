import type { Candle } from "@/types/domain";

// The candles currently on a chart, keyed by that chart's own series. The
// drawing primitives need the chart's real candles (the measure tool's bar
// count, the position tool's projection past the last bar), but they are
// created per drawing and only ever see their series -- so the series is
// the key. DrawingController.setCandles keeps it current on every data
// change (load, instrument switch, timeframe switch), so a primitive always
// reads the selected instrument's own candles at its own timeframe, never
// a fixed sample set.
//
// A WeakMap: a series that is removed with its chart drops its entry.
const bySeries = new WeakMap<object, Candle[]>();

export function setSeriesCandles(series: object, candles: Candle[]): void {
  bySeries.set(series, candles);
}

// seriesCandles returns the candles registered for series, or none.
export function seriesCandles(series: object | undefined): Candle[] {
  return series ? (bySeries.get(series) ?? []) : [];
}
