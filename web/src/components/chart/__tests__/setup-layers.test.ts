import { describe, expect, it } from "vitest";
import { buildLayers, DEFAULT_FILTERS, filtersFromPrefs, setupVisible, zoneSpan, type LayerFilters } from "../setup-layers";
import { dashFor, minSize } from "../setup-layers-primitive";
import { chartCursor, handleCursor } from "../drawings/controller";
import type { ApiSetup } from "@/api/types";

// The shape of real SOL setup 274 (dev database, 2026-11-22): a 1m bearish
// order block 7033.00-7033.60 formed 23:54, detected and triggered 00:03,
// invalidated 00:20, with its recorded evidence chain (prices at scale 2
// here for readability; the wire sends them as decimal strings).
function sol274(over: Partial<ApiSetup> = {}): ApiSetup {
  return {
    id: 274,
    instrument_id: 6,
    timeframe: "1m",
    poi_type: "order_block",
    poi_source_candle_id: 158644,
    direction: "bearish",
    strategy_version: "swing-structure-v1",
    state: "invalidated",
    poi_source_candle_revision: 1,
    last_event_id: 3,
    evaluation_time: "2026-11-22T00:20:00Z",
    algorithm_version: "swing-structure-v1",
    created_at: "2026-11-22T01:36:20Z",
    updated_at: "2026-11-22T01:36:20Z",
    found_mode: "replay",
    evidence_recorded: true,
    detected_at: "2026-11-22T00:03:00Z",
    triggered_at: "2026-11-22T00:03:00Z",
    invalidated_at: "2026-11-22T00:20:00Z",
    plan_snapshot: {
      status: "triggered",
      direction: "bearish",
      entry: "7033.00",
      stop: "7037.60",
      targets: [
        { level: "7032.30", rr: "7/46" },
        { level: "7032.10", rr: "9/46" },
        { level: "7031.90", rr: "11/46" },
      ],
      poi: {
        type: "order_block",
        direction: "bearish",
        low: "7033.00",
        high: "7033.60",
        order_block_candle_id: 158644,
        order_block_candle_revision: 1,
        order_block_open_time: "2026-11-21T23:54:00Z",
        qmr_fail_candle_id: 0,
        qmr_fail_candle_revision: 0,
        structural_swing_id: 7642,
        in_discount: true,
      },
      protected_swing_id: 7642,
    },
    evidence: {
      setup_id: 274,
      recorded: true,
      event_type: "detected",
      timeframe: "1m",
      direction: "bearish",
      as_of: "2026-11-22T00:03:00Z",
      steps: [
        { kind: "swing", role: "swept", section: "1", result: "met", attrs: { swing_type: "high" }, candles: [{ role: "pivot", open_time: "2026-11-21T23:40:00Z" }], levels: [{ name: "swing", price: "7037.40", swing_id: 7640, time: "2026-11-21T23:40:00Z" }] },
        { kind: "sweep", section: "6", result: "met", attrs: { side: "buy" }, candles: [{ role: "trigger", open_time: "2026-11-21T23:49:00Z", candle_id: 158634 }], levels: [{ name: "swept_level", price: "7037.40", swing_id: 7640, time: "2026-11-21T23:40:00Z" }, { name: "penetration_extreme", price: "7037.60" }, { name: "reclaim_close", price: "7035.80" }] },
        { kind: "swing", role: "protected", section: "7", result: "met", attrs: { swing_type: "high" }, candles: [{ role: "pivot", open_time: "2026-11-21T23:49:00Z" }], levels: [{ name: "swing", price: "7037.60", swing_id: 7642, time: "2026-11-21T23:49:00Z" }] },
        { kind: "break", section: "7", result: "met", attrs: { break_type: "bos", direction_before: "bearish", direction_after: "bearish", structure_event: "11870" }, candles: [{ role: "trigger", open_time: "2026-11-22T00:01:00Z", candle_id: 158658 }], levels: [{ name: "broken_swing", price: "7031.20", swing_id: 7643, time: "2026-11-21T23:52:00Z" }] },
        // in_discount is true on the snapshot, yet a bearish setup's side is premium (owner decision 2).
        { kind: "premium_discount", section: "8", result: "met", attrs: { zone: "premium" }, levels: [{ name: "range_low", price: "7020.40", swing_id: 7645, time: "2026-11-22T00:00:00Z" }, { name: "range_high", price: "7037.60", swing_id: 7642, time: "2026-11-21T23:49:00Z" }, { name: "equilibrium", price: "7029.00" }] },
        { kind: "structural_liquidity", section: "9", result: "met", candles: [{ role: "pivot", open_time: "2026-11-21T23:49:00Z" }], levels: [{ name: "structural_swing", price: "7037.60", swing_id: 7642, time: "2026-11-21T23:49:00Z" }] },
        { kind: "qmr", section: "13", result: "not_met", reason: "order_block_not_failed" },
        { kind: "poi", section: "12", result: "met", attrs: { poi_type: "order_block" }, candles: [{ role: "formation", open_time: "2026-11-21T23:54:00Z", candle_id: 158644, revision: 1 }], levels: [{ name: "zone_low", price: "7033.00" }, { name: "zone_high", price: "7033.60" }] },
        { kind: "plan", section: "14", result: "met", attrs: { target_1_rr: "7/46" }, levels: [{ name: "entry", price: "7033.00" }, { name: "stop", price: "7037.60", swing_id: 7642 }] },
      ],
    },
    ...over,
  } as ApiSetup;
}

