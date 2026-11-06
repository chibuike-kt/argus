import { getJSON, type GetJSONOptions } from "./client";
import type { ApiCandle, ApiInstrument } from "./types";
import { fixturesEnabled, fixtureApiInstruments, fixtureApiCandles } from "@/mocks/fixtures-as-api";

export function listInstruments(options?: GetJSONOptions): Promise<ApiInstrument[]> {
  if (fixturesEnabled()) return Promise.resolve(fixtureApiInstruments());
  return getJSON<ApiInstrument[]>("/instruments", undefined, options);
}

export interface ListCandlesParams {
  from: string; // ISO-8601
  to: string; // ISO-8601
  limit?: number; // server caps at 1000 regardless
}

export function listInstrumentCandles(
  instrumentId: number,
  params: ListCandlesParams,
  options?: GetJSONOptions,
): Promise<ApiCandle[]> {
  if (fixturesEnabled()) return Promise.resolve(fixtureApiCandles());
  return getJSON<ApiCandle[]>(
    `/instruments/${instrumentId}/candles`,
    { from: params.from, to: params.to, limit: params.limit },
    options,
  );
}
