import { describe, expect, it } from "vitest";
import type { Time } from "lightweight-charts";
import { extendPoint, getHandles, hitTest } from "./hit-test";
import type { ChartAccessors } from "./geometry";
import {
  DEFAULT_STYLE,
  type FibDrawing,
  type HLineDrawing,
  type PositionDrawing,
  type RectangleDrawing,
  type TrendLineDrawing,
} from "./types";

// A fake chart/series pair where time and price map 1:1 onto pixel
// coordinates (x = time, y = price). Pure geometry, so the actual mapping
// does not matter as long as it is consistent -- this keeps test points
// easy to reason about (time 10 is pixel x=10, price 50 is pixel y=50).
function fakeAccessors(): ChartAccessors {
  const chart = {
    timeScale: () => ({
      timeToCoordinate: (t: Time) => t as unknown as number,
      coordinateToTime: (x: number) => x as unknown as Time,
    }),
  };
  const series = {
    priceToCoordinate: (p: number) => p,
    coordinateToPrice: (y: number) => y,
  };
  return { chart, series } as unknown as ChartAccessors;
}

const acc = fakeAccessors();
const PANE_WIDTH = 1000;

function trendline(
  p1: { time: number; price: number },
  p2: { time: number; price: number },
): TrendLineDrawing {
  return {
    id: "t1",
    kind: "trendline",
    p1: { time: p1.time as unknown as Time, price: p1.price },
    p2: { time: p2.time as unknown as Time, price: p2.price },
    style: DEFAULT_STYLE,
    createdAt: 0,
    updatedAt: 0,
  };
}

function rectangle(
  p1: { time: number; price: number },
  p2: { time: number; price: number },
): RectangleDrawing {
  return {
    id: "r1",
    kind: "rectangle",
    p1: { time: p1.time as unknown as Time, price: p1.price },
    p2: { time: p2.time as unknown as Time, price: p2.price },
    style: DEFAULT_STYLE,
    createdAt: 0,
    updatedAt: 0,
  };
}

function hline(price: number): HLineDrawing {
  return { id: "h1", kind: "hline", price, style: DEFAULT_STYLE, createdAt: 0, updatedAt: 0 };
}

function fib(p1: { time: number; price: number }, p2: { time: number; price: number }): FibDrawing {
  return {
    id: "f1",
    kind: "fib",
    p1: { time: p1.time as unknown as Time, price: p1.price },
    p2: { time: p2.time as unknown as Time, price: p2.price },
    style: DEFAULT_STYLE,
    createdAt: 0,
    updatedAt: 0,
  };
}

function position(
  entry: number,
  stop: number,
  targets: number[],
  rightTime = 10000,
): PositionDrawing {
  return {
    id: "p1",
    kind: "position-long",
    time: 10 as unknown as Time,
    rightTime: rightTime as unknown as Time,
    entry,
    stop,
    targets,
    style: DEFAULT_STYLE,
    createdAt: 0,
    updatedAt: 0,
  };
}

describe("getHandles", () => {
  it("returns p1/p2 for a trend line, at the correct pixel positions", () => {
    const d = trendline({ time: 10, price: 100 }, { time: 50, price: 200 });
    const handles = getHandles(d, acc);
    expect(handles).toEqual([
      { id: "p1", px: { x: 10, y: 100 }, cursor: "resize" },
      { id: "p2", px: { x: 50, y: 200 }, cursor: "resize" },
    ]);
  });

  it("returns all 8 corner/edge handles for a rectangle, independent of point order", () => {
    const d = rectangle({ time: 50, price: 100 }, { time: 10, price: 200 }); // p1 is bottom-right-ish
    const handles = getHandles(d, acc);
    const byId = Object.fromEntries(handles.map((h) => [h.id, h.px]));
    expect(byId.tl).toEqual({ x: 10, y: 100 });
    expect(byId.br).toEqual({ x: 50, y: 200 });
    expect(byId.top).toEqual({ x: 30, y: 100 });
    expect(byId.left).toEqual({ x: 10, y: 150 });
  });

  it("returns entry/stop/target handles at the anchor time, plus a right handle at the box's right time-anchor", () => {
    const d = position(100, 90, [110, 120], 50);
    const handles = getHandles(d, acc);
    expect(handles.map((h) => h.id)).toEqual(["entry", "stop", "target-0", "target-1", "right"]);
    expect(handles.slice(0, 4).every((h) => h.px.x === 10)).toBe(true);
    const right = handles.find((h) => h.id === "right");
    expect(right?.px.x).toBe(50);
    expect(right?.cursor).toBe("resize");
  });

  it("returns no handles for hline/vline (they move as a whole via a body hit)", () => {
    expect(getHandles(hline(100), acc)).toEqual([]);
  });
});