const s = (iso: string) => Date.parse(iso) / 1000;
const NOW = s("2026-11-22T01:50:00Z");
const ALL: LayerFilters = { ...DEFAULT_FILTERS, states: { detected: true, actionable: true, triggered: true, invalidated: true } };

describe("zone span (owner decision 1)", () => {
  it("starts at the formation candle and ends at the invalidation, not at the trigger", () => {
    const z = zoneSpan(sol274(), NOW);
    expect(z.start).toBe(s("2026-11-21T23:54:00Z"));
    expect(z.end).toBe(s("2026-11-22T00:20:00Z"));
    expect(z.ended).toBe(true);
  });

  it("a triggered setup that is still active runs to the newest candle", () => {
    const z = zoneSpan(sol274({ state: "triggered", invalidated_at: null }), NOW);
    expect(z.end).toBe(NOW);
    expect(z.ended).toBe(false);
  });

  it("never says mitigated", () => {
    const els = buildLayers([sol274({ state: "triggered", invalidated_at: null })], { chartTimeframe: "1m", lastCandleSec: NOW, selectedId: 274, filters: ALL });
    for (const e of els) expect(JSON.stringify(e.popover)).not.toMatch(/mitigat/i);
  });
});

describe("buildLayers", () => {
  const layers = (over: Partial<ApiSetup> = {}, chartTimeframe = "1m") =>
    buildLayers([sol274(over)], { chartTimeframe, lastCandleSec: NOW, selectedId: 274, filters: ALL });

  it("draws the selected setup's whole story, each element with a short label (the full words are the popover's)", () => {
    const els = layers();
    expect(els.map((e) => `${e.kind}:${e.label}`)).toEqual([
      "zone:Bear OB · 1m",
      "plan:Plan",
      "formation:OB candle",
      "swing:Swing high",
      "sweep:Sweep",
      "swing:Protected high",
      "break:BOS",
      "range:Premium",
      "structural:Struct. liq.",
    ]);
  });

  it("a bearish setup says premium, never the raw in_discount flag (owner decision 2)", () => {
    const range = layers().find((e) => e.kind === "range")!;
    expect(range.geometry).toMatchObject({ type: "range", side: "premium", low: 7020.4, high: 7037.6, eq: 7029 });
    expect(range.popover.title).toMatch(/^Premium/);
    expect(JSON.stringify(range.popover)).not.toMatch(/discount/i);
    const bull = layers({ direction: "bullish" }).find((e) => e.kind === "range")!;
    expect(bull.label).toBe("Discount");
  });

  it("shows the stored R:R exactly as small as it is (owner decision 3)", () => {
    const plan = layers().find((e) => e.kind === "plan")!;
    expect(plan.geometry.type === "plan" && plan.geometry.targets.map((t) => t.label)).toEqual(["TP1 7032.30 · 0.15R", "TP2 7032.10 · 0.20R", "TP3 7031.90 · 0.24R"]);
    expect(plan.geometry).toMatchObject({ entryText: "Entry 7033.00", stopText: "Stop 7037.60" });
    expect(plan.popover.rows).toContainEqual(["Target 1", "7032.30 (0.15R)"]);
  });

  it("anchors every element by time and price, so it holds on any chart timeframe", () => {
    const at1m = layers({}, "1m");
    const at1h = layers({}, "1h");
    expect(at1h.map((e) => e.geometry)).toEqual(at1m.map((e) => e.geometry));
    expect(at1h[0].foreignTimeframe).toBe(true);
    expect(at1h[0].label).toBe("Bear OB · 1m");
    // Another setup carries one short label: source timeframe and direction.
    const other = buildLayers([sol274({ id: 9, state: "triggered", invalidated_at: null })], { chartTimeframe: "1h", lastCandleSec: NOW, filters: ALL });
    expect(other.map((e) => e.label)).toEqual(["1m bear OB"]);
  });

  it("the break runs from the broken swing to the break candle, the sweep from the swept swing to the sweep candle", () => {
    const els = layers();
    expect(els.find((e) => e.kind === "break")!.geometry).toEqual({ type: "segment", t1: s("2026-11-21T23:52:00Z"), p1: 7031.2, t2: s("2026-11-22T00:01:00Z"), p2: 7031.2 });
    expect(els.find((e) => e.kind === "sweep")!.geometry).toEqual({ type: "segment", t1: s("2026-11-21T23:40:00Z"), p1: 7037.4, t2: s("2026-11-21T23:49:00Z"), p2: 7037.4 });
  });

  it("every element's popover names its setup, timeframe, state, found mode and its candles", () => {
    for (const e of layers()) {
      expect(e.popover.rows).toContainEqual(["Setup", "#274"]);
      expect(e.popover.rows).toContainEqual(["Timeframe", "1m"]);
      expect(e.popover.rows.find(([k]) => k === "State")![1]).toMatch(/^invalidated/);
      expect(e.popover.rows.find(([k]) => k === "Found")![1]).toMatch(/^replay/);
      expect(e.popover.reason.length).toBeGreaterThan(20);
      expect(e.popover.candles.length).toBeGreaterThan(0);
    }
  });

  it("copies into an editable drawing of the matching kind without touching the element", () => {
    const els = layers();
    const kinds = Object.fromEntries(els.map((e) => [e.kind + (e.label === "Protected high" ? "-p" : ""), e.copy.kind]));
    expect(kinds).toMatchObject({ zone: "rectangle", plan: "position-short", formation: "rectangle", sweep: "trendline", break: "trendline", range: "fib", structural: "trendline", swing: "text" });
    const before = JSON.stringify(els);
    const copy = { ...els[0].copy, id: "x" };
    (copy as { label?: string }).label = "changed";
    expect(JSON.stringify(els)).toBe(before);
  });

  it("an old setup without evidence gets its zone and plan only, and says so", () => {
    const els = layers({ evidence: { setup_id: 274, recorded: false }, evidence_recorded: false, found_mode: "unknown" });
    expect(els.map((e) => e.kind)).toEqual(["zone", "plan"]);
    expect(els[0].popover.reason).toMatch(/Evidence not recorded for this setup\./);
    expect(els[0].foundMode).toBe("unknown");
  });

  it("other setups get their zone only; the selected one is painted last, on top", () => {
    const other = sol274({ id: 100, state: "triggered", invalidated_at: null });
    const els = buildLayers([sol274(), other], { chartTimeframe: "1m", lastCandleSec: NOW, selectedId: 274, filters: ALL });
    expect(els[0]).toMatchObject({ setupId: 100, kind: "zone", selected: false, storyStep: -1 });
    expect(els.filter((e) => e.setupId === 100)).toHaveLength(1);
    expect(els[els.length - 1].setupId).toBe(274);
  });

  it("is deterministic: the same setups give the same elements, whatever the input order", () => {
    const a = sol274({ id: 1 });
    const b = sol274({ id: 2, state: "triggered", invalidated_at: null });
    const opts = { chartTimeframe: "5m", lastCandleSec: NOW, selectedId: 1, filters: ALL };
    expect(buildLayers([b, a], opts)).toEqual(buildLayers([a, b], opts));
  });
});

