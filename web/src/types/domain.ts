// Domain vocabulary mirrored from docs/strategy/strategy-spec.md. The frontend
// formats prices for display; the backend is the source of truth and uses
// scaled integer money types (quant.Price). Times are ISO-8601 UTC strings.

export type Direction = "bullish" | "bearish";

// Decision provenance, same four-category framework as the strategy spec.
export type Provenance = "S" | "A" | "R" | "U";

export interface Candle {
  openTime: string;
  open: number;
  high: number;
  low: number;
  close: number;
}

export type SwingType = "high" | "low";

export interface Swing {
  id: string;
  type: SwingType;
  pivotTime: string;
  confirmedAt: string;
  price: number;
}

export type StructureEventType = "BOS" | "CHOCH";

export interface StructureEvent {
  id: string;
  type: StructureEventType;
  directionAfter: Direction;
  triggerTime: string;
  sweepValidated: boolean;
}

export type SweepSide = "buy" | "sell";

export interface LiquiditySweep {
  id: string;
  side: SweepSide;
  triggerTime: string;
  penetrationExtreme: number;
  reclaimClose: number;
}

export type POIType = "order_block" | "qmr";
export type POIState = "fresh" | "swept" | "mitigated" | "invalidated";

export interface PointOfInterest {
  id: string;
  poiType: POIType;
  direction: Direction;
  sourceOpenTime: string;
  zoneLow: number;
  zoneHigh: number;
  state: POIState;
}

export interface EvidenceItem {
  label: string;
  detail: string;
  provenance: Provenance;
}

export type SetupState = "forming" | "valid" | "triggered" | "invalidated";

export interface Setup {
  id: string;
  instrument: string;
  timeframe: string;
  direction: Direction;
  poiType: POIType;
  entry: number;
  stop: number;
  targets: number[];
  rr: number;
  state: SetupState;
  evidence: EvidenceItem[];
}

export interface Instrument {
  symbol: string;
  name: string;
  venue: string;
  last: number;
  changePct: number;
}

// A compact row for the Setups list (the inbox). The full Setup, with its
// evidence chain, is loaded on the analysis workspace.
export interface SetupSummary {
  id: string;
  symbol: string;
  direction: Direction;
  poiType: POIType;
  timeframe: string;
  entry: number;
  stop: number;
  rr: number;
  state: SetupState;
  createdAt: string;
}
