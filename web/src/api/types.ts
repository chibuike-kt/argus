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
// AuthSession schemas exactly (web auth UI brief, Part A;
// display_name/onboarding_completed added by the web light auth
// redesign brief, Part 2).
export interface ApiAuthUser {
  id: number;
  email: string;
  email_verified: boolean;
  created_at: string;
  display_name: string | null;
  onboarding_completed: boolean;
}

// ApiWatchlistItem mirrors api/openapi.yaml's WatchlistItem schema --
// position is this user's own display order (PUT /watchlist replaces
// the whole list at once; position is the index in the array sent).
export interface ApiWatchlistItem {
  instrument_id: number;
  position: number;
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

// GET /instruments/{id}/candles' own response shape (live-chart-pipeline
// brief, Phase 1): candles plus the join point with the live WebSocket
// stream (docs/briefs/adr-004-ws-contract-draft.md's own `snapshot`
// message uses the same field names) -- the instrument's own latest
// closed candle as of now, independent of this response's own from/to
// range. Both null only when the instrument has no closed candle yet.
export interface ApiCandleHistory {
  candles: ApiCandle[];
  last_closed_open_time: string | null;
  last_closed_revision: number | null;
}

// GET /instruments/stats' own row (real-panel-data brief). Every value
// is computed on the server with exact scaled-integer arithmetic and
// sent as a decimal string; null means absent (the candles it needs do
// not all exist) -- never zero. Prices are at the instrument's
// price_scale, volume_24h at its qty_scale (base asset), percentages in
// percent units with exactly 2 decimals and no "%" sign.
export type ApiPerformancePeriod = "1D" | "1W" | "1M" | "3M" | "6M" | "1Y";

export const PERFORMANCE_PERIODS: readonly ApiPerformancePeriod[] = ["1D", "1W", "1M", "3M", "6M", "1Y"];

export interface ApiInstrumentStats extends ApiLiveFigures {
  // The latest CLOSED 1-minute candle: the performance anchor and "data
  // finalized through", not the live price (last_price, below).
  last_close: string | null;
  last_closed_open_time: string | null;
  performance: Record<ApiPerformancePeriod, string | null>;
}

// The live figures (D3, product overhaul brief): the same fields, the
// same server computation and the same rendering in GET
// /instruments/stats and in the `stats` WebSocket message, so the
// panel's first load and every live update come from one source.
export interface ApiLiveFigures {
  instrument_id: number;
  // The newest stored candle's close: the forming candle's latest close,
  // or the latest closed candle's when nothing is forming.
  last_price: string | null;
  last_price_closed: boolean | null;
  // Exchange event time of that candle (its open time, minute-grained).
  as_of: string | null;
  // When ARGUS stored its latest value (ingestion time).
  last_tick_at: string | null;
  change_24h: string | null;
  change_24h_pct: string | null;
  // The exact minute change_24h is measured against, found or not.
  change_24h_reference_open_time: string | null;
  volume_24h: string | null;
  // How many of the 1440 minutes of the 24h window are stored.
  volume_24h_minutes: number | null;
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

export interface ApiSetup extends ApiSetupLifecycle {
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

// ---- Phase 6: lifecycle, evidence, structure, engine status, activity ----

export type ApiFoundMode = "live" | "replay" | "unknown";

export interface ApiSetupLifecycle {
  // How the setup was found (Phase 6.1): live, replay, or unknown for a
  // setup created before it was recorded.
  found_mode?: ApiFoundMode;
  evidence_recorded?: boolean;
  // Evaluation times of its detected, triggered and invalidated events;
  // null when that event never happened.
  detected_at?: string | null;
  triggered_at?: string | null;
  invalidated_at?: string | null;
  // Only with include_evidence=true.
  evidence?: ApiSetupEvidence | null;
}

export interface ApiEvidenceCandle {
  role: string;
  open_time?: string | null;
  candle_id?: number;
  revision?: number;
}

export interface ApiEvidenceLevel {
  name: string;
  price: string;
  swing_id?: number;
  time?: string | null;
}

export type ApiEvidenceKind =
  | "swing"
  | "sweep"
  | "break"
  | "premium_discount"
  | "structural_liquidity"
  | "qmr"
  | "poi"
  | "plan";

export interface ApiEvidenceStep {
  kind: ApiEvidenceKind;
  role?: string;
  section: string;
  result: "met" | "not_met" | "unevaluable";
  reason?: string;
  candles?: ApiEvidenceCandle[];
  levels?: ApiEvidenceLevel[];
  attrs?: Record<string, string>;
}

export interface ApiSetupEvidence {
  setup_id: number;
  // False: "evidence not recorded for this setup" (created before it was).
  recorded: boolean;
  event_type?: string;
  evaluation_time?: string;
  schema?: number;
  timeframe?: string;
  direction?: string;
  strategy_version?: string;
  algorithm_version?: string;
  as_of?: string;
  steps?: ApiEvidenceStep[];
}

export interface ApiStructureLevel {
  swing_id: number;
  price: string;
  time: string;
}

export interface ApiStructureWindow {
  instrument_id: number;
  timeframe: string;
  from: string;
  to: string;
  swings: { id: number; swing_type: "high" | "low"; pivot_time: string; price: string; confirmed_at: string }[];
  breaks: {
    id: number;
    event_type: "direction_established" | "bos" | "choch";
    direction_before: string;
    direction_after: string;
    trigger_open_time: string;
    sweep_validated: boolean;
    sweep_id?: number | null;
    protected_swing_id?: number | null;
    broken_swing?: ApiStructureLevel | null;
  }[];
  sweeps: {
    id: number;
    side: "buy" | "sell";
    trigger_open_time: string;
    penetration_extreme: string;
    reclaim_close: string;
    swept_swing: ApiStructureLevel;
  }[];
  next_from: string | null;
}

export interface ApiEngineInstrumentStatus {
  instrument_id: number;
  native_symbol: string;
  provider: string;
  analysed: boolean;
  timeframe?: string | null;
  evaluated_through: string | null;
  last_evaluated_at: string | null;
  latest_closed_close_time: string | null;
  behind_seconds: number | null;
  closed_candles_behind: number;
}

export interface ApiEngineStatus {
  as_of: string;
  instruments: ApiEngineInstrumentStatus[];
}

export type ApiActivityKind = "setup" | "structure" | "gap";

export interface ApiActivityItem {
  kind: ApiActivityKind;
  id: number;
  instrument_id: number;
  timeframe: string;
  at: string;
  event_time: string;
  detail: string;
  setup_id?: number | null;
  direction?: string | null;
  poi_type?: string | null;
  found_mode?: string | null;
  gap_end?: string | null;
}

export interface ApiActivityPage {
  items: ApiActivityItem[];
  next_cursor: string | null;
}