describe("filters (owner requirement e)", () => {
  it("hides invalidated setups by default and dims them when shown", () => {
    const inv = sol274({ id: 9 });
    expect(setupVisible(inv, DEFAULT_FILTERS, undefined)).toBe(false);
    const els = buildLayers([inv], { chartTimeframe: "1m", lastCandleSec: NOW, filters: ALL });
    expect(els[0].dimmed).toBe(true);
  });

  it("always draws the selected setup, dimmed when invalidated", () => {
    const els = buildLayers([sol274()], { chartTimeframe: "1m", lastCandleSec: NOW, selectedId: 274, filters: DEFAULT_FILTERS });
    expect(els.length).toBeGreaterThan(0);
    expect(els.every((e) => e.dimmed)).toBe(true);
  });

  it("filters by state and by source timeframe, from the saved preference", () => {
    const f = filtersFromPrefs({ state_triggered: false, tf_1m: false, evidence: false, state_invalidated: true });
    expect(f.states).toEqual({ detected: true, actionable: true, triggered: false, invalidated: true });
    expect(f.timeframes["1m"]).toBe(false);
    expect(setupVisible(sol274({ id: 5, state: "actionable" }), f, undefined)).toBe(false); // 1m hidden
    const noEvidence = buildLayers([sol274()], { chartTimeframe: "1m", lastCandleSec: NOW, selectedId: 274, filters: f });
    expect(noEvidence.map((e) => e.kind)).toEqual(["zone", "plan"]);
  });
});

