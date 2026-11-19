// system-zone.ts: the drawing rule for ARGUS's own setup zone and plan on
// the chart (fix/chart-interaction, item 1). Pure: every value comes from
// the setup and its stored events, nothing is invented.
//
// Before this, the zone started at the chart's first loaded bar whenever
// the setup's formation minute was not a bar of the chart's own
// timeframe (a 1m setup on a 1h chart: findIndex === -1, clamped to 0 --
// "from about Nov 14"), and always ended at the newest bar, whatever the
// setup's state -- so an invalidated 1m zone a few cents tall ran across
// the whole chart forever.
//
// Now:
//   - start: the order-block (or QMR) formation candle's open time;
//   - end: the earliest of the setup's triggered event (the zone was
//     mitigated: price came back into it -- the strategy's own trigger)
//     and its invalidated event. Neither stored: the last stored event's
//     time -- the setup's last known state, never "now";
//   - the plan's lines: from detection to invalidation, or to the last
//     stored event when it is still open (nothing after that is stored);
//   - on another timeframe than the setup's own it is labelled with its
//     source ("1m order block");
//   - a zone too thin or too short to see gets a minimum drawn size
//     (SystemZonePrimitive); the real high and low are in the popover,
//     the stored values never change.
import type { ApiSetup, ApiSetupEvent } from "@/api/types";

export type ZoneEndReason = "mitigated" | "invalidated" | "last-event";

export interface SystemZone {
  setupId: number;
  timeframe: string;
  state: string;
  poiType: string;
  direction: string;
  // The stored zone as the server's decimal strings (the popover) ...
  lowText: string;
  highText: string;
  // ... and for the canvas.
  low: number;
  high: number;
  startSec: number;
  endSec: number;
  endReason: ZoneEndReason;
  // The plan lines' span.
  planFromSec: number;
  planToSec: number;
  planOpen: boolean;
  // The chart is on another timeframe than the setup's.
  foreignTimeframe: boolean;
  label: string;
}

const POI_WORDS: Record<string, string> = { order_block: "order block", qmr: "QMR zone" };

export function poiWords(poiType: string): string {
  return POI_WORDS[poiType] ?? poiType.replace(/_/g, " ");
}

function sec(iso: string): number {
  return Math.floor(Date.parse(iso) / 1000);
}

function firstEvent(events: readonly ApiSetupEvent[], type: string): number | null {
  const times = events.filter((e) => e.event_type === type).map((e) => sec(e.evaluation_time));
  return times.length ? Math.min(...times) : null;
}

// systemZoneFor builds the zone for setup on a chart at chartTimeframe.
// events must be the setup's own events (any order).
export function systemZoneFor(setup: ApiSetup, events: readonly ApiSetupEvent[], chartTimeframe: string): SystemZone {
  const poi = setup.plan_snapshot.poi;
  const startSec = sec(poi.order_block_open_time);
  const own = events.filter((e) => e.setup_id === setup.id);
  const triggered = firstEvent(own, "triggered");
  const invalidated = firstEvent(own, "invalidated");
  const detected = firstEvent(own, "detected") ?? startSec;
  const lastEvent = own.length ? Math.max(...own.map((e) => sec(e.evaluation_time))) : sec(setup.evaluation_time);

  let endSec = lastEvent;
  let endReason: ZoneEndReason = "last-event";
  const ends = [
    triggered !== null ? { t: triggered, r: "mitigated" as const } : null,
    invalidated !== null ? { t: invalidated, r: "invalidated" as const } : null,
  ].filter((x): x is { t: number; r: "mitigated" | "invalidated" } => x !== null);
  if (ends.length) {
    const first = ends.reduce((a, b) => (b.t < a.t ? b : a));
    endSec = first.t;
    endReason = first.r;
  }
  // Never before its own start (a same-minute trigger).
  endSec = Math.max(endSec, startSec);

  const foreign = setup.timeframe !== chartTimeframe;
  const words = poiWords(setup.poi_type);
  return {
    setupId: setup.id,
    timeframe: setup.timeframe,
    state: setup.state,
    poiType: setup.poi_type,
    direction: setup.direction,
    lowText: poi.low,
    highText: poi.high,
    low: Number(poi.low),
    high: Number(poi.high),
    startSec,
    endSec,
    endReason,
    planFromSec: Math.min(detected, endSec),
    planToSec: Math.max(invalidated ?? lastEvent, detected),
    planOpen: invalidated === null,
    foreignTimeframe: foreign,
    label: foreign ? `${setup.timeframe} ${words}` : words,
  };
}

function utc(s: number): string {
  return new Date(s * 1000).toISOString().replace("T", " ").slice(0, 16) + " UTC";
}

// zoneEndWords says, in one line, why the zone ends where it does.
export function zoneEndWords(z: SystemZone): string {
  switch (z.endReason) {
    case "mitigated":
      return `ends ${utc(z.endSec)}, when the setup triggered (price returned into the zone)`;
    case "invalidated":
      return `ends ${utc(z.endSec)}, when the setup was invalidated`;
    case "last-event":
      return `drawn to its last stored state (${utc(z.endSec)}); nothing later is stored`;
  }
}

export function zoneStartWords(z: SystemZone): string {
  return `from ${utc(z.startSec)}`;
}
