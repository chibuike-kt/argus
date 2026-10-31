import { describe, expect, it } from "vitest";
import type { Candle } from "@/types/domain";
import { coordinateToSmoothTimeSeconds, forwardSpan, type ChartAccessors } from "./geometry";

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
