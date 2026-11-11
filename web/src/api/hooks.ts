import { useEffect, useState } from "react";
import { getWatchlist } from "./auth";
import { ApiClientError } from "./client";
import { listInstrumentCandles, listInstruments, type ListCandlesParams } from "./instruments";
import { getSetup, listSetupEvents, listSetups, type ListSetupsParams } from "./setups";
import type { ApiCandle, ApiInstrument, ApiSetup, ApiSetupEvent } from "./types";

export interface AsyncState<T> {
  data: T | undefined;
  loading: boolean;
  error: ApiClientError | undefined;
}

// useAsyncResource is the one fetch-lifecycle implementation every hook
// below shares: loading starts true, the in-flight request is aborted on
// unmount or re-run (dep change), and a cancelled/aborted request never
// calls setState after the fact (the classic "setState on an unmounted
// component" race). fn receives the AbortSignal so every API call in this
// app is cancellable, per the brief's own requirement.
function useAsyncResource<T>(
  fn: (signal: AbortSignal) => Promise<T>,
  deps: React.DependencyList,
): AsyncState<T> {
  const [state, setState] = useState<AsyncState<T>>({
    data: undefined,
    loading: true,
    error: undefined,
  });

  useEffect(() => {
    const controller = new AbortController();
    setState((s) => ({ ...s, loading: true, error: undefined }));
    fn(controller.signal)
      .then((data) => {
        if (!controller.signal.aborted) setState({ data, loading: false, error: undefined });
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return;
        const apiErr =
          err instanceof ApiClientError ? err : new ApiClientError(String(err), 0);
        setState({ data: undefined, loading: false, error: apiErr });
      });
    return () => controller.abort();
  }, deps);

  return state;
}

export function useInstruments(): AsyncState<ApiInstrument[]> {
  return useAsyncResource((signal) => listInstruments({ signal }), []);
}

export function useInstrumentCandles(
  instrumentId: number | undefined,
  params: ListCandlesParams,
): AsyncState<ApiCandle[]> {
  return useAsyncResource(
    (signal) => {
      if (instrumentId === undefined) return Promise.resolve([]);
      return listInstrumentCandles(instrumentId, params, { signal });
    },
    [instrumentId, params.from, params.to, params.limit],
  );
}

// useSetupsAcrossInstruments is the "no instrument selector" shape the
// brief's own minimal option describes: GET /setups needs an
// instrument_id, so this fetches it once per known instrument and merges
// the results client-side, preserving the existing one-table-no-selector
// UX on /setups. Cheap in practice: the dev-fixture stack this is
// verified against seeds one or two instruments, not dozens.
export function useSetupsAcrossInstruments(
  instrumentIds: number[],
  params?: { state?: ListSetupsParams["state"]; limit?: number },
): AsyncState<ApiSetup[]> {
  return useAsyncResource(
    async (signal) => {
      if (instrumentIds.length === 0) return [];
      const perInstrument = await Promise.all(
        instrumentIds.map((instrumentId) =>
          listSetups({ instrumentId, state: params?.state, limit: params?.limit }, { signal }),
        ),
      );
      return perInstrument.flat();
    },
    // instrumentIds is rebuilt from an upstream fetch each render; join it
    // into a stable primitive so this doesn't re-fetch on every render.
    [instrumentIds.join(","), params?.state, params?.limit],
  );
}

// LATEST_CLOSE_INSTRUMENT_CAP bounds the Watchlist crypto group's "Last"
// price lookups (web-wiring-followup brief, Part 5): one
// order=desc&limit=1 candles request per instrument, which does not
// scale to an unbounded instrument list, so only the first N get a real
// Last price -- the rest keep the existing em-dash fallback rather than
// firing an unbounded burst of requests.
export const LATEST_CLOSE_INSTRUMENT_CAP = 20;

// useLatestCloses fetches the single most recent closed candle for up to
// LATEST_CLOSE_INSTRUMENT_CAP instruments, in parallel, each
// individually abortable and each failure isolated (one instrument's
// candles endpoint erroring -- e.g. a brand new instrument with no
// candles yet -- must not blank out every other row's real price).
// Keyed by instrument id; an instrument with no candles yet, or past the
// cap, is simply absent from the map (the Watchlist's own existing
// em-dash fallback for "no last price").
export function useLatestCloses(instrumentIds: number[]): AsyncState<Map<number, number>> {
  const capped = instrumentIds.slice(0, LATEST_CLOSE_INSTRUMENT_CAP);
  return useAsyncResource(
    async (signal) => {
      if (capped.length === 0) return new Map();
      const entries = await Promise.all(
        capped.map(async (instrumentId): Promise<[number, number] | undefined> => {
          try {
            const rows = await listInstrumentCandles(
              instrumentId,
              { limit: 1, order: "desc" },
              { signal },
            );
            return rows[0] ? [instrumentId, Number(rows[0].close)] : undefined;
          } catch {
            return undefined;
          }
        }),
      );
      return new Map(entries.filter((e): e is [number, number] => e !== undefined));
    },
    [capped.join(",")],
  );
}

// useWatchlist returns the signed-in user's own saved instrument ids,
// in their saved order (web light auth redesign brief, Part 3's own
// terminal integration: the Watchlist panel shows this, not every
// registered instrument).
export function useWatchlist(): AsyncState<number[]> {
  return useAsyncResource(
    async (signal) => (await getWatchlist({ signal })).map((item) => item.instrument_id),
    [],
  );
}

export function useSetup(id: number | undefined): AsyncState<ApiSetup> {
  return useAsyncResource(
    (signal) => {
      if (id === undefined) return Promise.reject(new ApiClientError("no setup id", 0));
      return getSetup(id, { signal });
    },
    [id],
  );
}

export function useSetupEvents(id: number | undefined): AsyncState<ApiSetupEvent[]> {
  return useAsyncResource(
    (signal) => {
      if (id === undefined) return Promise.resolve([]);
      return listSetupEvents(id, { signal });
    },
    [id],
  );
}