describe("drawing details", () => {
  it("live, replay and not-recorded setups are outlined differently", () => {
    expect(dashFor("live")).toEqual([]);
    expect(dashFor("replay")).not.toEqual([]);
    expect(dashFor("unknown")).not.toEqual(dashFor("replay"));
  });

  it("a zone a few cents tall still gets a minimum drawn size, centred on its real place", () => {
    expect(minSize(100, 100.5, 4)).toEqual([98.25, 4]);
    expect(minSize(10, 50, 4)).toEqual([10, 40]);
  });
});

describe("chart cursor", () => {
  const base = { tool: "cursor", panning: false, drag: null, hover: null } as const;
  it("is a crosshair on the chart by default, never the pointer hand", () => {
    expect(chartCursor(base)).toBe("crosshair");
  });
  it("is the drawing tool's cursor: text for text, crosshair for lines and shapes", () => {
    expect(chartCursor({ ...base, tool: "text" })).toBe("text");
    expect(chartCursor({ ...base, tool: "trend" })).toBe("crosshair");
    expect(chartCursor({ ...base, tool: "rect" })).toBe("crosshair");
    expect(chartCursor({ ...base, tool: "eraser" })).toBe("crosshair");
  });
  it("is grabbing while panning", () => {
    expect(chartCursor({ ...base, panning: true })).toBe("grabbing");
  });
  it("matches the handle: resize by direction, move for a body or a free endpoint", () => {
    expect(chartCursor({ ...base, hover: { kind: "handle", handleId: "tl", resize: true } })).toBe("nwse-resize");
    expect(chartCursor({ ...base, hover: { kind: "handle", handleId: "tr", resize: true } })).toBe("nesw-resize");
    expect(chartCursor({ ...base, hover: { kind: "handle", handleId: "top", resize: true } })).toBe("ns-resize");
    expect(chartCursor({ ...base, hover: { kind: "handle", handleId: "left", resize: true } })).toBe("ew-resize");
    expect(chartCursor({ ...base, hover: { kind: "handle", handleId: "p2", resize: true } })).toBe("move");
    expect(chartCursor({ ...base, hover: { kind: "body" } })).toBe("move");
    expect(chartCursor({ ...base, drag: { mode: "handle", handleId: "br" } })).toBe("nwse-resize");
    expect(handleCursor(undefined, false)).toBe("move");
  });
  it("never returns the pointer hand in any state", () => {
    const states = [
      base,
      { ...base, panning: true },
      { ...base, hover: { kind: "body" as const } },
      ...["text", "trend", "eraser", "cursor"].map((tool) => ({ ...base, tool })),
    ];
    for (const st of states) expect(chartCursor(st)).not.toBe("pointer");
  });
});
