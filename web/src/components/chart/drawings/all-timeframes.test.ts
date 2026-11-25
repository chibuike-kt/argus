// Owner decision D17: a drawing belongs to the instrument and shows on every
// timeframe. Anchors are time and price, so a drawing made on one timeframe
// must be placed, hit and resized correctly on any other, including a time
// before the first loaded bar or after the last (a drawing made on 1d three
// months ago, viewed on 1m whose history is about 10 days).
import { describe, expect, it } from "vitest";
import type { Time } from "lightweight-charts";
import { timeToX, type ChartAccessors } from "./geometry";
import { getHandles, hitTest, minSpanRect } from "./hit-test";
import { logicalToTime, setSeriesTimeAxis, timeToLogical } from "./time-axis";
import { DEFAULT_STYLE, type PositionDrawing, type RectangleDrawing, type VLineDrawing } from "./types";

const MIN = 60;
const DAY = 86_400;
// 10 days of 1m bars, as the chart holds at 1m.
const FIRST = Date.UTC(2026, 10, 15) / 1000;
const BARS = 10 * 1440;
const LAST = FIRST + (BARS - 1) * MIN;
const BAR_PX = 6;
const ORIGIN_PX = 100;

// A chart like the real one: the library's timeToCoordinate is null for any
// time without a bar (here: every time, as on a timeframe the drawing was
// not made on), while the logical scale is linear and extends past the data
// on both sides.
function realisticAccessors(times: number[], intervalSec: number): ChartAccessors {
  const series = { priceToCoordinate: (p: number) => 1000 - p, coordinateToPrice: (y: number) => 1000 - y };
  const chart = {
    timeScale: () => ({
      timeToCoordinate: () => null,
      logicalToCoordinate: (i: number) => ORIGIN_PX + i * BAR_PX,
    }),
  };
  setSeriesTimeAxis(series, { times, intervalSec });
  return { chart, series } as unknown as ChartAccessors;
}

const oneMinuteTimes = Array.from({ length: BARS }, (_, i) => FIRST + i * MIN);
const acc1m = realisticAccessors(oneMinuteTimes, MIN);
const pane = 1600;

describe("anchors outside the loaded range, on both sides", () => {
  it("a time three months before the first bar is placed at its own time, never held at the edge", () => {
    const t = FIRST - 90 * DAY;
    const logical = timeToLogical({ times: oneMinuteTimes, intervalSec: MIN }, t)!;
    expect(logical).toBe(-90 * 1440);
    expect(logicalToTime({ times: oneMinuteTimes, intervalSec: MIN }, logical)).toBe(t);
    expect(timeToX(acc1m, t as Time)).toBe(ORIGIN_PX + -90 * 1440 * BAR_PX);
  });
  it("a time three months after the last bar likewise", () => {
    const t = LAST + 90 * DAY;
    const logical = timeToLogical({ times: oneMinuteTimes, intervalSec: MIN }, t)!;
    expect(logical).toBe(BARS - 1 + 90 * 1440);
    expect(timeToX(acc1m, t as Time)).toBe(ORIGIN_PX + (BARS - 1 + 90 * 1440) * BAR_PX);
  });
  it("a time between two bars of the current timeframe lands between them", () => {
    const fourHour = Array.from({ length: 60 }, (_, i) => FIRST + i * 4 * 3600);
    const acc4h = realisticAccessors(fourHour, 4 * 3600);
    // 13:37 on the first day: 3h37m into the 12:00 bar (index 3).
    const t = FIRST + 13 * 3600 + 37 * MIN;
    expect(timeToX(acc4h, t as Time)).toBeCloseTo(ORIGIN_PX + (3 + 97 / 240) * BAR_PX, 6);
  });
});

function position(time: number, rightTime: number): PositionDrawing {
  return {
    id: "p", kind: "position-long", time: time as Time, rightTime: rightTime as Time,
    entry: 500, stop: 480, targets: [540], style: DEFAULT_STYLE, createdAt: 0, updatedAt: 0,
  };
}

