import { describe, expect, it } from "vitest";
import type { Time } from "lightweight-charts";
import type { Candle } from "@/types/domain";
import {
  coordinateToSmoothTimeSeconds,
  forwardSpan,
  timeSecondsToSmoothCoordinate,
  type ChartAccessors,
} from "./geometry";

// Three 5-minute candles: seconds 0, 300, 600. coordinateToLogical is faked
// directly (x IS the logical value, for test clarity) since what is under
// test is the interpolation between candles, not the library's own
// pixel-to-logical mapping.
const CANDLES: Candle[] = [
  { openTime: "1970-01-01T00:00:00Z", open: 1, high: 1, low: 1, close: 1 },
  { openTime: "1970-01-01T00:05:00Z", open: 1, high: 1, low: 1, close: 1 },
  { openTime: "1970-01-01T00:10:00Z", open: 1, high: 1, low: 1, close: 1 },
];

function fakeAccessors(logicalOf: (x: number) => number | null): ChartAccessors {
  const chart = { timeScale: () => ({ coordinateToLogical: (x: number) => logicalOf(x) }) };
  return { chart, series: {} } as unknown as ChartAccessors;
}

// One consistent scale throughout -- 300 seconds (the fixture's own bar
// interval) per logical unit, 20px per logical unit -- so the "direct"
// path (timeToCoordinate, in range) and the "extrapolated" path
// (logicalToCoordinate, out of range) land in the same coordinate space.
// timeToCoordinate returns null outside the data, matching the real
// library (it will not place a bar-aligned coordinate for a time that is
// not an actual bar), which is what forces the extrapolation path to be
// the one under test there.
function fakeLogicalAccessors(): ChartAccessors {
  const toLogical = (seconds: number) => seconds / 300;
  const inRange = (seconds: number) => seconds >= 0 && seconds <= 600;
  const chart = {
    timeScale: () => ({
      logicalToCoordinate: (logical: number) => logical * 20,
      timeToCoordinate: (t: Time) => {
        const seconds = t as unknown as number;
        return inRange(seconds) ? toLogical(seconds) * 20 : null;
      },
    }),
  };
  return { chart, series: {} } as unknown as ChartAccessors;
}

describe("forwardSpan", () => {
  it("spans from the left anchor to the right boundary for a normal in-range pair", () => {
    expect(forwardSpan(100, 800)).toEqual({ x0: 100, width: 700 });
  });

  it("bounds a finite box between two anchors, not just to the pane edge", () => {
    // The position tool's own right time-anchor, not the pane's width --
    // a genuinely bounded box with a finite forward width.
    expect(forwardSpan(100, 300)).toEqual({ x0: 100, width: 200 });
  });

  it("never produces a negative width when the left anchor is beyond the right boundary", () => {
    // This is exactly the shape of the "box fills to the left" bug: an
    // unclamped x0 greater than xRight makes xRight - x0 negative, and a
    // negative width draws backwards (left) from x0 in canvas.
    const { x0, width } = forwardSpan(900, 800);
    expect(x0).toBe(800);
    expect(width).toBe(0);
    expect(width).toBeGreaterThanOrEqual(0);
  });

  it("clamps a negative/off-screen left anchor to 0 rather than growing past it", () => {
    expect(forwardSpan(-50, 800)).toEqual({ x0: 0, width: 800 });
  });
});

