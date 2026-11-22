import { describe, expect, it } from "vitest";
import { biasSentence, invalidationWords, whySteps } from "../why-setup";
import { activityWords, statusSummary } from "../engine-activity";
import { sanitizePrefs } from "@/lib/preferences";
import type { ApiActivityItem, ApiSetup, ApiSetupEvidence } from "@/api/types";

// SOL setup 274's recorded chain (dev database, 2026-11-22), trimmed.
const evidence: ApiSetupEvidence = {
  setup_id: 274,
  recorded: true,
  event_type: "detected",
  evaluation_time: "2026-11-22T00:03:00Z",
  schema: 1,
  strategy_version: "swing-structure-v1",
  algorithm_version: "swing-structure-v1",
  steps: [
    { kind: "swing", role: "swept", section: "1", result: "met", attrs: { swing_type: "high" }, levels: [{ name: "swing", price: "7037.40", time: "2026-11-21T23:40:00Z" }] },
    { kind: "sweep", section: "6", result: "met", attrs: { side: "buy" }, candles: [{ role: "trigger", open_time: "2026-11-21T23:49:00Z" }], levels: [{ name: "swept_level", price: "7037.40" }, { name: "penetration_extreme", price: "7037.60" }, { name: "reclaim_close", price: "7035.80" }] },
    { kind: "break", section: "7", result: "met", attrs: { break_type: "bos", direction_before: "bearish", direction_after: "bearish" }, candles: [{ role: "trigger", open_time: "2026-11-22T00:01:00Z" }], levels: [{ name: "broken_swing", price: "7031.20", time: "2026-11-21T23:52:00Z" }] },
    { kind: "premium_discount", section: "8", result: "met", attrs: { zone: "premium" }, levels: [{ name: "range_low", price: "7020.40" }, { name: "range_high", price: "7037.60" }, { name: "equilibrium", price: "7029.00" }] },
    { kind: "qmr", section: "13", result: "not_met", reason: "order_block_not_failed" },
    { kind: "plan", section: "14", result: "met" },
  ],
};

const setup = {
  id: 274,
  timeframe: "1m",
  direction: "bearish",
  poi_type: "order_block",
  plan_snapshot: { entry: "7033.00", stop: "7037.60", targets: [{ level: "7032.30", rr: "7/46" }], poi: { in_discount: true } },
} as unknown as ApiSetup;

describe("Why this setup", () => {
  it("states the bias in one templated sentence from the recorded sweep, break and side", () => {
    expect(biasSentence(setup, evidence)).toBe(
      "Bearish: buy-side liquidity above 7037.40 was swept at 2026-11-21 23:49 UTC, then a BOS closed through 7031.20 at 2026-11-22 00:01 UTC, the order block sits in premium.",
    );
  });

  it("lists every recorded step in order, with its result and values", () => {
    const steps = whySteps(setup, evidence);
    expect(steps.map((s) => [s.title, s.result])).toEqual([
      ["Swing high (swept)", "met"],
      ["Buy-side sweep", "met"],
      ["BOS (bearish to bearish)", "met"],
      ["Zone in premium", "met"],
      ["QMR check", "not_met"],
      ["Plan", "met"],
    ]);
    expect(steps[1].detail).toBe("through 7037.40 to 7037.60, closed back at 7035.80 · 2026-11-21 23:49 UTC");
    expect(steps[4].detail).toBe("not met (order block not failed)");
    expect(steps[5].detail).toBe("entry 7033.00, stop 7037.60, TP1 7032.30 (0.15R)");
    // Rows map to chart elements; the QMR check draws nothing.
    expect(steps.map((s) => s.elementId)).toEqual(["274:swing:swept", "274:sweep", "274:break", "274:range", null, "274:plan"]);
  });

  it("names the side by direction, never by the raw in_discount flag", () => {
    expect(whySteps(setup, evidence)[3].title).toBe("Zone in premium");
    expect(whySteps({ ...setup, direction: "bullish" } as ApiSetup, evidence)[3].title).toBe("Zone in discount");
  });

  it("an old setup has no steps and says its evidence was not recorded", () => {
    expect(whySteps(setup, { setup_id: 274, recorded: false })).toEqual([]);
    expect(biasSentence(setup, { setup_id: 274, recorded: false })).toMatch(/Evidence not recorded for this setup\.$/);
  });

  it("says what would invalidate it, from the stored stop", () => {
    expect(invalidationWords(setup)).toBe(
      "A finalized 1m candle closing above 7037.60, the protected high and the stop (spec sections 14 and 15.2).",
    );
  });
});

describe("engine status and activity", () => {
  it("summarises the cursors without inventing anything", () => {
    const s = statusSummary({
      as_of: "2026-11-22T01:47:00Z",
      instruments: [
        { instrument_id: 4, native_symbol: "BTC", provider: "hyperliquid", analysed: true, evaluated_through: "2026-11-22T01:46:00Z", last_evaluated_at: "2026-11-22T01:46:28Z", latest_closed_close_time: "2026-11-22T01:46:00Z", behind_seconds: 0, closed_candles_behind: 0 },
        { instrument_id: 5, native_symbol: "ETH", provider: "hyperliquid", analysed: true, evaluated_through: "2026-11-22T01:40:00Z", last_evaluated_at: "2026-11-22T01:40:30Z", latest_closed_close_time: "2026-11-22T01:46:00Z", behind_seconds: 360, closed_candles_behind: 6 },
        { instrument_id: 7, native_symbol: "DOGE", provider: "hyperliquid", analysed: false, evaluated_through: null, last_evaluated_at: null, latest_closed_close_time: null, behind_seconds: null, closed_candles_behind: 0 },
      ],
    });
    expect(s).toEqual({ analysed: 2, notAnalysed: 1, through: "2026-11-22T01:40:00Z", behind: 360, lastEvaluated: "2026-11-22T01:46:28Z" });
  });

  it("words each stored row plainly", () => {
    const base = { id: 1, instrument_id: 6, timeframe: "1m", at: "2026-11-22T01:36:20Z", event_time: "2026-11-22T00:20:00Z" };
    expect(activityWords({ ...base, kind: "setup", detail: "invalidated", setup_id: 274, direction: "bearish", poi_type: "order_block" } as ApiActivityItem, "SOL")).toBe(
      "SOL setup #274 (bearish order block, 1m) invalidated",
    );
    expect(activityWords({ ...base, kind: "setup", detail: "detected", setup_id: 274, direction: "bearish", poi_type: "qmr" } as ApiActivityItem, "SOL")).toBe(
      "SOL setup #274 (bearish QMR zone, 1m) found",
    );
    expect(activityWords({ ...base, kind: "structure", detail: "choch", direction: "bullish" } as ApiActivityItem, "BTC")).toBe("BTC CHoCH, bullish (1m)");
    expect(activityWords({ ...base, kind: "gap", detail: "backfilled", event_time: "2026-11-18T09:15:00Z", gap_end: "2026-11-18T09:18:00Z" } as ApiActivityItem, "BTC")).toBe(
      "BTC gap 09:15 UTC to 09:18 UTC resolved: backfilled",
    );
  });
});

describe("setup layer preferences", () => {
  it("keeps only the known filter names and booleans", () => {
    expect(sanitizePrefs({ setup_layers: { state_invalidated: true, tf_5m: false, evidence: "yes", state_expired: true, "a/b": true } })).toEqual({
      setup_layers: { state_invalidated: true, tf_5m: false },
    });
    expect(sanitizePrefs({ setup_layers: [true] })).toEqual({});
  });
});
