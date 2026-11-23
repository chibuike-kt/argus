import { useState, type ReactNode } from "react";
import { Plus, Columns, DotsThree, CaretDown, CircleNotch, Trash, DotsSixVertical } from "@phosphor-icons/react";
import { useSearchParams } from "react-router-dom";
import { getWatchlistStore, useWatchlistStore } from "@/lib/watchlist-store";
import { useAnalysisWorkspace } from "./analysis-workspace";
import { CoinIcon } from "@/components/ui/coin-icon";
import { useInstruments, useWatchlist } from "@/api/hooks";
import { LiveFigure } from "@/components/ui/live-figure";
import { LivePrice } from "@/components/ui/live-price";
import { feedLine, liveReason } from "@/lib/figure-reasons";
import { useLiveStats, type LiveStatsEntry, type LiveStatsSnapshot } from "@/lib/live-stats";
import { useNow } from "@/lib/use-now";
import { formatDecimal, formatPercent, toneClass } from "@/lib/decimal-display";
import { preferences, usePreferences } from "@/lib/preferences";
import { cn } from "@/lib/cn";
import type { ApiWatchlistItemTracking } from "@/api/types";
import { useHyperliquidCatalog, useWatchlistTracking } from "@/lib/use-watchlist-tracking";
import { useSymbolSearch, WATCHLIST_MAX_ITEMS } from "./symbol-search";

const COLS = "grid grid-cols-[1.3fr_1fr_0.9fr_0.8fr] items-center gap-2";

function IconBtn({ children }: { children: ReactNode }) {
  return (
    <button className="focus-ring grid size-7 place-items-center rounded-md transition-colors hover:bg-white/5 hover:text-fg">
      {children}
    </button>
  );
}

// Row renders one watched instrument's live figures from the page's one
// stats source (Phase 3): the server's decimal strings as sent
// (thousands separators only, never a number conversion). Last is the
// shared <LivePrice>; Chg and Chg% are coloured by their sign. A dash carries its reason on
// hover, and the row's hover names the feed state in plain words.
//
// The trash button (fix/chart-interaction, item 3) shows on row hover or
// keyboard focus and removes the row at once, with an undo toast.
function Row({
  symbol,
  entry,
  snapshot,
  now,
  onRemove,
  tracking,
  onMove,
  drag,
}: {
  symbol: string;
  entry: LiveStatsEntry | undefined;
  snapshot: LiveStatsSnapshot;
  now: number;
  onRemove: () => void;
  tracking?: ApiWatchlistItemTracking;
  // Keyboard reorder: Alt+ArrowUp / Alt+ArrowDown on the row.
  onMove: (delta: -1 | 1) => void;
  drag: {
    onDragStart: (e: React.DragEvent) => void;
    onDragOver: (e: React.DragEvent) => void;
    onDrop: (e: React.DragEvent) => void;
    onDragEnd: () => void;
    over: boolean;
  };
}) {
  const f = entry?.figures ?? null;
  const tone = toneClass(f?.change_24h);
  const feed = feedLine(entry, snapshot, now);
  const name = symbol.replace(".P", "");
  const badge = trackingBadge(tracking);
  return (
    <div
      className={cn("group relative", drag.over && "rounded-lg ring-1 ring-accent/60")}
      draggable
      onDragStart={drag.onDragStart}
      onDragOver={drag.onDragOver}
      onDrop={drag.onDrop}
      onDragEnd={drag.onDragEnd}
      data-testid={`watchlist-item-${symbol}`}
    >
      <button
        data-testid={`watchlist-row-${symbol}`}
        title={`${name}: ${feed.text}${badge ? ` · ${badge.title}` : ""}. Drag, or Alt+Up and Alt+Down, to reorder.`}
        aria-keyshortcuts="Alt+ArrowUp Alt+ArrowDown"
        onKeyDown={(e) => {
          if (e.altKey && (e.key === "ArrowUp" || e.key === "ArrowDown")) {
            e.preventDefault();
            onMove(e.key === "ArrowUp" ? -1 : 1);
          }
        }}
        className={cn(
          "focus-ring w-full rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-white/5",
          COLS,
        )}
      >
        <span className="flex items-center gap-2 overflow-hidden">
          <DotsSixVertical className="size-3 shrink-0 cursor-grab text-fg-subtle opacity-0 group-hover:opacity-100" aria-hidden />
          <CoinIcon symbol={symbol} size={20} />
          <span className="truncate text-[13px] font-medium">{symbol.replace(".P", "")}</span>
          {badge && (
            <span
              data-testid={`watchlist-state-${symbol}`}
              title={badge.title}
              className={cn("shrink-0 rounded px-1 py-px text-[9px] font-semibold uppercase tracking-wide", badge.cls)}
            >
              {badge.text}
            </span>
          )}
        </span>
        <span className={cn("truncate text-right text-[13px]", feed.quiet && "opacity-70")}>
          <LivePrice
            testId={`watchlist-last-${symbol}`}
            value={f?.last_price ?? null}
            text={f?.last_price != null ? formatDecimal(f.last_price) : null}
            reason={liveReason("last_price", entry, snapshot)}
            resetKey={symbol}
          />
        </span>
        <span className="truncate text-right text-[12px]">
          <LiveFigure
            testId={`watchlist-chg-${symbol}`}
            tone="sign"
            signTone={tone}
            text={f?.change_24h != null ? formatDecimal(f.change_24h, { signed: true }) : null}
            reason={liveReason("change_24h", entry, snapshot)}
            motion={entry?.motion.change_24h}
          />
        </span>
        <span className="truncate text-right text-[12px]">
          <LiveFigure
            testId={`watchlist-chgpct-${symbol}`}
            tone="sign"
            signTone={tone}
            text={f?.change_24h_pct != null ? formatPercent(f.change_24h_pct) : null}
            reason={liveReason("change_24h_pct", entry, snapshot)}
            motion={entry?.motion.change_24h_pct}
          />
        </span>
      </button>
      <button
        type="button"
        data-testid={`watchlist-remove-${symbol}`}
        aria-label={`Remove ${name} from the watchlist`}
        title={`Remove ${name} from the watchlist`}
        onClick={onRemove}
        className="focus-ring absolute right-1 top-1/2 grid size-6 -translate-y-1/2 place-items-center rounded-md bg-surface text-fg-subtle opacity-0 shadow-sm transition-opacity hover:bg-down/15 hover:text-down focus-visible:opacity-100 group-hover:opacity-100"
      >
        <Trash className="size-3.5" />
      </button>
    </div>
  );
}

