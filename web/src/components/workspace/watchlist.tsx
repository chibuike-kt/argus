import { type ReactNode } from "react";
import { Plus, Columns, DotsThree, CaretDown, CircleNotch, Trash } from "@phosphor-icons/react";
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
}: {
  symbol: string;
  entry: LiveStatsEntry | undefined;
  snapshot: LiveStatsSnapshot;
  now: number;
  onRemove: () => void;
}) {
  const f = entry?.figures ?? null;
  const tone = toneClass(f?.change_24h);
  const feed = feedLine(entry, snapshot, now);
  const name = symbol.replace(".P", "");
  return (
    <div className="group relative">
      <button
        data-testid={`watchlist-row-${symbol}`}
        title={`${name}: ${feed.text}`}
        className={cn(
          "focus-ring w-full rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-white/5",
          COLS,
        )}
      >
        <span className="flex items-center gap-2 overflow-hidden">
          <CoinIcon symbol={symbol} size={20} />
          <span className="truncate text-[13px] font-medium">{symbol.replace(".P", "")}</span>
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
          data-testid="watchlist-remove-failed"
        >
          <span>
            Could not remove {names.get(wl.failure.id) ?? "the instrument"}: {wl.failure.message}.
            It is back on the list.
          </span>
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

  const byId = new Map((instruments ?? []).map((inst) => [inst.id, inst]));
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
  const names = new Map((instruments ?? []).map((i) => [i.id, i.native_symbol.replace(".P", "")]));

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
          <IconBtn>
            <Plus className="size-4" />
          </IconBtn>
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
            watched.map((inst) => (
              <Row
                key={inst.id}
                symbol={inst.native_symbol}
                entry={snapshot.entries.get(inst.id)}
                snapshot={snapshot}
                now={now}
                onRemove={() => remove(inst.id)}
              />
            ))}
          {!loading && !error && watched.length === 0 && (
            <div className="px-2 py-2 text-[12px] text-fg-subtle">
              Your watchlist is empty. Add a pair from onboarding or account settings.
            </div>
          )}
        </Group>
      </div>
      <RemovalToasts names={names} />
    </div>
  );
}
