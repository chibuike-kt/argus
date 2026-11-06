import { getJSON, type GetJSONOptions } from "./client";
import type { ApiSetup, ApiSetupEvent, ApiSetupState } from "./types";
import { fixturesEnabled, fixtureApiSetups, fixtureApiSetup, fixtureApiSetupEvents } from "@/mocks/fixtures-as-api";

export interface ListSetupsParams {
  instrumentId: number;
  timeframe?: string;
  state?: ApiSetupState;
  limit?: number; // server default 50, max 200
}

export function listSetups(params: ListSetupsParams, options?: GetJSONOptions): Promise<ApiSetup[]> {
  if (fixturesEnabled()) {
    const all = fixtureApiSetups().filter((s) => s.instrument_id === params.instrumentId);
    return Promise.resolve(params.state ? all.filter((s) => s.state === params.state) : all);
  }
  return getJSON<ApiSetup[]>(
    "/setups",
    {
      instrument_id: params.instrumentId,
      timeframe: params.timeframe,
      state: params.state,
      limit: params.limit,
    },
    options,
  );
}

export function getSetup(id: number, options?: GetJSONOptions): Promise<ApiSetup> {
  if (fixturesEnabled()) return Promise.resolve(fixtureApiSetup());
  return getJSON<ApiSetup>(`/setups/${id}`, undefined, options);
}

export function listSetupEvents(id: number, options?: GetJSONOptions): Promise<ApiSetupEvent[]> {
  if (fixturesEnabled()) return Promise.resolve(fixtureApiSetupEvents());
  return getJSON<ApiSetupEvent[]>(`/setups/${id}/events`, undefined, options);
}
