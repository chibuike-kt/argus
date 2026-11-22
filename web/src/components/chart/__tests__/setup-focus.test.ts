import { describe, expect, it } from "vitest";
import { focusNotice, focusRange, focusSpan, FOCUS_MIN_BAR_SPACING_PX, FOCUS_RIGHT_GUTTER_PX } from "../setup-focus";
import type { LayerElement } from "../setup-layers";
import { timeToLogical } from "../drawings/time-axis";

const T0 = Date.UTC(2026, 10, 21, 12, 0) / 1000;
const min = (m: number) => T0 + m * 60;
// A 1m axis of 1000 bars from T0.
const axis = { times: Array.from({ length: 1000 }, (_, i) => min(i)), intervalSec: 60 };

function el(setupId: number, selected: boolean, geometry: LayerElement["geometry"], kind: LayerElement["kind"] = "zone"): LayerElement {
  return { id: `${setupId}:${kind}`, setupId, kind, label: "", geometry, dimmed: false, selected, foundMode: "replay", direction: "bullish", foreignTimeframe: false, storyStep: 0, popover: { title: "", reason: "", rows: [], candles: [] }, copy: {} as never };
}

// Setup #265's shape: structural swing 15:46, sweep 18:55, decision 19:08,
// invalidated 23:02 (here on a 12:00-based axis).
const layers265 = [
  el(265, true, { type: "rect", t1: min(420), t2: min(662), low: 1, high: 2 }, "zone"),
  el(265, true, { type: "plan", t1: min(428), t2: min(662), entry: 2, stop: 1, entryText: "", stopText: "", targets: [] }, "plan"),
  el(265, true, { type: "segment", t1: min(226), p1: 1, t2: min(428), p2: 1 }, "structural"),
  el(265, true, { type: "marker", t: min(415), p: 1, above: false }, "swing"),
  // Another setup far later never widens the frame.
  el(999, false, { type: "rect", t1: min(900), t2: min(990), low: 1, high: 2 }),
];

describe("focusSpan", () => {
  it("runs from the earliest evidence to the setup's end, with the decision at the plan's start", () => {
    expect(focusSpan(layers265, 265)).toEqual({ start: min(226), end: min(662), decision: min(428) });
  });
  it("is null for a setup with nothing drawn", () => {
    expect(focusSpan(layers265, 1)).toBeNull();
  });
});

describe("focusRange", () => {
  const span = focusSpan(layers265, 265)!;

  it("frames the whole setup with a margin on both sides when its labels fit", () => {
    const p = focusRange(span, axis, 5000)!;
    expect(p.mode).toBe("whole");
    expect(p.from).toBeLessThan(timeToLogical(axis, span.start)!);
    expect(p.to).toBeGreaterThan(timeToLogical(axis, span.end)!);
    expect(focusNotice(p, "1m")).toBeNull();
  });

  it("frames the evidence up to the decision when the whole setup is too long, and says so", () => {
    const p = focusRange(span, axis, 1800)!;
    expect(p.mode).toBe("evidence");
    expect(p.from).toBeLessThan(timeToLogical(axis, span.start)!);
    expect(p.to).toBeLessThan(timeToLogical(axis, span.end)!);
    expect(focusNotice(p, "1m")).toBe(
      "The setup runs 7.3 hours, too long to label at 1m in one view: its evidence (3.4 hours) is framed. Zoom out for the rest.",
    );
  });

  it("at the owner's 1m width frames the part around the decision, wide enough for labels, and says the evidence span", () => {
    const p = focusRange(span, axis, 1100)!;
    expect(p.mode).toBe("around-decision");
    const dec = timeToLogical(axis, span.decision)!;
    expect(p.from).toBeLessThan(dec);
    expect(p.to).toBeGreaterThan(dec);
    // Bars are wide enough for the labels to show.
    const bars = p.to - p.from - FOCUS_RIGHT_GUTTER_PX / FOCUS_MIN_BAR_SPACING_PX;
    expect((1100 - FOCUS_RIGHT_GUTTER_PX) / bars).toBeGreaterThanOrEqual(FOCUS_MIN_BAR_SPACING_PX - 1e-9);
    expect(focusNotice(p, "1m")).toBe(
      "Evidence spans 3.4 hours; its labels cannot all fit at 1m. The part around the decision is framed; zoom in on the rest to read its labels.",
    );
  });

  it("is deterministic", () => {
    expect(focusRange(span, axis, 1100)).toEqual(focusRange(span, axis, 1100));
  });
});
