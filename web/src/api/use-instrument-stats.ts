import { useCallback, useEffect, useRef, useState } from "react";
import { ApiClientError } from "./client";
import { listInstrumentStats } from "./instruments";
import type { ApiInstrumentStats } from "./types";

// STATS_REFRESH_MS is how often the side panel re-reads
// GET /instruments/stats. [U] -- an unconfirmed UI value from the
// real-panel-data brief (30 seconds), not an ADR decision; it lives here
// and nowhere else.
export const STATS_REFRESH_MS = 30_000;

export interface InstrumentStatsState {
  // Keyed by instrument id. Never holds rows fetched for a different id
  // set than the one currently asked for (see the request token below).
  data: Map<number, ApiInstrumentStats> | undefined;
  loading: boolean;
  error: ApiClientError | undefined;
}

interface Stored {
  key: string;
  data: Map<number, ApiInstrumentStats> | undefined;
  error: ApiClientError | undefined;
}

// useInstrumentStats reads GET /instruments/stats for ids: once when ids
// change, every intervalMs after that, and again whenever refreshKey
// changes (the analysis workspace passes the live socket's latest
// closed-candle sequence, so a closed candle refreshes the figures
// without waiting for the timer).
//
// Stale responses never reach the screen. Every fetch takes a new token
// and aborts the previous request; a response is applied only if its
// token is still the latest AND it was made for the id set currently
// asked for. On top of that, data is only returned when its stored key
// matches the current ids, so switching instruments shows nothing (em
// dashes) until the new instrument's own response arrives -- never the
// previous instrument's numbers.
//
// A failed refresh keeps the last good data for the same ids (each
// figure carries its own last_closed_open_time, so it is never passed
// off as newer than it is) and reports the error alongside it.
export function useInstrumentStats(
  ids: readonly number[],
  refreshKey?: unknown,
  intervalMs: number = STATS_REFRESH_MS,
): InstrumentStatsState {
  const key = ids.join(",");
  const [stored, setStored] = useState<Stored>({ key: "", data: undefined, error: undefined });
  const tokenRef = useRef(0);
  const controllerRef = useRef<AbortController | null>(null);

  const fetchNow = useCallback(() => {
    const token = ++tokenRef.current;
    controllerRef.current?.abort();
    if (key === "") {
      controllerRef.current = null;
      setStored({ key, data: new Map(), error: undefined });
      return;
    }
    const controller = new AbortController();
    controllerRef.current = controller;
    const requestIds = key.split(",").map(Number);
    listInstrumentStats(requestIds, { signal: controller.signal })
      .then((rows) => {
        if (token !== tokenRef.current) return;
        setStored({ key, data: new Map(rows.map((r) => [r.instrument_id, r])), error: undefined });
      })
      .catch((err: unknown) => {
        if (token !== tokenRef.current || controller.signal.aborted) return;
        const apiErr = err instanceof ApiClientError ? err : new ApiClientError(String(err), 0);
        setStored((prev) => (prev.key === key ? { ...prev, error: apiErr } : { key, data: undefined, error: apiErr }));
      });
  }, [key]);

  useEffect(() => {
    fetchNow();
    const timer = setInterval(fetchNow, intervalMs);
    return () => {
      clearInterval(timer);
      // Invalidate anything still in flight for this id set.
      tokenRef.current++;
      controllerRef.current?.abort();
    };
  }, [fetchNow, intervalMs]);

  // refreshKey's first value is covered by the effect above; only a
  // later change triggers an extra fetch.
  const firstRefreshKey = useRef(true);
  useEffect(() => {
    if (firstRefreshKey.current) {
      firstRefreshKey.current = false;
      return;
    }
    if (refreshKey === undefined || refreshKey === null) return;
    fetchNow();
    // fetchNow is deliberately not a dependency: an id change already
    // fetches through the effect above.
  }, [refreshKey]);

  const current = stored.key === key;
  return {
    data: current ? stored.data : undefined,
    loading: !current || (stored.data === undefined && stored.error === undefined),
    error: current ? stored.error : undefined,
  };
}
