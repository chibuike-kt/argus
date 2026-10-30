import type { IChartApi, ISeriesApi, SeriesType, Time } from "lightweight-charts";
import type { Candle } from "@/types/domain";
import type { DPoint } from "./types";

// The minimal chart/series surface the drawing engine needs for coordinate
// conversion. A thin alias so geometry/controller code depends on behaviour,
// not on which concrete series type chart-surface happens to use.
export interface ChartAccessors {
  chart: IChartApi;
  series: ISeriesApi<SeriesType, Time>;
}

export interface Px {
  x: number;
  y: number;
}

// Chart space -> pixel space. Null when the point is currently off-scale
// (e.g. price scale not yet sized) -- callers skip drawing/hit-testing it
// rather than guessing a coordinate.
export function toPixel({ chart, series }: ChartAccessors, p: DPoint): Px | null {
  const x = chart.timeScale().timeToCoordinate(p.time);
  const y = series.priceToCoordinate(p.price);
  if (x == null || y == null) return null;
  return { x, y };
}

// Pixel space -> chart space, for turning a raw pointer event into a point
// a drawing can store. fallbackTime covers the clamped edge where the time
// scale cannot resolve the pixel to a bar (e.g. past the last candle) --
// callers pass the chart's own last known time rather than silently
// dropping the point, matching how the chart's own crosshair clamps.
export function toPoint({ chart, series }: ChartAccessors, px: Px, fallbackTime: Time): DPoint | null {
  const price = series.coordinateToPrice(px.y);
  if (price == null) return null;
  const time = chart.timeScale().coordinateToTime(px.x);
  return { time: (time ?? fallbackTime) as Time, price };
}

export function distance(a: Px, b: Px): number {
  return Math.hypot(a.x - b.x, a.y - b.y);
}

// Shortest distance from point p to the segment [a, b], for line/edge hit
// testing with a pixel tolerance.
export function distanceToSegment(p: Px, a: Px, b: Px): number {
  const abx = b.x - a.x;
  const aby = b.y - a.y;
  const lenSq = abx * abx + aby * aby;
  if (lenSq === 0) return distance(p, a);
  let t = ((p.x - a.x) * abx + (p.y - a.y) * aby) / lenSq;
  t = Math.max(0, Math.min(1, t));
  return distance(p, { x: a.x + t * abx, y: a.y + t * aby });
}

export function insideRect(p: Px, a: Px, b: Px): boolean {
  const left = Math.min(a.x, b.x);
  const right = Math.max(a.x, b.x);
  const top = Math.min(a.y, b.y);
  const bottom = Math.max(a.y, b.y);
  return p.x >= left && p.x <= right && p.y >= top && p.y <= bottom;
}

// --- Magnet: snap a candidate point to the nearest candle's O/H/L/C ---

function unix(iso: string): number {
  return Math.floor(Date.parse(iso) / 1000);
}

// Finds the candle whose open_time is closest to point.time, then returns
// whichever of its O/H/L/C sits closest to point.price. Both axes snap
// independently of pixel distance -- magnet is a data-space concept (snap to
// a real traded price), not a screen-space one.
export function snapToCandle(point: DPoint, candles: Candle[]): DPoint {
  if (candles.length === 0) return point;
  const t = typeof point.time === "number" ? point.time : unix(point.time as unknown as string);

  let nearest = candles[0];
  let best = Infinity;
  for (const c of candles) {
    const d = Math.abs(unix(c.openTime) - t);
    if (d < best) {
      best = d;
      nearest = c;
    }
  }

  const candidates = [nearest.open, nearest.high, nearest.low, nearest.close];
  let price = candidates[0];
  let bestPriceDist = Math.abs(candidates[0] - point.price);
  for (const c of candidates) {
    const d = Math.abs(c - point.price);
    if (d < bestPriceDist) {
      bestPriceDist = d;
      price = c;
    }
  }

  return { time: unix(nearest.openTime) as Time, price };
}

// Number of candles whose open_time falls strictly between t1 and t2 (order
// independent), for the measure tool's bar-count readout.
export function barsBetween(t1: Time, t2: Time, candles: Candle[]): number {
  const a = typeof t1 === "number" ? t1 : unix(t1 as unknown as string);
  const b = typeof t2 === "number" ? t2 : unix(t2 as unknown as string);
  const [lo, hi] = a <= b ? [a, b] : [b, a];
  return candles.filter((c) => {
    const t = unix(c.openTime);
    return t > lo && t <= hi;
  }).length;
}

export function timeSeconds(t: Time): number {
  return typeof t === "number" ? t : unix(t as unknown as string);
}

// The position tool (and anything else that projects forward from a left
// anchor time to a right boundary, never back across history) clamps the
// left edge to 0 first, then never lets the right edge sit left of it, so
// the resulting width is never negative -- a raw `xRight - xLeft` is only
// safe to feed to fillRect/strokeRect once that ordering is guaranteed, and
// canvas draws a negative width backwards from x, which is exactly a box
// that fills to the left instead of projecting right.
//
// xRight is the position's own right time-anchor's x (bounded box, a fixed
// forward width), or the pane's width for anything that still means "project
// to the edge" (callers choose which by what they pass).
export function forwardSpan(xLeft: number, xRight: number): { x0: number; width: number } {
  const x0 = Math.max(0, Math.min(xLeft, xRight));
  const x1 = Math.max(x0, xRight);
  return { x0, width: x1 - x0 };
}