// RemovalToasts: one undo toast per removal in its undo window, and the
// message when the server refused one (the row is back by then).
function RemovalToasts({ names }: { names: Map<number, string> }) {
  const wl = useWatchlistStore();
  const store = getWatchlistStore();
  if (wl.pending.length === 0 && !wl.failure) return null;
  return (
    <div
      className="shrink-0 space-y-1.5 border-t border-line p-2"
      aria-live="polite"
      data-testid="watchlist-toasts"
    >
      {wl.pending.map((id) => (
        <div
          key={id}
          className="flex items-center justify-between gap-2 rounded-lg bg-white/[0.06] px-2.5 py-1.5 text-[12px] text-fg-muted"
          data-testid={`watchlist-undo-toast-${id}`}
        >
          <span>Removed {names.get(id) ?? "instrument"} from the watchlist</span>
          <button
            type="button"
            className="focus-ring rounded px-1.5 py-0.5 font-medium text-fg hover:bg-white/10"
            onClick={() => store.undo(id)}
          >
            Undo
          </button>
        </div>
      ))}
      {wl.failure && (
        <div
          role="alert"
          className="flex items-center justify-between gap-2 rounded-lg bg-down/10 px-2.5 py-1.5 text-[12px] text-down"
          data-testid={wl.failure.action === "add" ? "watchlist-add-failed" : wl.failure.action === "reorder" ? "watchlist-reorder-failed" : "watchlist-remove-failed"}
        >
          <span>{failureWords(wl.failure, names.get(wl.failure.id))}</span>
          <button
            type="button"
            className="focus-ring rounded px-1.5 py-0.5 font-medium hover:bg-white/10"
            onClick={() => store.dismissFailure()}
          >
            Dismiss
          </button>
        </div>
      )}
    </div>
  );
}

// nextAfterRemoval picks the chart instrument after the selected one is
// removed from the watchlist: the row that takes its place (the next
// one), else the one above it, else none (the chart stays where it is --
// the instrument is still enabled and viewable, just not watched).
// failureWords says what failed and what the list did about it.
export function failureWords(f: { message: string; action?: string; code?: string }, name: string | undefined): string {
  const who = name ?? "the instrument";
  switch (f.action) {
    case "add":
      return `Could not add ${who}: ${f.message}. It was taken off the list again.`;
    case "reorder":
      return f.code === "watchlist_changed"
        ? "The watchlist changed in another tab; it was reloaded. Reorder again if needed."
        : `Could not save the new order: ${f.message}. The previous order is back.`;
    default:
      return `Could not remove ${who}: ${f.message}. It is back on the list.`;
  }
}

