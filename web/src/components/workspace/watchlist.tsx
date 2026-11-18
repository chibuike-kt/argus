import { type ReactNode } from "react";
import { Plus, Columns, DotsThree, CaretDown, CircleNotch } from "@phosphor-icons/react";
import { CoinIcon } from "@/components/ui/coin-icon";
import { useInstruments, useWatchlist } from "@/api/hooks";
import { useInstrumentStats } from "@/api/use-instrument-stats";
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

// Row renders the decimal strings GET /instruments/stats sent, as sent
// (thousands separators only -- no number conversion, real-panel-data
// brief rule 10). null/undefined is an em dash, never a fabricated 0.
function Row({
  symbol,
  last,
  change,
  changePct,
}: {
  symbol: string;
  last?: string | null;
  change?: string | null;
  changePct?: string | null;
}) {
  const tone = toneClass(change);
  return (
    <button
      data-testid={`watchlist-row-${symbol}`}
      className={cn(
        "focus-ring w-full rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-white/5",
        COLS,
      )}
    >
      <span className="flex items-center gap-2 overflow-hidden">
        <CoinIcon symbol={symbol} size={20} />
        <span className="truncate text-[13px] font-medium">{symbol.replace(".P", "")}</span>
      </span>
      <span className="tnum truncate text-right text-[13px]">{formatDecimal(last)}</span>
      <span className={cn("tnum truncate text-right text-[12px]", tone)}>{formatDecimal(change, { signed: true })}</span>
      <span className={cn("tnum truncate text-right text-[12px]", tone)}>{formatPercent(changePct)}</span>
    </button>
  );
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
  const { data: instruments, loading: instrumentsLoading, error: instrumentsError } =
    useInstruments();
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

  // Last, Chg and Chg% for every watched instrument from ONE
  // GET /instruments/stats request (real-panel-data brief), so all three
  // come from the same server snapshot. Chg is against the closed
  // 1-minute candle exactly 24h before the latest one; an em dash when
  // that candle does not exist. Refreshed on the hook's own timer.
  const stats = useInstrumentStats(watched.map((i) => i.id)).data;

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
            watched.map((inst) => {
              const row = stats?.get(inst.id);
              return (
                <Row
                  key={inst.id}
                  symbol={inst.native_symbol}
                  last={row?.last_close}
                  change={row?.change_24h}
                  changePct={row?.change_24h_pct}
                />
              );
            })}
          {!loading && !error && watched.length === 0 && (
            <div className="px-2 py-2 text-[12px] text-fg-subtle">
              Your watchlist is empty. Add a pair from onboarding or account settings.
            </div>
          )}
        </Group>
      </div>
    </div>
  );
}
