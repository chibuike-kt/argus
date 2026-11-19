import { describe, expect, it } from "vitest";
import { systemZoneFor, zoneEndWords } from "../system-zone";
import { chartCursor, handleCursor } from "../drawings/controller";
import type { ApiSetup, ApiSetupEvent } from "@/api/types";

// The drawing rule for ARGUS's setup zone (fix/chart-interaction, item 1),
// with the shape of real SOL setup 172: a 1m bullish order block
// 72.9860-73.0440, formed 18 Nov 21:32, triggered 21:39, invalidated
// 21:43.

function setup(over: Partial<ApiSetup> = {}): ApiSetup {
  return {
    id: 172,
    instrument_id: 6,
    timeframe: "1m",
    poi_type: "order_block",
    poi_source_candle_id: 1,
    direction: "bullish",
    strategy_version: "v1",
    state: "invalidated",
    poi_source_candle_revision: 1,
    plan_snapshot: {
      status: "invalidated",
      direction: "bullish",
      entry: "73.0440",
      stop: "72.9000",
      targets: [{ level: "73.3000", rr: "2/1" }],
      poi: {
        type: "order_block",
        direction: "bullish",
        low: "72.9860",
        high: "73.0440",
        order_block_candle_id: 1,
        order_block_candle_revision: 1,
        order_block_open_time: "2026-11-18T21:32:00Z",
        qmr_fail_candle_id: 0,
        qmr_fail_candle_revision: 0,
        structural_swing_id: 1,
        in_discount: true,
      },
      protected_swing_id: 1,
    },
    last_event_id: 4,
    evaluation_time: "2026-11-18T21:43:00Z",
    algorithm_version: "v1",
    created_at: "2026-11-18T21:36:00Z",
    updated_at: "2026-11-18T21:43:00Z",
    ...over,
  } as ApiSetup;
}

function ev(type: string, at: string, setupId = 172): ApiSetupEvent {
  return {
    id: Math.random(),
    setup_id: setupId,
    event_type: type,
    plan_snapshot: setup().plan_snapshot,
    poi_source_candle_id: 1,
    poi_source_candle_revision: 1,
    evaluation_time: at,
    algorithm_version: "v1",
    created_at: at,
  } as ApiSetupEvent;
}

const s = (iso: string) => Date.parse(iso) / 1000;
const events = [
  ev("detected", "2026-11-18T21:36:00Z"),
  ev("actionable", "2026-11-18T21:37:00Z"),
  ev("triggered", "2026-11-18T21:39:00Z"),
  ev("invalidated", "2026-11-18T21:43:00Z"),
];

describe("systemZoneFor", () => {
  it("starts at the formation candle, never at the chart's first bar, even on another timeframe", () => {
    const z = systemZoneFor(setup(), events, "1h");
    expect(z.startSec).toBe(s("2026-11-18T21:32:00Z"));
  });

  it("ends when it was mitigated (triggered) -- the earliest of trigger and invalidation, never the right edge", () => {
    const z = systemZoneFor(setup(), events, "1m");
    expect(z.endSec).toBe(s("2026-11-18T21:39:00Z"));
    expect(z.endReason).toBe("mitigated");
    expect(z.planToSec).toBe(s("2026-11-18T21:43:00Z")); // the plan runs to invalidation
    expect(z.planOpen).toBe(false);
  });

  it("an invalidation before any trigger ends it at the invalidation", () => {
    const z = systemZoneFor(setup(), [events[0], ev("invalidated", "2026-11-18T21:38:00Z")], "1m");
    expect(z.endSec).toBe(s("2026-11-18T21:38:00Z"));
    expect(z.endReason).toBe("invalidated");
  });

  it("with neither stored, it is drawn to its last stored event, not to now, and says so", () => {
    const z = systemZoneFor(setup({ state: "detected" }), [events[0]], "1m");
    expect(z.endSec).toBe(s("2026-11-18T21:36:00Z"));
    expect(z.endReason).toBe("last-event");
    expect(z.planOpen).toBe(true);
    expect(zoneEndWords(z)).toMatch(/last stored state .* nothing later is stored/);
  });

  it("is labelled with its source timeframe on another chart timeframe", () => {
    expect(systemZoneFor(setup(), events, "1h").label).toBe("1m order block");
    expect(systemZoneFor(setup(), events, "1h").foreignTimeframe).toBe(true);
    expect(systemZoneFor(setup(), events, "1m").label).toBe("order block");
    expect(systemZoneFor(setup({ poi_type: "qmr" }), events, "5m").label).toBe("1m QMR zone");
  });

  it("keeps the stored high and low exactly, for the popover", () => {
    const z = systemZoneFor(setup(), events, "1h");
    expect([z.lowText, z.highText]).toEqual(["72.9860", "73.0440"]);
  });

  it("ignores another setup's events", () => {
    const z = systemZoneFor(setup(), [events[0], ev("invalidated", "2026-11-18T21:34:00Z", 999)], "1m");
    expect(z.endReason).toBe("last-event");
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
