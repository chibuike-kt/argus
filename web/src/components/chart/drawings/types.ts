import type { Time } from "lightweight-charts";

// Data model for a drawing. Every anchor is time + price (chart space), never
// a pixel coordinate, so a drawing stays pinned to the data under zoom, pan,
// and as new candles arrive (the same point-in-time discipline the backend
// applies to candles and swings).
export interface DPoint {
  time: Time;
  price: number;
}

export type LineStyle = "solid" | "dashed";

export interface DrawingStyle {
  color: string;
  lineWidth: number;
  lineStyle: LineStyle;
}

export const DEFAULT_STYLE: DrawingStyle = {
  color: "#6d7bff",
  lineWidth: 1.5,
  lineStyle: "solid",
};

interface Base {
  id: string;
  style: DrawingStyle;
  createdAt: number;
  updatedAt: number;
}

export interface TrendLineDrawing extends Base {
  kind: "trendline" | "ray" | "extended";
  p1: DPoint;
  p2: DPoint;
}

export interface HLineDrawing extends Base {
  kind: "hline";
  price: number;
}

export interface VLineDrawing extends Base {
  kind: "vline";
  time: Time;
}

export interface RectangleDrawing extends Base {
  kind: "rectangle";
  p1: DPoint;
  p2: DPoint;
  label?: string;
}

// Three levels only: the two anchors (0, 1) and the equilibrium (0.5) --
// the premium/discount split, the one level that needs to read as a
// distinct line, not the full retracement ladder (0.236/0.382/0.618/
// 0.705/0.786). TradingView's own fib shows the full ladder by default;
// ARGUS's reason for a fib is specifically the equilibrium split, so this
// is a deliberate narrowing, not a parity gap.
export const FIB_LEVELS = [0, 0.5, 1] as const;

export interface FibDrawing extends Base {
  kind: "fib";
  p1: DPoint;
  p2: DPoint;
}

export interface PositionDrawing extends Base {
  kind: "position-long" | "position-short";
  time: Time;
  // rightTime bounds the box/lines to a finite forward width, instead of
  // flooding to the pane's right edge -- a second, draggable time anchor,
  // defaulting to a fixed number of bars ahead of time at creation.
  rightTime: Time;
  entry: number;
  stop: number;
  targets: number[];
}

export interface TextDrawing extends Base {
  kind: "text";
  p: DPoint;
  text: string;
}

export interface MeasureDrawing extends Base {
  kind: "measure";
  p1: DPoint;
  p2: DPoint;
}

export type Drawing =
  | TrendLineDrawing
  | HLineDrawing
  | VLineDrawing
  | RectangleDrawing
  | FibDrawing
  | PositionDrawing
  | TextDrawing
  | MeasureDrawing;

export type DrawKind = Drawing["kind"];

// Two-point tools: first click sets p1, the mouse previews, second click (or
// release, for click-drag-release) commits p2. One-click tools place and
// commit immediately. Kinds not listed here are not yet drawing tools.
// Positions are two-point too -- entry at p1, stop at p2 -- so the risk
// (and therefore the reward target, a multiple of it) is sized by the
// trader while drawing, not fixed to a default distance; the box's
// forward width (rightTime) and all three levels still stay adjustable
// afterward via their own handles, same as always.
export const TWO_POINT_KINDS: DrawKind[] = [
  "trendline",
  "ray",
  "extended",
  "rectangle",
  "fib",
  "measure",
  "position-long",
  "position-short",
];

// Which rail tool id maps to which drawing kind. Tools not listed here select
// but do not yet draw (patterns, elliott, gann, ... -- out of V1 per the brief).
export const TOOL_TO_KIND: Partial<Record<string, DrawKind>> = {
  trend: "trendline",
  ray: "ray",
  extended: "extended",
  hline: "hline",
  vline: "vline",
  rect: "rectangle",
  "fib-retr": "fib",
  long: "position-long",
  short: "position-short",
  text: "text",
  measure: "measure",
};

let seq = 0;
export const nextId = () => `d${Date.now().toString(36)}${(++seq).toString(36)}`;

export function cloneDrawing(d: Drawing): Drawing {
  const now = Date.now();
  return { ...structuredCloneLike(d), id: nextId(), createdAt: now, updatedAt: now };
}

// structuredClone is available in every modern browser target this project
// ships to, but typing it loosely here avoids pulling a DOM lib dependency
// into this pure module's type surface.
function structuredCloneLike<T>(v: T): T {
  return typeof structuredClone === "function" ? structuredClone(v) : JSON.parse(JSON.stringify(v));
}

// Repairs a drawing loaded from persistence that predates a field this
// module later added as required -- rightTime, added after some users
// already had position drawings saved without it. Without this, a legacy
// position's rightTime is undefined: resolving it to a coordinate returns
// null, and the primitive skips drawing that position entirely (invisible),
// rather than guessing at the pane's edge. The fixed 6000s (20 bars at a
// 5-minute interval, the fixture data's own cadence) is a one-time repair
// value, not a live default -- new positions get a real one from the
// controller, which knows the chart's actual bar interval.
const LEGACY_POSITION_FORWARD_SECONDS = 6000;

export function migrateDrawing(d: Drawing): Drawing {
  if ((d.kind === "position-long" || d.kind === "position-short") && d.rightTime == null) {
    const timeSeconds =
      typeof d.time === "number"
        ? d.time
        : Math.floor(Date.parse(d.time as unknown as string) / 1000);
    return { ...d, rightTime: (timeSeconds + LEGACY_POSITION_FORWARD_SECONDS) as Time };
  }
  return d;
}

// Every point a drawing carries, for generic move/hit-test/handle code that
// does not need to know the specific kind.
export function pointsOf(d: Drawing): DPoint[] {
  switch (d.kind) {
    case "trendline":
    case "ray":
    case "extended":
    case "rectangle":
    case "fib":
    case "measure":
      return [d.p1, d.p2];
    case "hline":
      return [];
    case "vline":
      return [];
    case "text":
      return [d.p];
    case "position-long":
    case "position-short":
      return [];
  }
}
