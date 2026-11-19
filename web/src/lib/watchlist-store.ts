// watchlist-store.ts: the page's one copy of the signed-in user's
// watchlist (ids in saved order), shared by the watchlist panel and the
// analysis workspace (which picks the chart's fallback instrument from
// it), so a removal shows everywhere at once.
//
// Removal (fix/chart-interaction, item 3) is optimistic with undo:
//   1. remove(id): the row disappears at once, and an undo window of
//      UNDO_WINDOW_MS opens (the toast);
//   2. undo(id) inside the window: the row comes back; nothing was sent;
//   3. the window ends (or the page is being left): DELETE
//      /watchlist/items/{id} is sent (keepalive on pagehide);
//   4. the server refuses or cannot be reached: the row comes back, in
//      its old place, with a message.
// Deferring the DELETE is what makes undo safe: undoing never needs a
// whole-list PUT, which could overwrite a change another tab made in the
// meantime (the reason D4 has item-level endpoints).
import { useSyncExternalStore } from "react";
import { getWatchlist } from "@/api/auth";
import { ApiClientError, apiBaseUrl, deleteRequest } from "@/api/client";

// [U] display value: how long the undo toast stays (owner: "about 5 seconds").
export const UNDO_WINDOW_MS = 5_000;


export interface WatchlistSnapshot {
  // The visible list: saved order, minus removals in their undo window.
  ids: number[] | undefined;
  loading: boolean;
  error: ApiClientError | undefined;
  // Removals whose undo window is open, oldest first. A removal that
  // fails comes back in its old place: the full list is kept, only
  // filtered by these.
  pending: number[];
  // A removal the server refused, for the message; cleared on dismiss.
  failure: { id: number; message: string } | null;
}

type Listener = () => void;

class WatchlistStore {
  private all: number[] | undefined;
  private loading = false;
  private loaded = false;
  private error: ApiClientError | undefined;
  private pending = new Map<number, ReturnType<typeof setTimeout>>();
  private failure: WatchlistSnapshot["failure"] = null;
  private listeners = new Set<Listener>();
  private snapshot: WatchlistSnapshot = { ids: undefined, loading: false, error: undefined, pending: [], failure: null };
  private onPageHide = () => this.flushAll(true);

  subscribe = (l: Listener): (() => void) => {
    this.listeners.add(l);
    if (this.listeners.size === 1 && typeof window !== "undefined") window.addEventListener("pagehide", this.onPageHide);
    if (!this.loaded && !this.loading) void this.load();
    return () => {
      this.listeners.delete(l);
      if (this.listeners.size === 0 && typeof window !== "undefined") window.removeEventListener("pagehide", this.onPageHide);
    };
  };

  getSnapshot = (): WatchlistSnapshot => this.snapshot;

  async load(): Promise<void> {
    this.loading = true;
    this.emit();
    try {
      const items = await getWatchlist();
      this.all = items.map((i) => i.instrument_id);
      this.error = undefined;
      this.loaded = true;
    } catch (err: unknown) {
      this.error = err instanceof ApiClientError ? err : new ApiClientError(String(err), 0);
    } finally {
      this.loading = false;
      this.emit();
    }
  }

  // remove hides id at once and opens its undo window.
  remove(id: number): void {
    if (!this.all || this.pending.has(id)) return;
    if (!this.all.includes(id)) return;
    this.pending.set(id, setTimeout(() => void this.commit(id), UNDO_WINDOW_MS));
    this.failure = null;
    this.emit();
  }

  // undo puts id back if its window is still open; nothing was sent.
  undo(id: number): void {
    const timer = this.pending.get(id);
    if (timer === undefined) return;
    clearTimeout(timer);
    this.pending.delete(id);
    this.emit();
  }

  dismissFailure(): void {
    this.failure = null;
    this.emit();
  }

  // commit sends the DELETE; on failure the row comes back in place.
  private async commit(id: number, keepalive = false): Promise<void> {
    const timer = this.pending.get(id);
    if (timer === undefined) return;
    clearTimeout(timer);
    try {
      if (keepalive) {
        // The page is going away: a plain fetch with keepalive outlives it.
        await fetch(`${apiBaseUrl()}/watchlist/items/${id}`, {
          method: "DELETE",
          keepalive: true,
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
        });
      } else {
        await deleteRequest(`/watchlist/items/${id}`);
      }
      this.pending.delete(id);
      if (this.all) this.all = this.all.filter((x) => x !== id);
    } catch (err: unknown) {
      this.pending.delete(id);
      const message = err instanceof Error ? err.message : String(err);
      this.failure = { id, message };
    }
    this.emit();
  }

  private flushAll(keepalive: boolean): void {
    for (const id of [...this.pending.keys()]) void this.commit(id, keepalive);
  }

  private emit(): void {
    const ids = this.all?.filter((id) => !this.pending.has(id));
    const pending = [...this.pending.keys()];
    this.snapshot = { ids, loading: this.loading, error: this.error, pending, failure: this.failure };
    for (const l of this.listeners) l();
  }
}

let store: WatchlistStore | null = null;

export function getWatchlistStore(): WatchlistStore {
  if (!store) store = new WatchlistStore();
  return store;
}

// resetWatchlistStoreForTests gives each test its own store.
export function resetWatchlistStoreForTests(): void {
  store = null;
}

export function useWatchlistStore(): WatchlistSnapshot {
  const s = getWatchlistStore();
  return useSyncExternalStore(s.subscribe, s.getSnapshot, s.getSnapshot);
}
