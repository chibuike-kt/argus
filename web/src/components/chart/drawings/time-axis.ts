// time-axis.ts: converting between a chart's logical bar index and real
// time, including the empty space before the first and after the last
// bar (fix/chart-interaction, item 5).
//
// lightweight-charts' timeScale().timeToCoordinate returns null for any
// time without a bar, so a drawing point past the last candle had no x
// and the drawing vanished. Its logical index, though, keeps extending
// past the data in both directions (logicalToCoordinate and
// coordinateToLogical resolve there). So every conversion goes through
// the logical index:
//   - inside the data, between the series' own data times (gap whitespace
//     included -- it is what the logical index counts), interpolated;
//   - beyond the last bar: lastTime + n * interval; before the first:
//     firstTime - n * interval, n fractional, where interval is the
//     timeframe's bar length.
// Drawing points stay stored as time and price, so a point in the future
// stays at its time when new candles arrive (the mapping is recomputed
// from the new times), and survives a timeframe change and a reload.
// Nothing is added to the series data.

// MAX_BEYOND_BARS is how far past either end of the data a point can be
// placed, in bars of the current timeframe. [U] display value. A point
// dragged further is held at this limit.
export const MAX_BEYOND_BARS = 500;

export interface TimeAxis {
  // Unix seconds of every data point on the series, by logical index
  // (ascending; gap whitespace points included).
  times: readonly number[];
  // The timeframe's bar length in seconds.
  intervalSec: number;
}

const bySeries = new WeakMap<object, TimeAxis>();

export function setSeriesTimeAxis(series: object, axis: TimeAxis): void {
  bySeries.set(series, axis);
}

export function seriesTimeAxis(series: object | undefined): TimeAxis | undefined {
  return series ? bySeries.get(series) : undefined;
}

// clampLogical holds a logical index within MAX_BEYOND_BARS of the data.
export function clampLogical(axis: TimeAxis, logical: number): number {
  const last = axis.times.length - 1;
  return Math.min(Math.max(logical, -MAX_BEYOND_BARS), last + MAX_BEYOND_BARS);
}

// logicalToTime is the time (unix seconds, fractional) at a logical
// index. null for an empty axis.
export function logicalToTime(axis: TimeAxis, logical: number): number | null {
  const t = axis.times;
  if (t.length === 0) return null;
  const last = t.length - 1;
  if (logical >= last) return t[last] + (logical - last) * axis.intervalSec;
  if (logical <= 0) return t[0] + logical * axis.intervalSec;
  const i = Math.floor(logical);
  return t[i] + (logical - i) * (t[i + 1] - t[i]);
}

// timeToLogical is logicalToTime's inverse. null for an empty axis.
export function timeToLogical(axis: TimeAxis, seconds: number): number | null {
  const t = axis.times;
  if (t.length === 0) return null;
  const last = t.length - 1;
  if (seconds >= t[last]) return last + (seconds - t[last]) / axis.intervalSec;
  if (seconds <= t[0]) return (seconds - t[0]) / axis.intervalSec;
  // Binary search for the last index with t[i] <= seconds.
  let lo = 0;
  let hi = last;
  while (hi - lo > 1) {
    const mid = (lo + hi) >> 1;
    if (t[mid] <= seconds) lo = mid;
    else hi = mid;
  }
  const span = t[lo + 1] - t[lo];
  return lo + (span === 0 ? 0 : (seconds - t[lo]) / span);
}
