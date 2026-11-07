// Wire types mirroring api/openapi.yaml exactly (field names, nullability,
// enums), including internal/setups/types.go's PlanSnapshot shape -- now
// typed on both sides (web-wiring-followup brief, Part 3): entry/stop/
// targets[].level/poi.low/poi.high arrive as decimal strings built from
// the setup's own instrument price_scale, exactly like candle OHLCV and
// instrument tick_size (guide 13), not the bare scaled integers this API
// sent before Part 3. No call site needs to pair these with price_scale
// itself anymore -- Number() on the string is enough, the same as any
// other already-formatted money field.

export interface ApiError {
  error: {
    code: string;
    message: string;
    request_id?: string;
  };
}

// ApiAuthUser/ApiAuthSession mirror api/openapi.yaml's AuthUser/
// AuthSession schemas exactly (web auth UI brief, Part A).
export interface ApiAuthUser {
  id: number;
  email: string;
  email_verified: boolean;
  created_at: string;
}

export interface ApiAuthSession {
  id: number;
  created_at: string;
  last_seen_at: string;
  user_agent: string | null;
  ip: string | null;
  current: boolean;
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
// field for field (read directly from source, not guessed), and now
// api/openapi.yaml's own typed TargetSnapshot/POISnapshot/PlanSnapshot
// schemas (Part 3 made them typed rather than a free-form object).
export interface ApiTargetSnapshot {
  level: string;
  rr: string;
}

export interface ApiPOISnapshot {
  type: string;
  direction: string;
  low: string;
  high: string;
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
  entry: string;
  stop: string;
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
