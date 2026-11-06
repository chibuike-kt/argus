import { useEffect, useState } from "react";
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
