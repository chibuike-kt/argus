import { getJSON, type GetJSONOptions } from "./client";
import type { ApiSetup, ApiSetupEvent, ApiSetupEvidence, ApiSetupState } from "./types";

export interface ListSetupsParams {
  instrumentId: number;
  timeframe?: string;
  state?: ApiSetupState;
  limit?: number; // server default 50, max 200
  // Each setup carries its recorded evidence chain (Phase 6).
  includeEvidence?: boolean;
}

export function listSetups(params: ListSetupsParams, options?: GetJSONOptions): Promise<ApiSetup[]> {
  return getJSON<ApiSetup[]>(
    "/setups",
    {
      instrument_id: params.instrumentId,
      timeframe: params.timeframe,
      state: params.state,
      limit: params.limit,
      include_evidence: params.includeEvidence ? "true" : undefined,
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

export function getSetupEvidence(id: number, options?: GetJSONOptions): Promise<ApiSetupEvidence> {
  return getJSON<ApiSetupEvidence>(`/setups/${id}/evidence`, undefined, options);
}
