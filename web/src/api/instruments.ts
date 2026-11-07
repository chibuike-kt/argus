import { getJSON, type GetJSONOptions } from "./client";
import type { ApiCandle, ApiInstrument } from "./types";
import { fixturesEnabled, fixtureApiInstruments, fixtureApiCandles } from "@/mocks/fixtures-as-api";

export function listInstruments(options?: GetJSONOptions): Promise<ApiInstrument[]> {
  if (fixturesEnabled()) return Promise.resolve(fixtureApiInstruments());
  return getJSON<ApiInstrument[]>("/instruments", undefined, options);
}

export interface ListCandlesParams {
  // Both optional (web-wiring-polish brief, item 1): required server-side
  // only when order is "asc" or omitted. order="desc" means "the latest
  // N candles," which needs no range at all -- from/to omitted entirely
  // is the normal way to call it, not a workaround.
  from?: string; // ISO-8601
  to?: string; // ISO-8601
  limit?: number; // server caps at 1000 regardless
  // "desc" is the most-recent-N shortcut (web-wiring-followup brief,
  // Part 4): newest first, still capped by limit, still enforced in SQL
  // server-side. Omitted/"asc" is the original, unchanged oldest-first
  // order.
  order?: "asc" | "desc";
}

export function listInstrumentCandles(
  instrumentId: number,
  params: ListCandlesParams,
  options?: GetJSONOptions,
): Promise<ApiCandle[]> {
  if (fixturesEnabled()) return Promise.resolve(fixtureApiCandles());
  return getJSON<ApiCandle[]>(
    `/instruments/${instrumentId}/candles`,
    { from: params.from, to: params.to, limit: params.limit, order: params.order },
    options,
  );
}
