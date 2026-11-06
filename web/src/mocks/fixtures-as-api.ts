// Adapts @/mocks/fixtures into the same wire shapes api/types.ts
// describes, for VITE_USE_FIXTURES=true (offline dev, screenshots). This
// is a presentation fallback, not a second source of truth: ids/scale/
// provenance fields that the fixture data never had are invented here,
// plausibly, purely so the existing rich fixture UI keeps rendering
// through the exact same hooks/components real data flows through.
import {
  instrument,
  markets,
  setups as fixtureSetups,
  setup as fixtureSetup,
  candles as fixtureCandles,
  annotations,
} from "./fixtures";
import type { ApiCandle, ApiInstrument, ApiSetup, ApiSetupEvent, ApiSetupState } from "@/api/types";

export function fixturesEnabled(): boolean {
  return import.meta.env.VITE_USE_FIXTURES === "true";
}

const PRICE_SCALE = 2;
const QTY_SCALE = 4;

function symbolToInstrumentId(symbol: string): number {
  // Stable per-symbol id so repeated calls (and setup<->instrument
  // lookups) agree within one page load.
  let h = 0;
  for (let i = 0; i < symbol.length; i++) h = (h * 31 + symbol.charCodeAt(i)) | 0;
  return Math.abs(h) % 1_000_000;
}

export function fixtureApiInstruments(): ApiInstrument[] {
  return markets.map((m) => ({
    id: symbolToInstrumentId(m.symbol),
    provider: "fixture",
    native_symbol: m.symbol,
    asset_class: "crypto_perp",
    base_asset: m.symbol.replace(/USDT\.P$|USD$/, ""),
    quote_asset: "USDT",
    price_scale: PRICE_SCALE,
    qty_scale: QTY_SCALE,
    tick_size: (1 / 10 ** PRICE_SCALE).toFixed(PRICE_SCALE),
    supported_intervals: ["1m"],
    enabled: true,
  }));
}

const toScaledInt = (v: number, scale: number) => Math.round(v * 10 ** scale);

const STATE_FOR: Record<string, ApiSetupState> = {
  forming: "detected",
  valid: "actionable",
  triggered: "triggered",
  invalidated: "invalidated",
};

function fixturePlanSnapshot(entry: number, stop: number, targets: number[], direction: "bullish" | "bearish") {
  return {
    status: "triggered",
    direction,
    entry: toScaledInt(entry, PRICE_SCALE),
    stop: toScaledInt(stop, PRICE_SCALE),
    targets: targets.map((level) => ({ level: toScaledInt(level, PRICE_SCALE), rr: "1/1" })),
    poi: {
      type: "order_block",
      direction,
      low: toScaledInt(annotations.orderBlock.low, PRICE_SCALE),
      high: toScaledInt(annotations.orderBlock.high, PRICE_SCALE),
      order_block_candle_id: 1,
      order_block_candle_revision: 1,
      order_block_open_time: fixtureCandles[annotations.orderBlock.fromIndex]?.openTime ?? fixtureCandles[0].openTime,
      qmr_fail_candle_id: 0,
      qmr_fail_candle_revision: 0,
      structural_swing_id: 0,
      in_discount: true,
    },
    protected_swing_id: 0,
  };
}

export function fixtureApiSetups(): ApiSetup[] {
  return fixtureSetups.map((s, i) => ({
    id: i + 1,
    instrument_id: symbolToInstrumentId(s.symbol),
    timeframe: s.timeframe,
    poi_type: s.poiType,
    poi_source_candle_id: 1,
    direction: s.direction,
    strategy_version: "fixture-v1",
    state: STATE_FOR[s.state],
    poi_source_candle_revision: 1,
    plan_snapshot: fixturePlanSnapshot(s.entry, s.stop, [s.entry + (s.entry - s.stop)], s.direction),
    last_event_id: null,
    evaluation_time: s.createdAt,
    algorithm_version: "fixture-v1",
    created_at: s.createdAt,
    updated_at: s.createdAt,
  }));
}

export function fixtureApiSetup(): ApiSetup {
  return {
    id: 0,
    instrument_id: symbolToInstrumentId(instrument.symbol),
    timeframe: fixtureSetup.timeframe,
    poi_type: fixtureSetup.poiType,
    poi_source_candle_id: 1,
    direction: fixtureSetup.direction,
    strategy_version: "fixture-v1",
    state: STATE_FOR[fixtureSetup.state],
    poi_source_candle_revision: 1,
    plan_snapshot: fixturePlanSnapshot(fixtureSetup.entry, fixtureSetup.stop, fixtureSetup.targets, fixtureSetup.direction),
    last_event_id: null,
    evaluation_time: fixtureCandles[fixtureCandles.length - 1].openTime,
    algorithm_version: "fixture-v1",
    created_at: fixtureCandles[0].openTime,
    updated_at: fixtureCandles[fixtureCandles.length - 1].openTime,
  };
}

export function fixtureApiSetupEvents(): ApiSetupEvent[] {
  const base = fixtureApiSetup();
  return fixtureSetup.evidence.map((_evidence, i) => ({
    id: i + 1,
    setup_id: base.id,
    event_type: i === 0 ? "detected" : i === fixtureSetup.evidence.length - 1 ? "triggered" : "actionable",
    plan_snapshot: base.plan_snapshot,
    poi_source_candle_id: 1,
    poi_source_candle_revision: 1,
    evaluation_time: fixtureCandles[Math.min(i, fixtureCandles.length - 1)].openTime,
    algorithm_version: "fixture-v1",
    created_at: fixtureCandles[Math.min(i, fixtureCandles.length - 1)].openTime,
  }));
}

export function fixtureApiCandles(): ApiCandle[] {
  return fixtureCandles.map((c, i) => {
    const next = fixtureCandles[i + 1];
    return {
      open_time: c.openTime,
      close_time: next ? next.openTime : c.openTime,
      open: c.open.toFixed(PRICE_SCALE),
      high: c.high.toFixed(PRICE_SCALE),
      low: c.low.toFixed(PRICE_SCALE),
      close: c.close.toFixed(PRICE_SCALE),
      volume: "0",
      is_closed: true,
      revision: 1,
    };
  });
}
