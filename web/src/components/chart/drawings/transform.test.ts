import { describe, expect, it } from "vitest";
import type { Time } from "lightweight-charts";
import { rectAnchorsOf, reshapeDrawing, translateDrawing } from "./transform";
import {
  DEFAULT_STYLE,
  type HLineDrawing,
  type RectangleDrawing,
  type TrendLineDrawing,
  type VLineDrawing,
} from "./types";

const t = (n: number) => n as unknown as Time;

function trendline(
  p1: { time: number; price: number },
  p2: { time: number; price: number },
): TrendLineDrawing {
  return {
    id: "x",
    kind: "trendline",
    p1: { time: t(p1.time), price: p1.price },
    p2: { time: t(p2.time), price: p2.price },
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
    id: "r",
    kind: "rectangle",
    p1: { time: t(p1.time), price: p1.price },
    p2: { time: t(p2.time), price: p2.price },
    style: DEFAULT_STYLE,
    createdAt: 0,
    updatedAt: 0,
  };
}

function hline(price: number): HLineDrawing {
  return { id: "h", kind: "hline", price, style: DEFAULT_STYLE, createdAt: 0, updatedAt: 0 };
}

function vline(time: number): VLineDrawing {
  return {
    id: "v",
    kind: "vline",
    time: t(time),
    style: DEFAULT_STYLE,
    createdAt: 0,
    updatedAt: 0,
  };
}

describe("translateDrawing (drag the body)", () => {
  it("shifts both time and price for a trend line -- moving the body must move it horizontally, not just vertically", () => {
    const d = trendline({ time: 100, price: 50 }, { time: 200, price: 60 });
    const patch = translateDrawing(d, 5, 30) as Pick<TrendLineDrawing, "p1" | "p2">;
    expect(patch.p1).toEqual({ time: t(130), price: 55 });
    expect(patch.p2).toEqual({ time: t(230), price: 65 });
  });

  it("shifts both points of a rectangle", () => {
    const d = rectangle({ time: 0, price: 0 }, { time: 100, price: 100 });
    const patch = translateDrawing(d, 10, 50) as Pick<RectangleDrawing, "p1" | "p2">;
    expect(patch.p1).toEqual({ time: t(50), price: 10 });
    expect(patch.p2).toEqual({ time: t(150), price: 110 });
  });

  it("moves an hline only in price, never in time (it has no time axis)", () => {
    const patch = translateDrawing(hline(100), 25, 999);
    expect(patch).toEqual({ price: 125 });
  });

  it("moves a vline only in time, never in price (it has no price axis)", () => {
    const patch = translateDrawing(vline(100), 999, 25);
    expect(patch).toEqual({ time: t(125) });
  });

  it("shifts a position's time anchor, its right time-anchor, and every price level together", () => {
    const d: Parameters<typeof translateDrawing>[0] = {
      id: "p",
      kind: "position-long",
      time: t(100),
      rightTime: t(300),
      entry: 100,
      stop: 90,
      targets: [110, 120],
      style: DEFAULT_STYLE,
      createdAt: 0,
      updatedAt: 0,
    };
    const patch = translateDrawing(d, 5, 20);
    expect(patch).toEqual({ time: t(120), rightTime: t(320), entry: 105, stop: 95, targets: [115, 125] });
  });
});

describe("reshapeDrawing (drag a handle)", () => {
  it("moves only the dragged endpoint of a trend line, in both time and price", () => {
    const d = trendline({ time: 0, price: 0 }, { time: 100, price: 100 });
    const patch = reshapeDrawing(d, "p1", { time: t(10), price: 10 });
    expect(patch).toEqual({ p1: { time: t(10), price: 10 } });
  });

  it("resizes a rectangle from its top-left corner without disturbing the opposite corner", () => {
    const d = rectangle({ time: 0, price: 0 }, { time: 100, price: 100 }); // p1 bottom-left, p2 top-right
    const anchors = rectAnchorsOf(d);
    const patch = reshapeDrawing(d, "tl", { time: t(20), price: 80 }, anchors) as Pick<
      RectangleDrawing,
      "p1" | "p2"
    >;
    // "tl" edits the left-time point (p1) and the top-price point (p2),
    // leaving p1's price and p2's time as they were.
    expect(patch.p1).toEqual({ time: t(20), price: 0 });
    expect(patch.p2).toEqual({ time: t(100), price: 80 });
  });

  it("resizing through the opposite edge does not flip which point is being edited mid-drag", () => {
    const d = rectangle({ time: 0, price: 0 }, { time: 100, price: 100 });
    const anchors = rectAnchorsOf(d); // captured once, at drag start
    // Drag "right" (p2's time) past the original left edge (time 0).
    const patch = reshapeDrawing(d, "right", { time: t(-50), price: 999 }, anchors) as Pick<
      RectangleDrawing,
      "p1" | "p2"
    >;
    expect(patch.p2?.time).toBe(t(-50)); // still p2 being edited, not p1
    expect(patch.p1?.time).toBe(t(0)); // p1 (the original left point) untouched
  });

  it("drags a position's right handle to change only its forward width, never its price levels", () => {
    const d: Parameters<typeof translateDrawing>[0] = {
      id: "p",
      kind: "position-long",
      time: t(100),
      rightTime: t(300),
      entry: 100,
      stop: 90,
      targets: [110],
      style: DEFAULT_STYLE,
      createdAt: 0,
      updatedAt: 0,
    };
    const patch = reshapeDrawing(d, "right", { time: t(500), price: 999 });
    expect(patch).toEqual({ rightTime: t(500) });
  });
});
