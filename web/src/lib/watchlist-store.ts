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
import { addWatchlistItem, reorderWatchlist } from "@/api/watchlist";
import { refreshLiveStats } from "./live-stats";

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
  // The last failed change, kept until dismissed. action says which: a
  // remove (the row came back), an add (the row was taken out again) or a
  // reorder (the order was restored or reloaded). code is the server's
  // error code when there is one (watchlist_full, tracked_cap_reached, ...).
  failure: { id: number; message: string; action?: "remove" | "add" | "reorder"; code?: string } | null;
  // Adds still waiting for the server (their rows are already shown).
  adding: number[];
  // Bumped after every add the server confirmed, so pages that cache the
  // enabled instrument list can re-read it (an add may enable one, D15.1).
  version: number;
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
  private adding = new Set<number>();
  private version = 0;
  private snapshot: WatchlistSnapshot = { ids: undefined, loading: false, error: undefined, pending: [], failure: null, adding: [], version: 0 };
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

  // add appends id at once (optimistic) and asks the server; on failure
  // only this row is taken out again, so a change made meanwhile stays.
  // Adding a row still in its removal undo window is the undo.
  async add(id: number): Promise<boolean> {
    if (!this.all) return false;
    if (this.pending.has(id)) {
      this.undo(id);
      return true;
    }
    if (this.all.includes(id) || this.adding.has(id)) return true;
    this.adding.add(id);
    this.all = [...this.all, id];
    this.failure = null;
    this.emit();
    try {
      const items = await addWatchlistItem(id);
      this.all = items.map((i) => i.instrument_id);
      this.version++;
      // The row showed before the server enabled the instrument; its live
      // figures were refused then, so ask again now.
      refreshLiveStats([id]);
      return true;
    } catch (err: unknown) {
      if (this.all) this.all = this.all.filter((x) => x !== id);
      this.failure = { id, message: errorMessage(err), action: "add", code: err instanceof ApiClientError ? err.code : undefined };
      return false;
    } finally {
      this.adding.delete(id);
      this.emit();
    }
  }

  // reorder shows the new order at once and saves it. Rows in their
  // removal undo window are still on the server's list, so they are sent
  // at the end. A refusal because the list changed elsewhere (409
  // watchlist_changed) reloads the server's list; any other failure puts
  // the previous order back.
  async reorder(visible: number[]): Promise<boolean> {
    if (!this.all) return false;
    const before = this.all;
    const pendingIds = before.filter((id) => this.pending.has(id));
    const next = [...visible, ...pendingIds];
    this.all = next;
    this.failure = null;
    this.emit();
    try {
      const items = await reorderWatchlist(next);
      this.all = items.map((i) => i.instrument_id);
      this.emit();
      return true;
    } catch (err: unknown) {
      const code = err instanceof ApiClientError ? err.code : undefined;
      this.failure = { id: visible[0] ?? 0, message: errorMessage(err), action: "reorder", code };
      if (code === "watchlist_changed") {
        await this.load();
      } else {
        this.all = before;
        this.emit();
      }
      return false;
    }
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
      this.failure = { id, message: errorMessage(err), action: "remove" };
    }
    this.emit();
  }

  private flushAll(keepalive: boolean): void {
    for (const id of [...this.pending.keys()]) void this.commit(id, keepalive);
  }

  private emit(): void {
    const ids = this.all?.filter((id) => !this.pending.has(id));
    const pending = [...this.pending.keys()];
    this.snapshot = {
      ids,
      loading: this.loading,
      error: this.error,
      pending,
      failure: this.failure,
      adding: [...this.adding],
      version: this.version,
    };
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

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
