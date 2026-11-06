// Wire types mirroring api/openapi.yaml exactly (field names, nullability,
// enums) plus internal/setups/types.go's PlanSnapshot shape, which the
// spec itself leaves loosely typed (`additionalProperties: true`) because
// it is stored as free-form JSONB. These are *wire* shapes: OHLCV and
// tick_size stay decimal strings exactly as the API sends them (guide 13);
// plan_snapshot's own entry/stop/targets[].level are scaled integers with
// NO scale attached to them anywhere in the payload -- that is a real
// mismatch with the candles/instruments endpoints, which always send a
// pre-formatted decimal string. Call sites must pair plan_snapshot's
// numbers with the setup's own instrument's price_scale (fetched
// separately) before formatting them. See the brief's own report for this
// gap named explicitly.

export interface ApiError {
  error: {
    code: string;
    message: string;
    request_id?: string;
  };
}

export interface ApiInstrument {
  id: number;
  provider: string;
  native_symbol: string;
  asset_class: string;
  base_asset: string | null;
  quote_asset: string | null;
  price_scale: number;
  qty_scale: number;
  tick_size: string;
  supported_intervals: string[];
  enabled: boolean;
}

export interface ApiCandle {
  open_time: string;
  close_time: string;
  open: string;
  high: string;
  low: string;
  close: string;
  volume: string;
  is_closed: boolean;
  revision: number;
}

// TargetSnapshot/POISnapshot/PlanSnapshot mirror internal/setups/types.go
// field for field (read directly from source, not guessed). `level`,
// `entry`, `stop`, `low`, `high` are quant.Price's own default JSON
// encoding: a plain number holding the SCALED INTEGER, not a decimal
// string -- unlike every other money field this API sends.
export interface ApiTargetSnapshot {
  level: number;
  rr: string;
}

export interface ApiPOISnapshot {
  type: string;
  direction: string;
  low: number;
  high: number;
  order_block_candle_id: number;
  order_block_candle_revision: number;
  order_block_open_time: string;
  qmr_fail_candle_id: number;
  qmr_fail_candle_revision: number;
  structural_swing_id: number;
  in_discount: boolean;
}

export interface ApiPlanSnapshot {
  status: string;
  direction: string;
  entry: number;
  stop: number;
  targets: ApiTargetSnapshot[];
  poi: ApiPOISnapshot;
  protected_swing_id: number;
}

export type ApiSetupState = "detected" | "actionable" | "triggered" | "invalidated";

export interface ApiSetup {
  id: number;
  instrument_id: number;
  timeframe: string;
  poi_type: string;
  poi_source_candle_id: number;
  direction: string;
  strategy_version: string;
  state: ApiSetupState;
  poi_source_candle_revision: number;
  plan_snapshot: ApiPlanSnapshot;
  last_event_id: number | null;
  evaluation_time: string;
  algorithm_version: string;
  created_at: string;
  updated_at: string;
}

export type ApiSetupEventType = "detected" | "actionable" | "triggered" | "invalidated" | "amendment";

export interface ApiSetupEvent {
  id: number;
  setup_id: number;
  event_type: ApiSetupEventType;
  plan_snapshot: ApiPlanSnapshot;
  poi_source_candle_id: number;
  poi_source_candle_revision: number;
  evaluation_time: string;
  algorithm_version: string;
  created_at: string;
}
