import { getJSON, type GetJSONOptions } from "./client";
import type { ApiCandleHistory, ApiInstrument } from "./types";
import { fixturesEnabled, fixtureApiInstruments, fixtureApiCandles } from "@/mocks/fixtures-as-api";

export function listInstruments(options?: GetJSONOptions): Promise<ApiInstrument[]> {
  if (fixturesEnabled()) return Promise.resolve(fixtureApiInstruments());
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
  // "desc" is the most-recent-N shortcut (web-wiring-followup brief,
  // Part 4): newest first, still capped by limit, still enforced
  // server-side. Omitted/"asc" is the original, unchanged oldest-first
  // order.
  order?: "asc" | "desc";
}

function fixtureCandleHistory(): ApiCandleHistory {
  const candles = fixtureApiCandles();
  const last = candles[candles.length - 1];
  return {
    candles,
    last_closed_open_time: last?.open_time ?? null,
    last_closed_revision: last?.revision ?? null,
  };
}

export function listInstrumentCandles(
  instrumentId: number,
  params: ListCandlesParams,
  options?: GetJSONOptions,
): Promise<ApiCandleHistory> {
  if (fixturesEnabled()) return Promise.resolve(fixtureCandleHistory());
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
