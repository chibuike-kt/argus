import { getJSON, type GetJSONOptions } from "./client";
import type { ApiCandleHistory, ApiInstrument, ApiInstrumentStats } from "./types";

export function listInstruments(options?: GetJSONOptions): Promise<ApiInstrument[]> {
  return getJSON<ApiInstrument[]>("/instruments", undefined, options);
}

// CandleInterval matches the server's own enum exactly (live-chart-
// pipeline brief, Phase 1): server-side aggregation only, the browser
// never aggregates, so history and the live stream's derived candles
// cannot disagree.
export type CandleInterval = "1m" | "5m" | "15m" | "1h" | "4h" | "1d";

export interface ListCandlesParams {
  // interval omitted means "1m" server-side (so every existing caller
  // that never passes this is unaffected) -- not defaulted here, so a
  // caller can tell "I asked for 1m" apart from "I didn't ask."
  interval?: CandleInterval;
  // Both optional (web-wiring-polish brief, item 1): required server-side
  // only when order is "asc" or omitted, and only at interval 1m --
  // above 1m the server requires both regardless of order (no open-ended
  // "latest N" shortcut for aggregated intervals).
  from?: string; // ISO-8601
  to?: string; // ISO-8601
  limit?: number; // server caps at 1000 regardless
  // "desc" is the most-recent-N shortcut: newest first, still capped by
  // limit, still enforced server-side. Extended to every interval, not
  // only 1m (live-chart-pipeline brief, adr-004-ws-contract-review.md
  // addendum item C) -- from/to are only required when order is "asc"
  // or omitted, regardless of interval. Omitted/"asc" is the original,
  // unchanged oldest-first order, which still needs both at every
  // interval.
  order?: "asc" | "desc";
}

export function listInstrumentCandles(
  instrumentId: number,
  params: ListCandlesParams,
  options?: GetJSONOptions,
): Promise<ApiCandleHistory> {
  return getJSON<ApiCandleHistory>(
    `/instruments/${instrumentId}/candles`,
    {
      interval: params.interval,
      from: params.from,
      to: params.to,
      limit: params.limit,
      order: params.order,
    },
    options,
  );
}

// MAX_STATS_IDS matches the server's own cap on GET /instruments/stats.
export const MAX_STATS_IDS = 50;

// listInstrumentStats is one request for up to MAX_STATS_IDS instruments
// (the whole watchlist). Unknown or disabled ids are simply omitted by
// the server.
export function listInstrumentStats(ids: readonly number[], options?: GetJSONOptions): Promise<ApiInstrumentStats[]> {
  if (ids.length === 0) return Promise.resolve([]);
  const capped = ids.slice(0, MAX_STATS_IDS);
  return getJSON<ApiInstrumentStats[]>("/instruments/stats", { ids: capped.join(",") }, options);
}
