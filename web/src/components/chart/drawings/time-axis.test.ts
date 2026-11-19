import { describe, expect, it } from "vitest";
import { MAX_BEYOND_BARS, clampLogical, logicalToTime, timeToLogical, type TimeAxis } from "./time-axis";
import { DrawingsStore, type DrawingsPersistence } from "./store";
import { DEFAULT_STYLE, type Drawing, type TrendLineDrawing } from "./types";
import type { Time } from "lightweight-charts";

// Drawings in the empty space past either end of the data
// (fix/chart-interaction, item 5).

const M5 = 300;
const T0 = Date.UTC(2026, 10, 19, 12, 0) / 1000;

// 10 five-minute bars, with a gap (whitespace point) at index 4 -- the
// chart's logical index counts it.
function axis(n = 10): TimeAxis {
  return { times: Array.from({ length: n }, (_, i) => T0 + i * M5), intervalSec: M5 };
}

describe("time axis", () => {
  it("inside the data: interpolates between data times, and round-trips", () => {
    const a = axis();
    expect(logicalToTime(a, 3)).toBe(T0 + 3 * M5);
    expect(logicalToTime(a, 3.5)).toBe(T0 + 3.5 * M5);
    expect(timeToLogical(a, T0 + 3.5 * M5)).toBeCloseTo(3.5);
  });

  it("after the last bar: last time + n * interval (a real future time)", () => {
    const a = axis();
    const last = T0 + 9 * M5;
    expect(logicalToTime(a, 9 + 20)).toBe(last + 20 * M5);
    expect(timeToLogical(a, last + 20 * M5)).toBe(29);
  });

  it("before the first bar: first time - n * interval", () => {
    const a = axis();
    expect(logicalToTime(a, -7)).toBe(T0 - 7 * M5);
    expect(timeToLogical(a, T0 - 7 * M5)).toBe(-7);
  });

  it(`is held within ${MAX_BEYOND_BARS} bars of the data`, () => {
    const a = axis();
    expect(clampLogical(a, 9 + 10_000)).toBe(9 + MAX_BEYOND_BARS);
    expect(clampLogical(a, -10_000)).toBe(-MAX_BEYOND_BARS);
    expect(clampLogical(a, 12.5)).toBe(12.5);
  });

  it("a point in the future stays at its time when new candles arrive (not at its screen position)", () => {
    const future = T0 + 30 * M5; // 21 bars past the last of 10
    const before = timeToLogical(axis(10), future)!;
    const after = timeToLogical(axis(12), future)!; // two new bars
    expect(before).toBe(30);
    expect(after).toBe(30); // same logical slot: the same time, the bars caught up to it
    expect(logicalToTime(axis(12), after)).toBe(future);
  });

  it("uses the timeframe's interval, not one candle pair's spacing", () => {
    // The last two data points are 3 bars apart (a gap at the end of the data).
    const a: TimeAxis = { times: [T0, T0 + M5, T0 + 4 * M5], intervalSec: M5 };
    expect(logicalToTime(a, 3)).toBe(T0 + 5 * M5);
  });
});

class MemoryPersistence implements DrawingsPersistence {
  store = new Map<string, Drawing[]>();
  load(k: string) {
    const v = this.store.get(k);
    return v ? JSON.parse(JSON.stringify(v)) : null; // as a reload would read it back
  }
  save(k: string, d: Drawing[]) {
    this.store.set(k, JSON.parse(JSON.stringify(d)));
  }
}

describe("a line with its second point in the future", () => {
  const future = T0 + 40 * M5;
  const line: TrendLineDrawing = {
    id: "l1",
    kind: "trendline",
    p1: { time: (T0 + 2 * M5) as Time, price: 100 },
    p2: { time: future as Time, price: 120 },
    style: DEFAULT_STYLE,
    createdAt: 0,
    updatedAt: 0,
  };

  it("keeps both points after a reload, and after switching 5m to 1h and back", () => {
    const p = new MemoryPersistence();
    const s1 = new DrawingsStore(p);
    s1.setScope("6:5m");
    s1.add(line);

    // Reload: a new store over the same persisted data.
    const s2 = new DrawingsStore(p);
    s2.setScope("6:5m");
    expect(s2.get("l1")).toMatchObject({ p1: line.p1, p2: { time: future, price: 120 } });

    // 5m -> 1h -> 5m.
    s2.setScope("6:1h");
    expect(s2.get("l1")).toBeUndefined(); // drawings are per instrument and timeframe
    s2.setScope("6:5m");
    expect(s2.get("l1")).toMatchObject({ p2: { time: future, price: 120 } });

    // And it still maps to a real slot past the data, before and after new bars.
    expect(timeToLogical(axis(10), future)).toBe(40);
    expect(timeToLogical(axis(15), future)).toBe(40);
  });
});

// lightweight-charts 5.2.1 resolves only integer logical indexes
// (indexToCoordinate returns 0 for a fractional one, coordinateToLogical
// rounds up): logicalToX and xToLogical must work around both.
import { logicalToX, xToLogical } from "./geometry";
import type { IChartApi } from "lightweight-charts";

function fakeChart(barSpacing = 8, x0 = 100): IChartApi {
  const ts = {
    logicalToCoordinate: (i: number) => (Number.isInteger(i) ? x0 + i * barSpacing : 0),
    coordinateToLogical: (x: number) => Math.ceil((x - x0) / barSpacing),
  };
  return { timeScale: () => ts } as unknown as IChartApi;
}

describe("logical index <-> x (library work-arounds)", () => {
  it("a fractional index resolves between its integer neighbours, not to 0", () => {
    const c = fakeChart();
    expect(logicalToX(c, 148.75)).toBe(100 + 148.75 * 8);
    expect(logicalToX(c, -3.5)).toBe(100 - 3.5 * 8); // before the data
    expect(logicalToX(c, 620.25)).toBe(100 + 620.25 * 8); // far after it
  });
  it("x gives a fractional index, not a whole bar", () => {
    const c = fakeChart();
    expect(xToLogical(c, 100 + 10.37 * 8)).toBeCloseTo(10.37);
    expect(xToLogical(c, 100 - 2.5 * 8)).toBeCloseTo(-2.5);
  });
});
