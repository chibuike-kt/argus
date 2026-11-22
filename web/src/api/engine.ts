import { getJSON, type GetJSONOptions } from "./client";
import type { ApiActivityKind, ApiActivityPage, ApiEngineStatus, ApiStructureWindow } from "./types";

// GET /engine/status (Phase 6.5): what the engine has evaluated, from the
// stored processing cursors only.
export function getEngineStatus(options?: GetJSONOptions): Promise<ApiEngineStatus> {
  return getJSON<ApiEngineStatus>("/engine/status", undefined, options);
}

export interface ListActivityParams {
  instrumentIds?: number[];
  kinds?: ApiActivityKind[];
  cursor?: string;
  limit?: number;
}

// GET /activity (Phase 6.5): stored setup events, structure events and
// resolved gaps, newest first.
export function listActivity(params: ListActivityParams, options?: GetJSONOptions): Promise<ApiActivityPage> {
  return getJSON<ApiActivityPage>(
    "/activity",
    { instrument_id: params.instrumentIds, kind: params.kinds, cursor: params.cursor, limit: params.limit },
    options,
  );
}

// GET /instruments/{id}/structure (Phase 6.2): stored swings, breaks and
// sweeps in [from, to). The chart draws only each setup's own evidence
// (owner decision 4); this is for reading the stored structure behind it.
export function getInstrumentStructure(
  instrumentId: number,
  params: { from: string; to: string; timeframe?: string; limit?: number },
  options?: GetJSONOptions,
): Promise<ApiStructureWindow> {
  return getJSON<ApiStructureWindow>(`/instruments/${instrumentId}/structure`, params, options);
}
