import type { Coordinate } from "lightweight-charts";
import type { Drawing } from "./types";
import {
  type ChartAccessors,
  type Px,
  distance,
  distanceToSegment,
  insideRect,
  toPixel,
} from "./geometry";

export interface Handle {
  id: string;
  px: Px;
  // "move" handles (rectangle edges, line bodies) show a move cursor;
  // "resize" handles (corners, endpoints) show a resize cursor.
  cursor: "move" | "resize";
}

export type HitResult = { kind: "handle"; handle: Handle } | { kind: "body" } | null;

const HANDLE_TOLERANCE = 8;
const BODY_TOLERANCE = 8;

function between(v: number, a: number, b: number, tolerance: number): boolean {
  return v >= Math.min(a, b) - tolerance && v <= Math.max(a, b) + tolerance;
}

function rectHandles(a: Px, b: Px): Handle[] {
  const left = Math.min(a.x, b.x);
  const right = Math.max(a.x, b.x);
  const top = Math.min(a.y, b.y);
  const bottom = Math.max(a.y, b.y);
  const midX = (left + right) / 2;
  const midY = (top + bottom) / 2;
  return [
    { id: "tl", px: { x: left, y: top }, cursor: "resize" },
    { id: "tr", px: { x: right, y: top }, cursor: "resize" },
    { id: "bl", px: { x: left, y: bottom }, cursor: "resize" },
    { id: "br", px: { x: right, y: bottom }, cursor: "resize" },
    { id: "top", px: { x: midX, y: top }, cursor: "resize" },
    { id: "bottom", px: { x: midX, y: bottom }, cursor: "resize" },
    { id: "left", px: { x: left, y: midY }, cursor: "resize" },
    { id: "right", px: { x: right, y: midY }, cursor: "resize" },
  ];
}

// Pixel-space handle positions for the selected drawing. Returns [] for
// kinds with no draggable anchors (hline/vline move as a whole via body hit).
export function getHandles(d: Drawing, acc: ChartAccessors): Handle[] {
  switch (d.kind) {
    case "trendline":
    case "ray":
    case "extended":
    case "measure": {
      const p1 = toPixel(acc, d.p1);
      const p2 = toPixel(acc, d.p2);
      if (!p1 || !p2) return [];
      return [
        { id: "p1", px: p1, cursor: "resize" },
        { id: "p2", px: p2, cursor: "resize" },
      ];
    }
    case "fib": {
      const p1 = toPixel(acc, d.p1);
      const p2 = toPixel(acc, d.p2);
      if (!p1 || !p2) return [];
      return [
        { id: "p1", px: p1, cursor: "resize" },
        { id: "p2", px: p2, cursor: "resize" },
      ];
    }
    case "rectangle": {
      const p1 = toPixel(acc, d.p1);
      const p2 = toPixel(acc, d.p2);
      if (!p1 || !p2) return [];
      return rectHandles(p1, p2);
    }
    case "text": {
      const p = toPixel(acc, d.p);
      return p ? [{ id: "p", px: p, cursor: "resize" }] : [];
    }
    case "hline":
    case "vline":
      return [];
    case "position-long":
    case "position-short": {
      const x = acc.chart.timeScale().timeToCoordinate(d.time);
      const yEntry = acc.series.priceToCoordinate(d.entry);
      const yStop = acc.series.priceToCoordinate(d.stop);
      if (x == null) return [];
      const handles: Handle[] = [];
      if (yEntry != null) handles.push({ id: "entry", px: { x, y: yEntry }, cursor: "move" });
      if (yStop != null) handles.push({ id: "stop", px: { x, y: yStop }, cursor: "resize" });
      const targetYs: number[] = [];
      d.targets.forEach((t, i) => {
        const y = acc.series.priceToCoordinate(t);
        if (y != null) {
          targetYs.push(y);
          handles.push({ id: `target-${i}`, px: { x, y }, cursor: "resize" });
        }
      });
      // The box's left and right edges, both draggable like a rectangle's
      // own edge handles -- the width resizes from either side, not just
      // the right, and not only while first drawing it. Both sit at the
      // box's vertical midpoint so neither overlaps the entry/stop/target
      // handles at x itself.
      const ys = [yEntry, yStop, ...targetYs].filter((v): v is Coordinate => v != null);
      const midY = ys.length ? (Math.min(...ys) + Math.max(...ys)) / 2 : (yEntry ?? 0);
      handles.push({ id: "left", px: { x, y: midY }, cursor: "resize" });
      const xRight = acc.chart.timeScale().timeToCoordinate(d.rightTime);
      if (xRight != null) {
        handles.push({ id: "right", px: { x: xRight, y: midY }, cursor: "resize" });
      }
      return handles;
    }
  }
}