// trackingBadge is the row's ingestion state in a word (Phase 4.1a): nothing
// while it is live, the state and its reason otherwise.
export function trackingBadge(t: ApiWatchlistItemTracking | undefined): { text: string; title: string; cls: string } | null {
  if (!t) return null;
  if (!t.tracked) {
    return { text: "Waiting", title: t.reason || "Over the system cap of tracked symbols", cls: "bg-white/10 text-fg-muted" };
  }
  const reason = t.reason ? `: ${t.reason}` : "";
  switch (t.state) {
    case "live":
    case undefined:
    case null:
      return t.state === "live" ? null : { text: "Starting", title: "Waiting for the worker to pick it up", cls: "bg-white/10 text-fg-muted" };
    case "waiting":
      return { text: "Waiting", title: `Waiting${reason}`, cls: "bg-white/10 text-fg-muted" };
    case "loading_history":
      return { text: "Loading history", title: "Loading its price history", cls: "bg-accent/20 text-accent" };
    case "retrying":
      return { text: "Retrying", title: `Retrying${t.attempt ? ` (attempt ${t.attempt})` : ""}${reason}`, cls: "bg-prov-r/15 text-prov-r" };
    case "failed":
      return { text: "Failed", title: `Failed${reason}`, cls: "bg-down/15 text-down" };
    case "needs_attention":
      return { text: "Needs attention", title: `Needs attention${reason}`, cls: "bg-down/15 text-down" };
    case "stopped":
      return { text: "Stopped", title: `Stopped${reason}`, cls: "bg-white/10 text-fg-muted" };
  }
  return null;
}

// moveItem returns ids with the item at index moved by delta (clamped);
// unchanged when it cannot move.
export function moveItem(ids: readonly number[], index: number, delta: number): number[] {
  const to = Math.max(0, Math.min(ids.length - 1, index + delta));
  if (index < 0 || index >= ids.length || to === index) return [...ids];
  const out = [...ids];
  const [it] = out.splice(index, 1);
  out.splice(to, 0, it);
  return out;
}

export function nextAfterRemoval(visible: readonly number[], removed: number): number | undefined {
  const i = visible.indexOf(removed);
  if (i < 0) return undefined;
  return visible[i + 1] ?? visible[i - 1];
}

// A group's collapsed state is a preference (D1), keyed by its title.
function Group({ title, children }: { title: string; children: ReactNode }) {
  const collapsed = usePreferences().watchlist_groups_collapsed?.[title] ?? false;
  const open = !collapsed;
  const setOpen = (o: boolean) => preferences.setEntry("watchlist_groups_collapsed", title, !o);
  return (
    <div>
      <div className="flex items-center justify-between px-2">
        <button
          onClick={() => setOpen(!open)}
          className="focus-ring flex items-center gap-1 py-1.5 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle transition-colors hover:text-fg-muted"
        >
          <CaretDown className={cn("size-3 transition-transform", !open && "-rotate-90")} />
          {title}
        </button>
      </div>
      {open && <div className="space-y-0.5">{children}</div>}
    </div>
  );
}

