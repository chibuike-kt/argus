import { useCallback, useEffect, useState } from "react";
import { getEngineStatus, listActivity } from "@/api/engine";
import type { ApiActivityItem, ApiActivityKind, ApiEngineStatus } from "@/api/types";
import { useWatchlist } from "@/api/hooks";
import { formatAge } from "@/lib/decimal-display";
import { useNow } from "@/lib/use-now";
import { cn } from "@/lib/cn";
import { poiWords, utcTime } from "@/components/chart/setup-layers";
import { useAnalysisWorkspace } from "./analysis-workspace";

// Phase 6.5 (D7): what the engine has really evaluated, and what it really
// recorded. Every figure comes from GET /engine/status (stored processing
// cursors) and GET /activity (stored setup events, structure events,
// resolved gaps). Nothing is scripted, estimated or animated to look busy.

// [U] display values: how often each is re-read while the tab is visible.
export const ENGINE_STATUS_REFRESH_MS = 15_000;
export const ACTIVITY_REFRESH_MS = 30_000;
const ACTIVITY_PAGE = 20;

function usePolled<T>(load: (signal: AbortSignal) => Promise<T>, everyMs: number, deps: React.DependencyList) {
  const [data, setData] = useState<T | undefined>();
  const [error, setError] = useState<string | undefined>();
  useEffect(() => {
    const controller = new AbortController();
    const run = () =>
      load(controller.signal)
        .then((d) => {
          if (!controller.signal.aborted) {
            setData(d);
            setError(undefined);
          }
        })
        .catch((e: unknown) => {
          if (!controller.signal.aborted) setError(e instanceof Error ? e.message : String(e));
        });
    void run();
    const id = setInterval(() => {
      if (document.visibilityState === "visible") void run();
    }, everyMs);
    return () => {
      controller.abort();
      clearInterval(id);
    };
  }, deps);
  return { data, error };
}

// statusSummary turns the cursors into the header line's figures.
export function statusSummary(st: ApiEngineStatus) {
  const analysed = st.instruments.filter((i) => i.analysed && i.evaluated_through);
  const through = analysed.map((i) => i.evaluated_through!).sort()[0] ?? null;
  const behind = analysed.reduce<number | null>((m, i) => (i.behind_seconds === null ? m : Math.max(m ?? 0, i.behind_seconds)), null);
  const lastEvaluated = analysed.map((i) => i.last_evaluated_at!).filter(Boolean).sort().pop() ?? null;
  return { analysed: analysed.length, notAnalysed: st.instruments.length - analysed.length, through, behind, lastEvaluated };
}

function seconds(n: number): string {
  if (n < 120) return `${n}s`;
  if (n < 7200) return `${Math.floor(n / 60)}m`;
  return `${Math.floor(n / 3600)}h`;
}

// EngineStatusLine: "3 instruments evaluated · through 01:46 UTC · 0s behind ·
// last evaluated 12s ago", every value read from the stored cursors.
export function EngineStatusLine() {
  const now = useNow(1000);
  const { data, error } = usePolled((signal) => getEngineStatus({ signal }), ENGINE_STATUS_REFRESH_MS, []);
  if (error && !data) {
    return (
      <p className="text-[11px] text-fg-subtle" data-testid="engine-status">
        Engine status unavailable: {error}
      </p>
    );
  }
  if (!data) return <p className="text-[11px] text-fg-subtle" data-testid="engine-status">Reading engine status…</p>;
  const s = statusSummary(data);
  const title = data.instruments
    .map((i) =>
      i.analysed
        ? `${i.native_symbol}: evaluated through ${i.evaluated_through ? utcTime(i.evaluated_through) : "?"}, ${i.closed_candles_behind} closed candle(s) not yet evaluated`
        : `${i.native_symbol}: not evaluated yet`,
    )
    .join("\n");
  return (
    <p className="tnum text-[11px] leading-snug text-fg-muted" data-testid="engine-status" title={title}>
      {s.analysed === 0 ? (
        "No instrument evaluated yet"
      ) : (
        <>
          {s.analysed} instrument{s.analysed === 1 ? "" : "s"} evaluated
          {s.through && <> · through {utcTime(s.through).slice(11)}</>}
          {s.behind !== null && <> · {seconds(s.behind)} behind</>}
          {s.lastEvaluated && <> · last evaluated {formatAge(now - Date.parse(s.lastEvaluated))}</>}
        </>
      )}
      {s.notAnalysed > 0 && <> · {s.notAnalysed} not evaluated yet</>}
    </p>
  );
}

