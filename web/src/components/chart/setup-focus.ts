// setup-focus.ts: framing a selected setup on the chart (polish pass, item
// 4). Pure: the selected setup's drawn elements and the chart's time axis
// in, a logical range out, and a plain sentence when not all of it fits.
//
// The frame runs from the setup's earliest evidence time to its end (its
// invalidation, or the newest candle while it is active), with a margin on
// both sides and room on the right for the plan's price pills. Labels only
// show when bars are at least FOCUS_MIN_BAR_SPACING_PX wide (the layer's
// level-of-detail rule), so when the whole setup cannot be framed that
// wide, the evidence up to the decision is framed instead; when even that
// is too long, the part around the decision is.
import type { LayerElement } from "./setup-layers";
import { timeToLogical, type TimeAxis } from "./drawings/time-axis";
import { LABEL_MIN_BAR_SPACING_PX } from "./setup-layers-primitive";

// [U] display values.
export const FOCUS_MIN_BAR_SPACING_PX = LABEL_MIN_BAR_SPACING_PX + 1;
export const FOCUS_MARGIN_FRACTION = 0.12;
export const FOCUS_MIN_MARGIN_BARS = 5;
// Room right of the plan for its pills ("TP1 60145.0 · 0.06R").
export const FOCUS_RIGHT_GUTTER_PX = 150;

export interface FocusSpan {
  start: number; // earliest evidence time, unix seconds
  end: number; // invalidation, or the newest candle while active
  decision: number; // when the setup was found (the plan's start)
}

// focusSpan reads the span from the selected setup's elements.
export function focusSpan(layers: readonly LayerElement[], setupId: number): FocusSpan | null {
  const own = layers.filter((l) => l.setupId === setupId && l.selected);
  if (own.length === 0) return null;
  let start = Infinity;
  let end = -Infinity;
  let decision: number | null = null;
  for (const l of own) {
    const g = l.geometry;
    const ts = g.type === "marker" ? [g.t] : [g.t1, g.t2];
    for (const t of ts) {
      start = Math.min(start, t);
      end = Math.max(end, t);
    }
    if (g.type === "plan") decision = g.t1;
  }
  return { start, end, decision: decision ?? end };
}

export type FocusMode = "whole" | "evidence" | "around-decision";

export interface FocusPlan {
  from: number;
  to: number;
  mode: FocusMode;
  spanSec: number;
  evidenceSec: number;
}

export function focusRange(
  span: FocusSpan,
  axis: TimeAxis,
  paneWidthPx: number,
  minSpacing = FOCUS_MIN_BAR_SPACING_PX,
): FocusPlan | null {
  const L = (t: number) => timeToLogical(axis, t);
  const la = L(span.start);
  const lend = L(span.end);
  const ldec = L(span.decision);
  if (la === null || lend === null || ldec === null || paneWidthPx <= FOCUS_RIGHT_GUTTER_PX) return null;
  const usable = paneWidthPx - FOCUS_RIGHT_GUTTER_PX;
  const frame = (a: number, b: number) => {
    const bars = Math.max(b - a, 1);
    const margin = Math.max(FOCUS_MIN_MARGIN_BARS, bars * FOCUS_MARGIN_FRACTION);
    const width = bars + 2 * margin;
    const spacing = usable / width;
    return { from: a - margin, to: b + margin + FOCUS_RIGHT_GUTTER_PX / spacing, spacing };
  };
  const base = { spanSec: span.end - span.start, evidenceSec: span.decision - span.start };
  const whole = frame(la, lend);
  if (whole.spacing >= minSpacing) return { from: whole.from, to: whole.to, mode: "whole", ...base };
  // The evidence up to the decision, plus a few bars after it.
  const ev = frame(la, ldec + FOCUS_MIN_MARGIN_BARS);
  if (ev.spacing >= minSpacing) return { from: ev.from, to: ev.to, mode: "evidence", ...base };
  // Around the decision: as many bars as fit at minSpacing, the decision
  // about two thirds of the way across (the sweep and break come before it).
  const bars = usable / minSpacing;
  const from = ldec - bars * 0.66;
  return { from, to: from + bars + FOCUS_RIGHT_GUTTER_PX / minSpacing, mode: "around-decision", ...base };
}

function hours(sec: number): string {
  const h = sec / 3600;
  if (h < 1) return `${Math.max(1, Math.round(sec / 60))} minutes`;
  return `${h < 10 ? h.toFixed(1) : Math.round(h)} hours`;
}

// focusNotice is the panel's sentence for a frame, or null when the whole
// setup fits with its labels.
export function focusNotice(plan: FocusPlan, timeframe: string): string | null {
  switch (plan.mode) {
    case "whole":
      return null;
    case "evidence":
      return `The setup runs ${hours(plan.spanSec)}, too long to label at ${timeframe} in one view: its evidence (${hours(plan.evidenceSec)}) is framed. Zoom out for the rest.`;
    case "around-decision":
      return `Evidence spans ${hours(plan.evidenceSec)}; its labels cannot all fit at ${timeframe}. The part around the decision is framed; zoom in on the rest to read its labels.`;
  }
}
