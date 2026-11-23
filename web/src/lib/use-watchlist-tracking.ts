import { useCallback, useEffect, useState } from "react";
import { getWatchlistTracking, listHyperliquidCatalog } from "@/api/watchlist";
import type { ApiInstrument, ApiWatchlistTracking } from "@/api/types";
import { useWatchlistStore } from "./watchlist-store";

// [U] display values: how often the watchlist row states are re-read --
// quickly while a symbol is still settling (waiting, loading history,
// retrying), slowly once everything is live.
export const TRACKING_FAST_MS = 5_000;
export const TRACKING_SLOW_MS = 30_000;

const SETTLED = new Set(["live", "stopped", "failed", "needs_attention"]);

// useWatchlistTracking polls GET /watchlist/tracking (Phase 4.1a): each
// row's ingestion state and the system cap. Re-read at once when the list
// changes; paused while the tab is hidden.
export function useWatchlistTracking(): { data: ApiWatchlistTracking | undefined; error: string | undefined } {
  const wl = useWatchlistStore();
  const [data, setData] = useState<ApiWatchlistTracking>();
  const [error, setError] = useState<string>();
  const key = `${(wl.ids ?? []).join(",")}|${wl.version}`;
  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = async () => {
      if (typeof document !== "undefined" && document.visibilityState === "hidden") {
        timer = setTimeout(tick, TRACKING_SLOW_MS);
        return;
      }
      let next = TRACKING_SLOW_MS;
      try {
        const d = await getWatchlistTracking();
        if (stopped) return;
        setData(d);
        setError(undefined);
        if (d.items.some((i) => !i.state || !SETTLED.has(i.state))) next = TRACKING_FAST_MS;
      } catch (err: unknown) {
        if (stopped) return;
        setError(err instanceof Error ? err.message : String(err));
      }
      if (!stopped) timer = setTimeout(tick, next);
    };
    void tick();
    return () => {
      stopped = true;
      if (timer) clearTimeout(timer);
    };
  }, [key]);
  return { data, error };
}

// The catalog (every registered Hyperliquid perpetual), read once per page
// and re-read after an add enabled one.
let catalogPromise: Promise<ApiInstrument[]> | null = null;
let catalogVersion = -1;

export function resetCatalogForTests(): void {
  catalogPromise = null;
  catalogVersion = -1;
}

export function useHyperliquidCatalog(): {
  data: ApiInstrument[] | undefined;
  loading: boolean;
  error: string | undefined;
  retry: () => void;
} {
  const { version } = useWatchlistStore();
  const [state, setState] = useState<{ data?: ApiInstrument[]; loading: boolean; error?: string }>({ loading: true });
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let live = true;
    if (!catalogPromise || catalogVersion !== version) {
      catalogPromise = listHyperliquidCatalog();
      catalogVersion = version;
    }
    const p = catalogPromise;
    setState((s) => ({ ...s, loading: s.data === undefined, error: undefined }));
    p.then(
      (data) => live && setState({ data, loading: false }),
      (err: unknown) => {
        if (catalogPromise === p) catalogPromise = null; // the next try reads again
        if (live) setState({ loading: false, error: err instanceof Error ? err.message : String(err) });
      },
    );
    return () => {
      live = false;
    };
  }, [version, attempt]);
  const retry = useCallback(() => setAttempt((a) => a + 1), []);
  return { data: state.data, loading: state.loading, error: state.error, retry };
}