describe("hit testing and handles on a timeframe the drawing was not made on", () => {
  it("a position whose times have no bar here still has handles and is hit inside its box", () => {
    // Drawn on 1d (00:00 to 10 days later), seen on 1m: neither time is a bar here.
    const left = FIRST + 2 * DAY + 30;
    const d = position(left, left + 10 * DAY);
    const handles = getHandles(d, acc1m);
    expect(handles.map((h) => h.id)).toEqual(expect.arrayContaining(["entry", "stop", "left", "right"]));
    const xLeft = timeToX(acc1m, left as Time)!;
    expect(hitTest(d, acc1m, { x: xLeft + 50, y: 1000 - 490 }, pane)).toEqual({ kind: "body" });
  });
  it("a tiny position (drawn on 1m, seen on 1d) is still hit at its anchor", () => {
    const daily = Array.from({ length: 400 }, (_, i) => FIRST - 300 * DAY + i * DAY);
    const acc1d = realisticAccessors(daily, DAY);
    const t = FIRST + 5 * 3600 + 7 * MIN;
    const d = position(t, t + 10 * MIN);
    const x = timeToX(acc1d, t as Time)!;
    expect(hitTest(d, acc1d, { x: x + 1, y: 1000 - 500 }, pane)).not.toBeNull();
  });
  it("a vertical line between two bars is hit", () => {
    const fourHour = Array.from({ length: 60 }, (_, i) => FIRST + i * 4 * 3600);
    const acc4h = realisticAccessors(fourHour, 4 * 3600);
    const t = FIRST + 13 * 3600 + 37 * MIN;
    const d: VLineDrawing = { id: "v", kind: "vline", time: t as Time, style: DEFAULT_STYLE, createdAt: 0, updatedAt: 0 };
    const x = timeToX(acc4h, t as Time)!;
    expect(hitTest(d, acc4h, { x: x + 3, y: 300 }, pane)).toEqual({ kind: "body" });
  });
  it("a rectangle a fraction of a pixel wide is still selectable", () => {
    const daily = Array.from({ length: 400 }, (_, i) => FIRST - 300 * DAY + i * DAY);
    const acc1d = realisticAccessors(daily, DAY);
    // Drawn on 1m over 5 minutes: 5/1440 of a daily bar, well under a pixel.
    const t = FIRST + 9 * 3600;
    const d: RectangleDrawing = {
      id: "r", kind: "rectangle", p1: { time: t as Time, price: 600 }, p2: { time: (t + 5 * MIN) as Time, price: 400 },
      style: DEFAULT_STYLE, createdAt: 0, updatedAt: 0,
    };
    const x = timeToX(acc1d, t as Time)!;
    // Away from every handle (corners at y 400 and 600, middles at y 500),
    // 3 px beside a sliver a fraction of a pixel wide: still its body.
    expect(hitTest(d, acc1d, { x: x + 3, y: 430 }, pane)).toEqual({ kind: "body" });
  });
  it("a huge rectangle (drawn on 1d, seen on 1m) is hit anywhere inside the view", () => {
    const d: RectangleDrawing = {
      id: "r", kind: "rectangle",
      p1: { time: (FIRST - 60 * DAY) as Time, price: 600 }, p2: { time: (LAST + 60 * DAY) as Time, price: 400 },
      style: DEFAULT_STYLE, createdAt: 0, updatedAt: 0,
    };
    expect(hitTest(d, acc1m, { x: 800, y: 1000 - 500 }, pane)).toEqual({ kind: "body" });
  });
  it("minSpanRect only grows a span below the minimum, about its centre", () => {
    const [a, b] = minSpanRect({ x: 10, y: 10 }, { x: 10.2, y: 50 }, 16);
    expect(a.x).toBeCloseTo(2.1, 9);
    expect(b.x).toBeCloseTo(18.1, 9);
    expect([a.y, b.y]).toEqual([10, 50]);
    expect(minSpanRect({ x: 10, y: 10 }, { x: 40, y: 50 }, 16)).toEqual([{ x: 10, y: 10 }, { x: 40, y: 50 }]);
  });
});