describe("hitTest", () => {
  it("hits a trend line's p1 handle within tolerance, not exactly on it", () => {
    const d = trendline({ time: 10, price: 100 }, { time: 50, price: 200 });
    const r = hitTest(d, acc, { x: 13, y: 103 }, PANE_WIDTH);
    expect(r).toEqual({
      kind: "handle",
      handle: { id: "p1", px: { x: 10, y: 100 }, cursor: "resize" },
    });
  });

  it("hits a trend line's body when near the segment but far from either endpoint", () => {
    const d = trendline({ time: 0, price: 0 }, { time: 100, price: 100 });
    const r = hitTest(d, acc, { x: 50, y: 52 }, PANE_WIDTH); // near the midpoint, 2px off the line
    expect(r).toEqual({ kind: "body" });
  });

  it("misses a trend line far from both the body and the handles", () => {
    const d = trendline({ time: 0, price: 0 }, { time: 100, price: 100 });
    const r = hitTest(d, acc, { x: 50, y: 90 }, PANE_WIDTH);
    expect(r).toBeNull();
  });

  it("hits a rectangle's body anywhere inside it, not just the border", () => {
    const d = rectangle({ time: 0, price: 0 }, { time: 100, price: 100 });
    const r = hitTest(d, acc, { x: 50, y: 50 }, PANE_WIDTH);
    expect(r).toEqual({ kind: "body" });
  });

  it("hits a rectangle's corner handle over its body when both are in range", () => {
    const d = rectangle({ time: 0, price: 0 }, { time: 100, price: 100 });
    const r = hitTest(d, acc, { x: 2, y: 2 }, PANE_WIDTH);
    expect(r?.kind).toBe("handle");
  });

  it("hits an hline anywhere along its length, within a small vertical tolerance", () => {
    const d = hline(50);
    expect(hitTest(d, acc, { x: 900, y: 52 }, PANE_WIDTH)).toEqual({ kind: "body" });
    expect(hitTest(d, acc, { x: 900, y: 70 }, PANE_WIDTH)).toBeNull();
  });

  it("hits a position's entry line only at or beyond the anchor time, as a handle right at the anchor and a body hit further along", () => {
    const d = position(100, 90, [110]);
    expect(hitTest(d, acc, { x: 0, y: 100 }, PANE_WIDTH)).toBeNull(); // before the anchor, outside handle tolerance too
    expect(hitTest(d, acc, { x: 10, y: 100 }, PANE_WIDTH)).toEqual({
      kind: "handle",
      handle: { id: "entry", px: { x: 10, y: 100 }, cursor: "move" },
    });
    expect(hitTest(d, acc, { x: 500, y: 100 }, PANE_WIDTH)).toEqual({ kind: "body" });
  });

  it("hits a position's stop line away from its handle as a body hit", () => {
    const d = position(100, 90, [110]);
    const r = hitTest(d, acc, { x: 500, y: 90 }, PANE_WIDTH);
    expect(r).toEqual({ kind: "body" });
  });

  it("hits anywhere inside a position's risk box, not just near the entry or stop line", () => {
    const d = position(100, 90, [110]);
    // y=95 is the midpoint between entry (100) and stop (90) -- not within
    // tolerance of either line on its own, only inside the box between them.
    const r = hitTest(d, acc, { x: 500, y: 95 }, PANE_WIDTH);
    expect(r).toEqual({ kind: "body" });
  });

  it("hits anywhere inside a position's reward box, between entry and the farthest target", () => {
    const d = position(100, 90, [110, 140]);
    // y=125, well clear of entry (100), target-0 (110), and target-1 (140).
    const r = hitTest(d, acc, { x: 500, y: 125 }, PANE_WIDTH);
    expect(r).toEqual({ kind: "body" });
  });

  it("misses a position above the reward box and below the risk box", () => {
    const d = position(100, 90, [110]);
    expect(hitTest(d, acc, { x: 500, y: 300 }, PANE_WIDTH)).toBeNull();
  });

  it("is a bounded box: misses past the right time-anchor even at a y inside the box", () => {
    const d = position(100, 90, [110], 200); // box spans x=[10,200]
    expect(hitTest(d, acc, { x: 95, y: 95 }, PANE_WIDTH)).toEqual({ kind: "body" }); // inside
    expect(hitTest(d, acc, { x: 400, y: 95 }, PANE_WIDTH)).toBeNull(); // same y, past the right edge
  });

  it("hits the right handle at the box's own right time-anchor", () => {
    const d = position(100, 90, [110], 200);
    // midpoint of [min(entry,stop,target), max(...)] = midpoint of [90,110] = 100
    const r = hitTest(d, acc, { x: 200, y: 100 }, PANE_WIDTH);
    expect(r?.kind).toBe("handle");
    if (r?.kind === "handle") expect(r.handle.id).toBe("right");
  });

  it("hits a fib's body strictly between its own two anchors, not out to the pane edge", () => {
    const d = fib({ time: 0, price: 0 }, { time: 100, price: 100 });
    expect(hitTest(d, acc, { x: 50, y: 50 }, PANE_WIDTH)).toEqual({ kind: "body" }); // between the anchors
    expect(hitTest(d, acc, { x: 500, y: 50 }, PANE_WIDTH)).toBeNull(); // well past the right anchor
  });
});

describe("extendPoint", () => {
  it("projects a line forward to the pane's right edge", () => {
    const far = extendPoint({ x: 0, y: 0 }, { x: 10, y: 10 }, 100);
    expect(far.x).toBe(100);
    expect(far.y).toBe(100);
  });

  it("handles a vertical line (no horizontal component) without dividing by zero", () => {
    const far = extendPoint({ x: 20, y: 0 }, { x: 20, y: 10 }, 100);
    expect(Number.isFinite(far.y)).toBe(true);
  });
});
