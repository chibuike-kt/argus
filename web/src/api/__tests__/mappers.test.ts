import { describe, expect, it } from "vitest";
import { mapApiPoiType, mapApiState, toEvidence, toPlanOverlay, toSetupSummary } from "../mappers";
import type { ApiInstrument, ApiSetup, ApiSetupEvent } from "../types";

const instrument: ApiInstrument = {
  id: 1,
  provider: "fixture",
  native_symbol: "BTC-PERP",
  asset_class: "crypto_perp",
  base_asset: "BTC",
  quote_asset: "USDT",
  price_scale: 2,
  qty_scale: 4,
  tick_size: "0.01",
  supported_intervals: ["1m"],
  enabled: true,
};

function makeSetup(overrides: Partial<ApiSetup> = {}): ApiSetup {
  return {
    id: 42,
    instrument_id: instrument.id,
    timeframe: "1m",
    poi_type: "order_block",
    poi_source_candle_id: 7,
    direction: "bullish",
    strategy_version: "v1",
    state: "triggered",
    poi_source_candle_revision: 1,
    plan_snapshot: {
      status: "triggered",
      direction: "bullish",
      // Decimal strings, exactly as the real API sends them since Part 3
      // (web-wiring-followup brief) -- not scaled integers needing the
      // instrument's own price_scale to interpret.
      entry: "90.00",
      stop: "88.00",
      targets: [{ level: "94.00", rr: "2/1" }],
      poi: {
        type: "order_block",
        direction: "bullish",
        low: "88.00",
        high: "90.00",
        order_block_candle_id: 7,
        order_block_candle_revision: 1,
        order_block_open_time: "2026-01-01T00:10:00Z",
        qmr_fail_candle_id: 0,
        qmr_fail_candle_revision: 0,
        structural_swing_id: 3,
        in_discount: true,
      },
      protected_swing_id: 3,
    },
    last_event_id: 5,
    evaluation_time: "2026-01-01T00:20:00Z",
    algorithm_version: "v1",
    created_at: "2026-01-01T00:10:00Z",
    updated_at: "2026-01-01T00:20:00Z",
    ...overrides,
  };
}

describe("mapApiState", () => {
  it("maps every backend state to a frontend SetupState", () => {
    expect(mapApiState("detected")).toBe("forming");
    expect(mapApiState("actionable")).toBe("valid");
    expect(mapApiState("triggered")).toBe("triggered");
    expect(mapApiState("invalidated")).toBe("invalidated");
  });
});

describe("mapApiPoiType", () => {
  it("passes qmr through and defaults anything else to order_block", () => {
    expect(mapApiPoiType("qmr")).toBe("qmr");
    expect(mapApiPoiType("order_block")).toBe("order_block");
  });
});

describe("toSetupSummary", () => {
  it("converts plan_snapshot's own decimal-string money to a display number", () => {
    const summary = toSetupSummary(makeSetup(), instrument);
    expect(summary.entry).toBe(90);
    expect(summary.stop).toBe(88);
    expect(summary.rr).toBe(2);
    expect(summary.symbol).toBe("BTC-PERP");
    expect(summary.state).toBe("triggered");
  });

  it("falls back to rr=0 when there are no targets", () => {
    const summary = toSetupSummary(
      makeSetup({ plan_snapshot: { ...makeSetup().plan_snapshot, targets: [] } }),
      instrument,
    );
    expect(summary.rr).toBe(0);
  });
});

describe("toPlanOverlay", () => {
  it("converts entry/stop/targets/poi zone and carries the real formation time", () => {
    const overlay = toPlanOverlay(makeSetup());
    expect(overlay.entry).toBe(90);
    expect(overlay.stop).toBe(88);
    expect(overlay.targets).toEqual([94]);
    expect(overlay.poiLow).toBe(88);
    expect(overlay.poiHigh).toBe(90);
    expect(overlay.fromTime).toBe("2026-01-01T00:10:00Z");
  });
});

describe("toEvidence", () => {
  it("labels each known event type and tags it [S]", () => {
    const event: ApiSetupEvent = {
      id: 1,
      setup_id: 42,
      event_type: "triggered",
      plan_snapshot: makeSetup().plan_snapshot,
      poi_source_candle_id: 7,
      poi_source_candle_revision: 1,
      evaluation_time: "2026-01-01T00:20:00Z",
      algorithm_version: "v1",
      created_at: "2026-01-01T00:20:00Z",
    };
    const item = toEvidence(event);
    expect(item.label).toBe("Triggered");
    expect(item.provenance).toBe("S");
  });

  it("reports no live plan for a PlanNone snapshot", () => {
    const event: ApiSetupEvent = {
      ...{
        id: 2,
        setup_id: 42,
        event_type: "invalidated",
        poi_source_candle_id: 7,
        poi_source_candle_revision: 1,
        evaluation_time: "2026-01-01T00:30:00Z",
        algorithm_version: "v1",
        created_at: "2026-01-01T00:30:00Z",
      },
      plan_snapshot: { ...makeSetup().plan_snapshot, status: "none" },
    };
    expect(toEvidence(event).detail).toMatch(/No live plan/);
  });
});
