// Mapping from wire shapes (api/types.ts) to this app's existing display
// types (@/types/domain), so the already-built components keep their
// current props unchanged (the brief's own "pure wiring" rule) -- the
// conversion to a plain display `number` happens once, here, at the edge,
// using the exact scaled-integer string path (api/money.ts), never a raw
// division.
import { formatScaledInt, parseRatString, scaledIntToNumber } from "./money";
import type { ApiInstrument, ApiSetup, ApiSetupEvent } from "./types";
import type { EvidenceItem, POIType, SetupState, SetupSummary } from "@/types/domain";

// The backend's 4 real lifecycle states collapse onto the frontend's
// existing 4-value SetupState type, but not 1:1 by name -- "forming" was
// invented for the fixture scaffold and has no backend equivalent.
// "detected" (spec section 15.2: a plan exists but may not be actionable
// yet) is the closest real match for what "forming" was standing in for.
export function mapApiState(state: ApiSetup["state"]): SetupState {
  switch (state) {
    case "detected":
      return "forming";
    case "actionable":
      return "valid";
    case "triggered":
      return "triggered";
    case "invalidated":
      return "invalidated";
  }
}

export function mapApiPoiType(poiType: string): POIType {
  return poiType === "qmr" ? "qmr" : "order_block";
}

// instrumentSymbol defaults to native_symbol -- there is no separate
// "display symbol" field on the wire Instrument.
export function instrumentSymbol(inst: ApiInstrument): string {
  return inst.native_symbol;
}

// toSetupSummary maps one API Setup (plus its own instrument, for the
// price_scale plan_snapshot's entry/stop need and the symbol for
// display) into the row shape the /setups table and the "other setups"
// strip already render.
export function toSetupSummary(setup: ApiSetup, instrument: ApiInstrument): SetupSummary {
  const scale = instrument.price_scale;
  const targets = setup.plan_snapshot.targets;
  const lastTarget = targets[targets.length - 1];
  // R:R shown in these summary rows is the furthest target's own R:R, the
  // same quantity the fixture's single `rr` field stood in for -- parsed
  // from the API's own exact rational string, not recomputed from prices.
  const rr = lastTarget ? parseRatString(lastTarget.rr) : 0;
  return {
    id: String(setup.id),
    symbol: instrumentSymbol(instrument),
    direction: setup.direction === "bearish" ? "bearish" : "bullish",
    poiType: mapApiPoiType(setup.poi_type),
    timeframe: setup.timeframe,
    entry: scaledIntToNumber(setup.plan_snapshot.entry, scale),
    stop: scaledIntToNumber(setup.plan_snapshot.stop, scale),
    rr,
    state: mapApiState(setup.state),
    createdAt: setup.created_at,
  };
}

// toEvidence maps a setup's event history (GET /setups/{id}/events) to
// the existing EvidenceItem list shape. The backend does not carry a
// provenance tag per event the way the strategy spec's own decisions do
// -- every event is the engine's own deterministic output, so each one
// is tagged [S] (source-confirmed: it is exactly what the engine
// computed, not a guess this UI is making on top of it).
export function toEvidence(event: ApiSetupEvent): EvidenceItem {
  const label = EVENT_LABEL[event.event_type] ?? event.event_type;
  const plan = event.plan_snapshot;
  const detail =
    plan.status === "none"
      ? `No live plan at this evaluation step.`
      : `${plan.direction === "bullish" ? "Bullish" : "Bearish"} ${plan.poi.type.replace("_", " ")}, entry/stop in the snapshot at this step, evaluated ${event.evaluation_time}.`;
  return { label, detail, provenance: "S" };
}

const EVENT_LABEL: Record<string, string> = {
  detected: "Detected",
  actionable: "Became actionable",
  triggered: "Triggered",
  invalidated: "Invalidated",
  amendment: "Amendment (corrected candle)",
};

// toPlanOverlay builds the chart's plan-line overlay (entry/stop/targets)
// directly from a setup's plan_snapshot and its own instrument's scale --
// the one piece of "real" structural overlay this wiring brief's scope
// covers (see the report for what is deliberately left out: BOS/CHOCH
// labels, swing labels and the equilibrium line have no per-setup API
// source today).
export interface PlanOverlay {
  entry: number;
  stop: number;
  targets: number[];
  poiLow: number;
  poiHigh: number;
  // The POI's own formation candle (order_block_open_time for both
  // order-block and QMR POIs -- a QMR's identity is still anchored to
  // the order block it failed from, per internal/setups/types.go's own
  // comment on poi_source_candle_id). The chart overlay anchors both the
  // zone rectangle and the plan box to this real timestamp, not a fixture
  // array index.
  fromTime: string;
}

export function toPlanOverlay(setup: ApiSetup, instrument: ApiInstrument): PlanOverlay {
  const scale = instrument.price_scale;
  const plan = setup.plan_snapshot;
  return {
    entry: scaledIntToNumber(plan.entry, scale),
    stop: scaledIntToNumber(plan.stop, scale),
    targets: plan.targets.map((t) => scaledIntToNumber(t.level, scale)),
    poiLow: scaledIntToNumber(plan.poi.low, scale),
    poiHigh: scaledIntToNumber(plan.poi.high, scale),
    fromTime: plan.poi.order_block_open_time,
  };
}

// formatMoney is the one place a real, already-formatted decimal string
// (candle OHLCV, instrument tick_size) gets turned into a display
// number for an existing component's sake -- Number() on a string the
// API itself already rendered exactly, never a division.
export function formatMoney(decimalString: string): number {
  return Number(decimalString);
}

export { formatScaledInt, parseRatString };