export function Watchlist() {
  const {
    data: instruments,
    loading: instrumentsLoading,
    error: instrumentsError,
  } = useInstruments();
  // The signed-in user's own saved instrument ids, in their saved
  // order (web light auth redesign brief, Part 3) -- this panel no
  // longer shows every registered instrument, only the ones onboarding
  // (or /account later) actually saved.
  const { data: watchlistIds, loading: watchlistLoading, error: watchlistError } = useWatchlist();
  const loading = instrumentsLoading || watchlistLoading;
  const error = instrumentsError ?? watchlistError;

  // The catalog too: a symbol an add has just enabled is not in the
  // enabled list this page read at load.
  const catalog = useHyperliquidCatalog();
  const byId = new Map([...(catalog.data ?? []), ...(instruments ?? [])].map((inst) => [inst.id, inst]));
  const { data: tracking } = useWatchlistTracking();
  const trackingById = new Map((tracking?.items ?? []).map((t) => [t.instrument_id, t]));
  const search = useSymbolSearch();
  const [dragId, setDragId] = useState<number | null>(null);
  const [overId, setOverId] = useState<number | null>(null);
  const watched = (watchlistIds ?? [])
    .map((id) => byId.get(id))
    .filter((inst): inst is NonNullable<typeof inst> => inst !== undefined);

  // Last, Chg and Chg% for every watched instrument from the page's one
  // live stats source: one GET /instruments/stats for the whole list,
  // then ONE multiplexed subscribe_stats on the socket (never one
  // subscription per row). D3: Last is the forming candle's close, Chg
  // is against the closed candle exactly 24h before the current minute.
  // (useLiveStats keys its subscription on the ids' joined value, so a
  // new array with the same ids each render changes nothing.)
  const snapshot = useLiveStats(watched.map((i) => i.id));
  const now = useNow(1000);
  const { instrument: selected } = useAnalysisWorkspace();
  const [, setSearchParams] = useSearchParams();
  const names = new Map([...byId.values()].map((i) => [i.id, i.native_symbol.replace(".P", "")]));
  const visibleIds = watched.map((i) => i.id);

  function move(index: number, delta: -1 | 1) {
    const next = moveItem(visibleIds, index, delta);
    if (next.join() !== visibleIds.join()) void getWatchlistStore().reorder(next);
    // Keep keyboard focus on the moved row.
    const sym = watched[index]?.native_symbol;
    if (sym) requestAnimationFrame(() => document.querySelector<HTMLElement>(`[data-testid="watchlist-row-${sym}"]`)?.focus());
  }

  function dropOn(targetId: number) {
    if (dragId === null || dragId === targetId) return;
    const from = visibleIds.indexOf(dragId);
    const to = visibleIds.indexOf(targetId);
    if (from < 0 || to < 0) return;
    void getWatchlistStore().reorder(moveItem(visibleIds, from, to - from));
  }

  function remove(id: number) {
    if (selected?.id === id) {
      const next = nextAfterRemoval(
        watched.map((i) => i.id),
        id,
      );
      if (next !== undefined) setSearchParams({ instrumentId: String(next) });
    }
    getWatchlistStore().remove(id);
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* title row: pinned */}
      <div className="flex shrink-0 items-center justify-between border-b border-line px-3 py-2.5">
        <button className="focus-ring flex items-center gap-1 text-sm font-semibold">
          Watchlist <CaretDown className="size-3.5 text-fg-subtle" />
        </button>
        <div className="flex items-center gap-0.5 text-fg-subtle">
          <span className="mr-1 text-[11px]" data-testid="watchlist-count" title="Symbols on your watchlist, of the maximum">
            {watchlistIds?.length ?? 0} of {WATCHLIST_MAX_ITEMS}
          </span>
          <button
            type="button"
            data-testid="watchlist-add"
            onClick={search.open}
            aria-label="Add a symbol (Ctrl+K)"
            title="Add a symbol (Ctrl+K)"
            className="focus-ring grid size-7 place-items-center rounded-md transition-colors hover:bg-white/5 hover:text-fg"
          >
            <Plus className="size-4" />
          </button>
          <IconBtn>
            <Columns className="size-4" />
          </IconBtn>
          <IconBtn>
            <DotsThree className="size-4" />
          </IconBtn>
        </div>
      </div>

      {/* column header: pinned */}
      <div
        className={cn(
          "shrink-0 border-b border-line px-2 py-1.5 text-[11px] font-medium text-fg-subtle",
          COLS,
        )}
      >
        <span>Symbol</span>
        <span className="text-right">Last</span>
        <span className="text-right">Chg</span>
        <span className="text-right">Chg%</span>
      </div>

      {/* rows: the only scroll region */}
      <div className="min-h-0 flex-1 overflow-y-auto py-1">
        <Group title="Crypto">
          {loading && (
            <div className="flex items-center gap-2 px-2 py-2 text-[12px] text-fg-subtle">
              <CircleNotch className="size-3.5 animate-spin" />
              Loading…
            </div>
          )}
          {error && <div className="px-2 py-2 text-[12px] text-down">{error.message}</div>}
          {!loading &&
            !error &&
            watched.map((inst, index) => (
              <Row
                key={inst.id}
                symbol={inst.native_symbol}
                entry={snapshot.entries.get(inst.id)}
                snapshot={snapshot}
                now={now}
                onRemove={() => remove(inst.id)}
                tracking={trackingById.get(inst.id)}
                onMove={(d) => move(index, d)}
                drag={{
                  onDragStart: (e) => {
                    setDragId(inst.id);
                    e.dataTransfer.effectAllowed = "move";
                    e.dataTransfer.setData("text/plain", String(inst.id));
                  },
                  onDragOver: (e) => {
                    if (dragId === null) return;
                    e.preventDefault();
                    if (overId !== inst.id) setOverId(inst.id);
                  },
                  onDrop: (e) => {
                    e.preventDefault();
                    dropOn(inst.id);
                    setDragId(null);
                    setOverId(null);
                  },
                  onDragEnd: () => {
                    setDragId(null);
                    setOverId(null);
                  },
                  over: overId === inst.id && dragId !== inst.id,
                }}
              />
            ))}
          {!loading && !error && watched.length === 0 && (
            <div className="flex flex-col items-start gap-2 px-2 py-3 text-[12px] text-fg-subtle" data-testid="watchlist-empty">
              Your watchlist is empty.
              <button
                type="button"
                onClick={search.open}
                className="focus-ring rounded-md bg-accent px-3 py-1.5 text-[12px] font-semibold text-accent-foreground"
              >
                Add symbols
              </button>
            </div>
          )}
        </Group>
      </div>
      <RemovalToasts names={names} />
    </div>
  );
}
