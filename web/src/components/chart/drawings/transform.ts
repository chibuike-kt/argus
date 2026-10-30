import type { Time } from "lightweight-charts";
import { timeSeconds } from "./geometry";
import type { DPoint, Drawing } from "./types";

// Pure geometry transforms for move/reshape. Kept separate from the
// controller so they are trivially unit-testable without a chart instance.

function shift(p: DPoint, deltaPrice: number, deltaTimeSec: number): DPoint {
  return { time: (timeSeconds(p.time) + deltaTimeSec) as Time, price: p.price + deltaPrice };
}

// Moves the whole drawing by a price/time delta, preserving its shape --
// the "drag the body" behaviour. hline only has a price axis to move on;
// vline only a time axis; everything else moves on both.
export function translateDrawing(
  d: Drawing,
  deltaPrice: number,
  deltaTimeSec: number,
): Partial<Drawing> {
  switch (d.kind) {
    case "trendline":
    case "ray":
    case "extended":
    case "fib":
    case "measure":
    case "rectangle":
      return {
        p1: shift(d.p1, deltaPrice, deltaTimeSec),
        p2: shift(d.p2, deltaPrice, deltaTimeSec),
      };
    case "hline":
      return { price: d.price + deltaPrice };
    case "vline":
      return { time: (timeSeconds(d.time) + deltaTimeSec) as Time };
    case "text":
      return { p: shift(d.p, deltaPrice, deltaTimeSec) };
    case "position-long":
    case "position-short":
      return {
        time: (timeSeconds(d.time) + deltaTimeSec) as Time,
        // rightTime shifts by the same delta so the box's own width stays
        // constant when moving the whole drawing -- only the "right" handle
        // changes the width itself.
        rightTime: (timeSeconds(d.rightTime) + deltaTimeSec) as Time,
        entry: d.entry + deltaPrice,
        stop: d.stop + deltaPrice,
        targets: d.targets.map((t) => t + deltaPrice),
      };
  }
}

// Rectangle corner/edge resize needs to know, at drag start, which original
// point (p1 or p2) is "the left one" and which is "the top one" -- fixed for
// the whole drag so a resize through the opposite edge does not flip which
// point is being edited mid-gesture.
export interface RectAnchors {
  leftIsP1: boolean;
  topIsP1: boolean;
}

export function rectAnchorsOf(d: { p1: DPoint; p2: DPoint }): RectAnchors {
  return {
    leftIsP1: timeSeconds(d.p1.time) <= timeSeconds(d.p2.time),
    topIsP1: d.p1.price >= d.p2.price,
  };
}

function resizeRectangle(
  d: { p1: DPoint; p2: DPoint },
  handleId: string,
  newPoint: DPoint,
  anchors: RectAnchors,
): Partial<{ p1: DPoint; p2: DPoint }> {
  const p1 = { ...d.p1 };
  const p2 = { ...d.p2 };
  const setTime = (isP1: boolean, time: Time) => {
    if (isP1) p1.time = time;
    else p2.time = time;
  };
  const setPrice = (isP1: boolean, price: number) => {
    if (isP1) p1.price = price;
    else p2.price = price;
  };
  const { leftIsP1, topIsP1 } = anchors;
  switch (handleId) {
    case "left":
      setTime(leftIsP1, newPoint.time);
      break;
    case "right":
      setTime(!leftIsP1, newPoint.time);
      break;
    case "top":
      setPrice(topIsP1, newPoint.price);
      break;
    case "bottom":
      setPrice(!topIsP1, newPoint.price);
      break;
    case "tl":
      setTime(leftIsP1, newPoint.time);
      setPrice(topIsP1, newPoint.price);
      break;
    case "tr":
      setTime(!leftIsP1, newPoint.time);
      setPrice(topIsP1, newPoint.price);
      break;
    case "bl":
      setTime(leftIsP1, newPoint.time);
      setPrice(!topIsP1, newPoint.price);
      break;
    case "br":
      setTime(!leftIsP1, newPoint.time);
      setPrice(!topIsP1, newPoint.price);
      break;
  }
  return { p1, p2 };
}

// Adjusts a single handle -- the "drag a handle to reshape/resize" behaviour.
// anchors is only consulted for rectangle corner/edge handles; pass the
// RectAnchors captured at drag start (see rectAnchorsOf).
export function reshapeDrawing(
  d: Drawing,
  handleId: string,
  newPoint: DPoint,
  anchors?: RectAnchors,
): Partial<Drawing> {
  switch (d.kind) {
    case "trendline":
    case "ray":
    case "extended":
    case "fib":
    case "measure":
      return handleId === "p1" ? { p1: newPoint } : { p2: newPoint };
    case "rectangle":
      return anchors ? resizeRectangle(d, handleId, newPoint, anchors) : {};
    case "text":
      return { p: newPoint };
    case "position-long":
    case "position-short": {
      if (handleId === "entry") return { entry: newPoint.price };
      if (handleId === "stop") return { stop: newPoint.price };
      if (handleId === "right") return { rightTime: newPoint.time };
      if (handleId.startsWith("target-")) {
        const i = Number(handleId.slice("target-".length));
        const targets = [...d.targets];
        targets[i] = newPoint.price;
        return { targets };
      }
      return {};
    }
    case "hline":
    case "vline":
      return {};
  }
}
