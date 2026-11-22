import { createContext, useContext, useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { Check, CircleNotch, MagnifyingGlass, Plus } from "@phosphor-icons/react";
import type { ApiInstrument, ApiWatchlistTracking } from "@/api/types";
import { CoinIcon } from "@/components/ui/coin-icon";
import { cn } from "@/lib/cn";
import { getWatchlistStore, useWatchlistStore } from "@/lib/watchlist-store";
import { useHyperliquidCatalog, useWatchlistTracking } from "@/lib/use-watchlist-tracking";

// The symbol search dialog (Phase 4.1a): every Hyperliquid perpetual in the
// catalog, filtered as you type, with an Add button per row and a check on
// the ones already on your watchlist. Keyboard first: Ctrl+K (or the
// watchlist's plus button) opens it with the search box focused, the up and
// down arrows move, Enter adds, Esc closes and focus goes back where it was.
// The search runs client side over the catalog (about 180 rows).

// The per-user cap, ADR-015 (the server enforces it; this only explains a
// disabled Add before a request is made).
export const WATCHLIST_MAX_ITEMS = 50;

// filterCatalog keeps the perpetuals whose symbol contains query (any case),
// those starting with it first, then by symbol. An empty query keeps all.
export function filterCatalog(list: readonly ApiInstrument[], query: string): ApiInstrument[] {
  const q = query.trim().toUpperCase();
  if (!q) return [...list];
  const starts: ApiInstrument[] = [];
  const contains: ApiInstrument[] = [];
  for (const inst of list) {
    const s = inst.native_symbol.toUpperCase();
    if (s.startsWith(q)) starts.push(inst);
    else if (s.includes(q)) contains.push(inst);
  }
  return [...starts, ...contains];
}

export type AddAvailability = { kind: "added" } | { kind: "add" } | { kind: "blocked"; reason: string };

// addAvailability says what a row's button offers, and why not when it
// cannot add -- the same rules the server enforces, so a click never fails
// for a reason the dialog could have said first.
export function addAvailability(
  inst: ApiInstrument,
  ctx: { onList: ReadonlySet<number>; listCount: number; tracking: ApiWatchlistTracking | undefined },
): AddAvailability {
  if (ctx.onList.has(inst.id)) return { kind: "added" };
  if (ctx.listCount >= WATCHLIST_MAX_ITEMS) {
    return { kind: "blocked", reason: `Your watchlist is full (${WATCHLIST_MAX_ITEMS} of ${WATCHLIST_MAX_ITEMS}). Remove one first.` };
  }
  const t = ctx.tracking;
  if (!inst.enabled && t && !t.can_enable) return { kind: "blocked", reason: "Needs to be enabled by the operator." };
  if (t && !t.tracked_instrument_ids.includes(inst.id) && t.tracked_instrument_ids.length >= t.cap) {
    return {
      kind: "blocked",
      reason: `ARGUS tracks at most ${t.cap} symbols at once. Remove one from every watchlist to add another.`,
    };
  }
  return { kind: "add" };
}

const SearchCtx = createContext<{ open: () => void } | null>(null);

export function useSymbolSearch(): { open: () => void } {
  const ctx = useContext(SearchCtx);
  return ctx ?? { open: () => {} };
}

// SymbolSearchProvider owns the dialog and its Ctrl+K shortcut (Cmd+K on a
// Mac). The command palette moved to Ctrl+Shift+K.
export function SymbolSearchProvider({ children }: { children: ReactNode }) {
  const [isOpen, setOpen] = useState(false);
  const opener = useRef<HTMLElement | null>(null);
  const open = () => {
    opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setOpen(true);
  };
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "k") {
        e.preventDefault();
        opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
        setOpen(true);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  const close = () => {
    setOpen(false);
    opener.current?.focus?.();
  };
  return (
    <SearchCtx.Provider value={{ open }}>
      {children}
      {isOpen && <SymbolSearchDialog onClose={close} />}
    </SearchCtx.Provider>
  );
}

export function SymbolSearchDialog({ onClose }: { onClose: () => void }) {
  const catalog = useHyperliquidCatalog();
  const wl = useWatchlistStore();
  const { data: tracking } = useWatchlistTracking();
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const [announce, setAnnounce] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);
  const dialogRef = useRef<HTMLDivElement>(null);
  const listId = useId();

  const results = useMemo(() => filterCatalog(catalog.data ?? [], query), [catalog.data, query]);
  const onList = useMemo(() => new Set(wl.ids ?? []), [wl.ids]);
  const listCount = wl.ids?.length ?? 0;

  useEffect(() => inputRef.current?.focus(), []);
  useEffect(() => setActive(0), [query]);
  useEffect(() => {
    document.getElementById(`${listId}-opt-${active}`)?.scrollIntoView?.({ block: "nearest" });
  }, [active, listId]);

  async function act(inst: ApiInstrument) {
    const a = addAvailability(inst, { onList, listCount, tracking });
    if (a.kind === "blocked") {
      setAnnounce(`${inst.native_symbol}: ${a.reason}`);
      return;
    }
    if (a.kind === "added") {
      getWatchlistStore().remove(inst.id); // with the panel's undo toast
      setAnnounce(`Removed ${inst.native_symbol} from your watchlist`);
      return;
    }
    setAnnounce(`Adding ${inst.native_symbol}`);
    const ok = await getWatchlistStore().add(inst.id);
    const failure = getWatchlistStore().getSnapshot().failure;
    setAnnounce(ok ? `Added ${inst.native_symbol} to your watchlist` : `Could not add ${inst.native_symbol}: ${failure?.message ?? "unknown error"}`);
  }

  function onKeyDown(e: React.KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      onClose();
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((i) => Math.min(i + 1, Math.max(results.length - 1, 0)));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((i) => Math.max(i - 1, 0));
    } else if (e.key === "Enter" && e.target === inputRef.current) {
      e.preventDefault();
      const inst = results[active];
      if (inst) void act(inst);
    } else if (e.key === "Tab") {
      // Keep focus inside the dialog.
      const f = dialogRef.current?.querySelectorAll<HTMLElement>("input, button:not([disabled])");
      if (!f || f.length === 0) return;
      const first = f[0];
      const last = f[f.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    }
  }

  const trackedCount = tracking?.tracked_instrument_ids.length;
  return (
    <div className="fixed inset-0 z-[80] flex items-start justify-center bg-black/50 pt-[12vh]" onMouseDown={onClose}>
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-label="Add a symbol to your watchlist"
        data-testid="symbol-search"
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={onKeyDown}
        className="glass-strong flex max-h-[70vh] w-[560px] max-w-[92vw] flex-col overflow-hidden rounded-xl shadow-2xl"
      >
        <div className="flex items-center gap-2 border-b border-line px-3 py-2.5">
          <MagnifyingGlass className="size-4 text-fg-subtle" />
          <input
            ref={inputRef}
            data-testid="symbol-search-input"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search Hyperliquid perpetuals"
            aria-label="Search symbols"
            aria-controls={listId}
            aria-activedescendant={results[active] ? `${listId}-opt-${active}` : undefined}
            className="min-w-0 flex-1 bg-transparent text-[14px] text-fg outline-none placeholder:text-fg-subtle"
          />
          <span className="text-[11px] text-fg-subtle" data-testid="symbol-search-count">
            {listCount} of {WATCHLIST_MAX_ITEMS}
          </span>
        </div>

        <div id={listId} role="listbox" aria-label="Perpetuals" className="min-h-0 flex-1 overflow-y-auto py-1">
          {catalog.loading && (
            <div className="flex items-center gap-2 px-4 py-6 text-[13px] text-fg-subtle" data-testid="symbol-search-loading">
              <CircleNotch className="size-4 animate-spin" /> Loading the Hyperliquid catalog…
            </div>
          )}
          {catalog.error && !catalog.data && (
            <div className="px-4 py-6 text-[13px] text-down" data-testid="symbol-search-error">
              Could not load the catalog: {catalog.error}{" "}
              <button type="button" onClick={catalog.retry} className="focus-ring rounded font-medium text-fg underline">
                Retry
              </button>
            </div>
          )}
          {catalog.data && results.length === 0 && (
            <div className="px-4 py-6 text-[13px] text-fg-subtle" data-testid="symbol-search-empty">
              {catalog.data.length === 0
                ? "No perpetuals registered yet. The worker registers Hyperliquid's catalog when it starts."
                : `Nothing matches '${query.trim()}'. ARGUS lists Hyperliquid perpetuals only.`}
            </div>
          )}
          {results.map((inst, i) => {
            const a = addAvailability(inst, { onList, listCount, tracking });
            const busy = wl.adding.includes(inst.id);
            return (
              <div
                key={inst.id}
                id={`${listId}-opt-${i}`}
                role="option"
                aria-selected={i === active}
                data-testid={`symbol-search-row-${inst.native_symbol}`}
                onMouseEnter={() => setActive(i)}
                className={cn("flex items-center gap-3 px-3 py-2", i === active && "bg-white/[0.06]")}
              >
                <CoinIcon symbol={inst.native_symbol} size={22} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[13px] font-semibold text-fg">{inst.native_symbol}</span>
                  <span className="block truncate text-[11px] text-fg-subtle">
                    {inst.base_asset ?? inst.native_symbol} / {inst.quote_asset ?? "USD"}
                  </span>
                </span>
                <span className="rounded border border-line px-1.5 py-0.5 text-[10px] font-medium text-fg-muted">Perp</span>
                <span className="rounded border border-line px-1.5 py-0.5 text-[10px] font-medium text-fg-muted">Hyperliquid</span>
                {a.kind === "added" ? (
                  <button
                    type="button"
                    tabIndex={-1}
                    data-testid={`symbol-search-added-${inst.native_symbol}`}
                    aria-label={`${inst.native_symbol} is on your watchlist; remove it`}
                    title="On your watchlist. Click to remove (you can undo)."
                    onClick={() => void act(inst)}
                    className="focus-ring inline-flex w-[84px] items-center justify-center gap-1 rounded-md bg-up/15 px-2 py-1 text-[12px] font-medium text-up"
                  >
                    <Check className="size-3.5" weight="bold" /> Added
                  </button>
                ) : (
                  <button
                    type="button"
                    tabIndex={-1}
                    disabled={a.kind === "blocked" || busy}
                    data-testid={`symbol-search-add-${inst.native_symbol}`}
                    aria-label={a.kind === "blocked" ? `${inst.native_symbol}: ${a.reason}` : `Add ${inst.native_symbol} to your watchlist`}
                    title={a.kind === "blocked" ? a.reason : `Add ${inst.native_symbol}`}
                    onClick={() => void act(inst)}
                    className="focus-ring inline-flex w-[84px] items-center justify-center gap-1 rounded-md bg-accent px-2 py-1 text-[12px] font-semibold text-accent-foreground disabled:cursor-not-allowed disabled:bg-white/10 disabled:text-fg-subtle"
                  >
                    {busy ? <CircleNotch className="size-3.5 animate-spin" /> : <Plus className="size-3.5" weight="bold" />} Add
                  </button>
                )}
              </div>
            );
          })}
        </div>

        <div className="border-t border-line px-3 py-2 text-[11px] leading-snug text-fg-subtle">
          <span data-testid="symbol-search-cap">
            {tracking && trackedCount !== undefined ? `ARGUS is tracking ${trackedCount} of ${tracking.cap} symbols. ` : ""}
          </span>
          ↑ ↓ to move · Enter to add · Esc to close.{" "}
          {tracking?.can_enable ? "Adding a perpetual starts its history backfill and live data (private research use only)." : ""}
          <span className="sr-only" role="status" aria-live="polite" data-testid="symbol-search-status">
            {announce}
          </span>
        </div>
      </div>
    </div>
  );
}
