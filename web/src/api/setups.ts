import { getJSON, type GetJSONOptions } from "./client";
import type { ApiSetup, ApiSetupEvent, ApiSetupState } from "./types";

export interface ListSetupsParams {
  instrumentId: number;
  timeframe?: string;
  state?: ApiSetupState;
  limit?: number; // server default 50, max 200
}

export function listSetups(params: ListSetupsParams, options?: GetJSONOptions): Promise<ApiSetup[]> {
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
  return getJSON<ApiSetup>(`/setups/${id}`, undefined, options);
}

export function listSetupEvents(id: number, options?: GetJSONOptions): Promise<ApiSetupEvent[]> {
  return getJSON<ApiSetupEvent[]>(`/setups/${id}/events`, undefined, options);
}