// activityWords is one feed row in plain words, templated from its fields.
export function activityWords(it: ApiActivityItem, symbol: string): string {
  switch (it.kind) {
    case "setup": {
      const what = `${it.direction ?? ""} ${poiWords(it.poi_type ?? "")}`.trim();
      const verb =
        it.detail === "detected"
          ? "found"
          : it.detail === "amendment"
            ? "amended after a candle correction"
            : it.detail === "invalidated"
              ? "invalidated"
              : `became ${it.detail}`;
      return `${symbol} setup #${it.setup_id} (${what}, ${it.timeframe}) ${verb}`;
    }
    case "structure": {
      const t = it.detail === "bos" ? "BOS" : it.detail === "choch" ? "CHoCH" : "direction established";
      return `${symbol} ${t}${it.direction ? `, ${it.direction}` : ""} (${it.timeframe})`;
    }
    case "gap":
      return `${symbol} gap ${utcTime(it.event_time).slice(11)} to ${it.gap_end ? utcTime(it.gap_end).slice(11) : "?"} resolved: ${it.detail.replace(/_/g, " ")}`;
  }
}

// ActivityFeed lists what ARGUS recorded for the watchlist, newest first.
export function ActivityFeed() {
  const { instruments, selectSetup } = useAnalysisWorkspace();
  const watchlist = useWatchlist().data;
  const [structure, setStructure] = useState(false);
  const ids = watchlist ?? [];
  const kinds: ApiActivityKind[] = structure ? ["setup", "structure", "gap"] : ["setup", "gap"];
  const key = `${ids.join(",")}|${kinds.join(",")}`;
  const { data, error } = usePolled(
    (signal) =>
      ids.length === 0 ? Promise.resolve({ items: [], next_cursor: null }) : listActivity({ instrumentIds: ids, kinds, limit: ACTIVITY_PAGE }, { signal }),
    ACTIVITY_REFRESH_MS,
    [key],
  );
  const [older, setOlder] = useState<ApiActivityItem[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  useEffect(() => {
    setOlder([]);
    setCursor(data?.next_cursor ?? null);
  }, [data, key]);
  const loadMore = useCallback(() => {
    if (!cursor) return;
    void listActivity({ instrumentIds: ids, kinds, cursor, limit: ACTIVITY_PAGE }).then((p) => {
      setOlder((o) => [...o, ...p.items]);
      setCursor(p.next_cursor);
    });
  }, [cursor, key]);

  const symbol = (id: number) => instruments.find((i) => i.id === id)?.native_symbol ?? `#${id}`;
  const items = [...(data?.items ?? []), ...older];
  return (
    <div data-testid="activity-feed">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-sm font-semibold">Activity</span>
        <label className="flex items-center gap-1 text-[11px] text-fg-subtle">
          <input type="checkbox" checked={structure} onChange={(e) => setStructure(e.target.checked)} data-testid="activity-structure" />
          Structure events
        </label>
      </div>
      {error && !data && <p className="text-[12px] text-down">Could not load activity: {error}</p>}
      {!data && !error && <p className="text-[12px] text-fg-subtle">Loading…</p>}
      {data && items.length === 0 && (
        <p className="text-[12px] text-fg-subtle">
          {ids.length === 0 ? "Add an instrument to your watchlist to see its activity." : "Nothing recorded yet for your watchlist."}
        </p>
      )}
      <ul className="space-y-1.5">
        {items.map((it) => (
          <li key={`${it.kind}:${it.id}`} className="text-[12px] leading-snug" data-testid="activity-item">
            <button
              type="button"
              disabled={it.kind !== "setup" || !it.setup_id}
              onClick={() => it.setup_id && selectSetup(it.setup_id)}
              className={cn("focus-ring w-full rounded px-1 text-left", it.kind === "setup" && "hover:bg-white/[0.04]")}
            >
              <span className="text-fg">{activityWords(it, symbol(it.instrument_id))}</span>
              <span className="tnum block text-[11px] text-fg-subtle">
                {it.kind === "gap" ? "resolved" : "candle"} {utcTime(it.kind === "gap" ? it.at : it.event_time)} · recorded {utcTime(it.at)}
                {it.found_mode && it.found_mode !== "unknown" && it.detail === "detected" ? ` · ${it.found_mode}` : ""}
              </span>
            </button>
          </li>
        ))}
      </ul>
      {cursor && (
        <button type="button" onClick={loadMore} className="focus-ring mt-2 text-[11px] font-medium text-fg-muted hover:text-fg" data-testid="activity-more">
          Older
        </button>
      )}
    </div>
  );
}