// Hit-tests a single drawing against a pointer position, in this order:
// handles first (small, specific), then the body (line/edge proximity or
// rectangle fill), with a few-pixel tolerance throughout -- never pixel-exact,
// per the UX contract.
export function hitTest(d: Drawing, acc: ChartAccessors, px: Px, paneWidth: number): HitResult {
  const handles = getHandles(d, acc);
  for (const h of handles) {
    if (distance(px, h.px) <= HANDLE_TOLERANCE) return { kind: "handle", handle: h };
  }

  switch (d.kind) {
    case "trendline":
    case "measure": {
      const p1 = toPixel(acc, d.p1);
      const p2 = toPixel(acc, d.p2);
      if (!p1 || !p2) return null;
      return distanceToSegment(px, p1, p2) <= BODY_TOLERANCE ? { kind: "body" } : null;
    }
    case "ray":
    case "extended": {
      const p1 = toPixel(acc, d.p1);
      const p2 = toPixel(acc, d.p2);
      if (!p1 || !p2) return null;
      const far = extendPoint(p1, p2, paneWidth);
      return distanceToSegment(px, p1, far) <= BODY_TOLERANCE ? { kind: "body" } : null;
    }
    case "rectangle": {
      const p1 = toPixel(acc, d.p1);
      const p2 = toPixel(acc, d.p2);
      if (!p1 || !p2) return null;
      return insideRect(px, p1, p2) ? { kind: "body" } : null;
    }
    case "fib": {
      const p1 = toPixel(acc, d.p1);
      const p2 = toPixel(acc, d.p2);
      if (!p1 || !p2) return null;
      const left = Math.min(p1.x, p2.x);
      const right = Math.max(p1.x, p2.x);
      if (px.x < left - BODY_TOLERANCE || px.x > right + BODY_TOLERANCE) return null;
      const top = Math.min(p1.y, p2.y) - BODY_TOLERANCE;
      const bottom = Math.max(p1.y, p2.y) + BODY_TOLERANCE;
      return px.y >= top && px.y <= bottom ? { kind: "body" } : null;
    }
    case "hline": {
      const y = acc.series.priceToCoordinate(d.price);
      if (y == null) return null;
      return Math.abs(px.y - y) <= BODY_TOLERANCE ? { kind: "body" } : null;
    }
    case "vline": {
      const x = acc.chart.timeScale().timeToCoordinate(d.time);
      if (x == null) return null;
      return Math.abs(px.x - x) <= BODY_TOLERANCE ? { kind: "body" } : null;
    }
    case "text": {
      const p = toPixel(acc, d.p);
      if (!p) return null;
      return px.x >= p.x - 4 && px.x <= p.x + 120 && px.y >= p.y - 12 && px.y <= p.y + 12
        ? { kind: "body" }
        : null;
    }
    case "position-long":
    case "position-short": {
      const x = acc.chart.timeScale().timeToCoordinate(d.time);
      const yEntry = acc.series.priceToCoordinate(d.entry);
      const yStop = acc.series.priceToCoordinate(d.stop);
      if (x == null || yEntry == null) return null;
      if (px.x < x - BODY_TOLERANCE) return null; // never behind the anchor: the plan projects forward only
      const xRight = acc.chart.timeScale().timeToCoordinate(d.rightTime);
      if (xRight != null && px.x > xRight + BODY_TOLERANCE) return null; // bounded box: never past the right edge either

      // Anywhere inside the risk box (entry-to-stop) or the reward box
      // (entry-to-farthest-target) is a hit, not just within a sliver of
      // one of the lines themselves -- "a position anywhere in its box."
      if (yStop != null && between(px.y, yEntry, yStop, BODY_TOLERANCE)) return { kind: "body" };
      const targetYs = d.targets
        .map((v) => acc.series.priceToCoordinate(v))
        .filter((v): v is Coordinate => v != null);
      if (targetYs.length && between(px.y, yEntry, targetYs[targetYs.length - 1], BODY_TOLERANCE)) {
        return { kind: "body" };
      }
      return null;
    }
  }
}

// Projects p1->p2 to the pane's right edge (ray) or across the full pane
// (extended line would also reach left, but V1 ships the ray's forward
// extension only -- an extended line reuses the same forward projection,
// which is the visually distinguishing behaviour this tool needs for now).
export function extendPoint(p1: Px, p2: Px, paneWidth: number): Px {
  const dx = p2.x - p1.x;
  const dy = p2.y - p1.y;
  if (dx === 0) return { x: p2.x, y: dy >= 0 ? 1e6 : -1e6 };
  const t = (paneWidth - p1.x) / dx;
  return { x: paneWidth, y: p1.y + dy * t };
}