describe("coordinateToSmoothTimeSeconds", () => {
  it("returns a candle's exact time at a whole logical index", () => {
    const acc = fakeAccessors(() => 0);
    expect(coordinateToSmoothTimeSeconds(acc, 0, CANDLES)).toBe(0);
  });

  it("interpolates smoothly within a bar, not just at bar boundaries -- the fix for stepped dragging", () => {
    const acc = fakeAccessors(() => 0.5);
    expect(coordinateToSmoothTimeSeconds(acc, 0, CANDLES)).toBe(150);
  });

  it("interpolates between a later pair of candles too", () => {
    const acc = fakeAccessors(() => 1.37);
    expect(coordinateToSmoothTimeSeconds(acc, 0, CANDLES)).toBeCloseTo(300 + 0.37 * 300, 5);
  });

  it("extrapolates smoothly before the first candle, using the first interval", () => {
    const acc = fakeAccessors(() => -2);
    expect(coordinateToSmoothTimeSeconds(acc, 0, CANDLES)).toBe(-600);
  });

  it("extrapolates smoothly past the last candle, using the last interval", () => {
    const acc = fakeAccessors(() => 5);
    expect(coordinateToSmoothTimeSeconds(acc, 0, CANDLES)).toBe(1500);
  });

  it("returns null when the chart cannot resolve a logical position", () => {
    const acc = fakeAccessors(() => null);
    expect(coordinateToSmoothTimeSeconds(acc, 0, CANDLES)).toBeNull();
  });

  it("returns null with fewer than two candles -- nothing to interpolate between", () => {
    const acc = fakeAccessors(() => 0);
    expect(coordinateToSmoothTimeSeconds(acc, 0, [CANDLES[0]])).toBeNull();
  });
});

describe("timeSecondsToSmoothCoordinate", () => {
  const acc = fakeLogicalAccessors(); // logicalToCoordinate(logical) = logical * 20

  it("resolves a candle's exact coordinate at its own time", () => {
    expect(timeSecondsToSmoothCoordinate(acc, 0, CANDLES)).toBe(0); // logical 0 -> 0*20
    expect(timeSecondsToSmoothCoordinate(acc, 600, CANDLES)).toBe(40); // logical 2 -> 2*20
  });

  it("interpolates within a bar", () => {
    expect(timeSecondsToSmoothCoordinate(acc, 150, CANDLES)).toBe(10); // logical 0.5 -> 10
  });

  // This is the regression: a position's rightTime (entry + N bars) lands
  // past the last candle on a short series, timeToCoordinate returns null
  // for it, and the primitive used to skip drawing the box entirely.
  // Extrapolating through the logical scale resolves a real, finite x
  // beyond the last candle's own coordinate instead.
  it("extrapolates a finite coordinate beyond the last candle, further right than the last candle itself", () => {
    const x = timeSecondsToSmoothCoordinate(acc, 900, CANDLES); // 1 bar past the last candle (600)
    expect(x).not.toBeNull();
    expect(x).toBe(60); // logical 3 -> 60
    const lastCandleX = timeSecondsToSmoothCoordinate(acc, 600, CANDLES)!;
    expect(x!).toBeGreaterThan(lastCandleX);
  });

  it("extrapolates before the first candle too", () => {
    expect(timeSecondsToSmoothCoordinate(acc, -300, CANDLES)).toBe(-20); // logical -1 -> -20
  });

  it("returns null with fewer than two candles", () => {
    expect(timeSecondsToSmoothCoordinate(acc, 0, [CANDLES[0]])).toBeNull();
  });
});

describe("position box resolution (regression: rightTime beyond the last candle)", () => {
  // Mirrors PositionPrimitive's own resolveX: timeToCoordinate first, then
  // timeSecondsToSmoothCoordinate's extrapolation, then an absolute
  // fallback -- never null, so the box is never skipped.
  function resolveX(acc: ChartAccessors, t: Time, candles: Candle[], fallback: number): number {
    const direct = acc.chart.timeScale().timeToCoordinate(t);
    if (direct != null) return direct;
    return timeSecondsToSmoothCoordinate(acc, t as unknown as number, candles) ?? fallback;
  }

  it("still produces a drawn box of non-zero width when rightTime is beyond the last candle", () => {
    const acc = fakeLogicalAccessors();
    const entryTime = 300 as unknown as Time; // in range: resolves directly
    const rightTime = 900 as unknown as Time; // 1 bar past the last candle: out of range

    const xe = resolveX(acc, entryTime, CANDLES, 0);
    const xRight = resolveX(acc, rightTime, CANDLES, 99999);

    expect(xRight).toBe(60); // the extrapolated coordinate, not the 99999 fallback
    const { width } = forwardSpan(xe, xRight);
    expect(width).toBeGreaterThan(0);
  });
});
