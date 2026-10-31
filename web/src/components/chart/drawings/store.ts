import { useMemo, useSyncExternalStore } from "react";
import { migrateDrawing, type Drawing } from "./types";

// Everything the store needs from a persistence backend. localStorage is the
// only implementation today; a server-backed sync swaps in here later
// without the store (or anything above it) changing shape.
export interface DrawingsPersistence {
  load(scopeKey: string): Drawing[] | null;
  save(scopeKey: string, drawings: Drawing[]): void;
}

const STORAGE_PREFIX = "argus:drawings:v1:";

export class LocalStorageDrawingsPersistence implements DrawingsPersistence {
  load(scopeKey: string): Drawing[] | null {
    try {
      const raw = window.localStorage.getItem(STORAGE_PREFIX + scopeKey);
      if (!raw) return null;
      const parsed = JSON.parse(raw);
      return Array.isArray(parsed) ? (parsed as Drawing[]) : null;
    } catch {
      // Corrupt or inaccessible storage is not fatal: start from empty.
      return null;
    }
  }
  save(scopeKey: string, drawings: Drawing[]): void {
    try {
      window.localStorage.setItem(STORAGE_PREFIX + scopeKey, JSON.stringify(drawings));
    } catch {
      // Storage full or disabled (private mode): drawings still work this
      // session, they just will not survive a reload.
    }
  }
}

interface State {
  order: string[];
  byId: Record<string, Drawing>;
  selectedId: string | null;
}

const EMPTY_STATE: State = { order: [], byId: {}, selectedId: null };

const MAX_HISTORY = 50;

/**
 * Owns one instrument+timeframe's drawings: the ordered id list (z-order,
 * last = front), undo/redo as full-state snapshots (drawing counts are small
 * -- a dozen-plus, not thousands -- so snapshotting is simpler than patches
 * and plenty fast), and persistence through a swappable adapter. Selection
 * lives here too (not in React state) so undo/redo and persistence do not
 * need to reach back into a component to know what is selected.
 */
export class DrawingsStore {
  private state: State = EMPTY_STATE;
  private undoStack: State[] = [];
  private redoStack: State[] = [];
  private listeners = new Set<() => void>();
  private scopeKey = "";

  constructor(private persistence: DrawingsPersistence) {}

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  private emit() {
    for (const l of this.listeners) l();
  }

  getSnapshot = (): State => this.state;

  // Switches which instrument+timeframe this store reflects: persists the
  // outgoing scope (in case a save was pending only in memory), loads the
  // incoming one, and resets undo/redo -- history does not cross scopes.
  setScope(scopeKey: string) {
    if (scopeKey === this.scopeKey) return;
    this.scopeKey = scopeKey;
    const loaded = (this.persistence.load(scopeKey) ?? []).map(migrateDrawing);
    this.state = {
      order: loaded.map((d) => d.id),
      byId: Object.fromEntries(loaded.map((d) => [d.id, d])),
      selectedId: null,
    };
    this.undoStack = [];
    this.redoStack = [];
    this.emit();
  }

  private pushHistory() {
    this.undoStack.push(this.state);
    if (this.undoStack.length > MAX_HISTORY) this.undoStack.shift();
    this.redoStack = [];
  }

  private commit(next: State, { history = true }: { history?: boolean } = {}) {
    if (history) this.pushHistory();
    this.state = next;
    this.persistence.save(this.scopeKey, this.list());
    this.emit();
  }

  list(): Drawing[] {
    return this.state.order.map((id) => this.state.byId[id]);
  }

  get(id: string): Drawing | undefined {
    return this.state.byId[id];
  }

  get selectedId(): string | null {
    return this.state.selectedId;
  }

  add(d: Drawing) {
    this.commit({
      order: [...this.state.order, d.id],
      byId: { ...this.state.byId, [d.id]: d },
      selectedId: d.id,
    });
  }

  // Shallow-merges patch into the drawing. Used for move, reshape, and style
  // changes alike -- they are all "replace some fields" from the store's
  // point of view; the semantic difference is only in what the caller passes.
  update(id: string, patch: Partial<Drawing>, opts?: { history?: boolean }) {
    const existing = this.state.byId[id];
    if (!existing) return;
    const updated = { ...existing, ...patch, updatedAt: Date.now() } as Drawing;
    this.commit({ ...this.state, byId: { ...this.state.byId, [id]: updated } }, opts);
  }

  remove(id: string) {
    if (!this.state.byId[id]) return;
    const byId = { ...this.state.byId };
    delete byId[id];
    this.commit({
      order: this.state.order.filter((x) => x !== id),
      byId,
      selectedId: this.state.selectedId === id ? null : this.state.selectedId,
    });
  }

  clear() {
    if (this.state.order.length === 0) return;
    this.commit({ order: [], byId: {}, selectedId: null });
  }

  select(id: string | null) {
    if (id === this.state.selectedId) return;
    // Selection alone is not an undoable edit.
    this.state = { ...this.state, selectedId: id };
    this.emit();
  }

  bringToFront(id: string) {
    if (!this.state.byId[id] || this.state.order[this.state.order.length - 1] === id) return;
    this.commit({ ...this.state, order: [...this.state.order.filter((x) => x !== id), id] });
  }

  get canUndo(): boolean {
    return this.undoStack.length > 0;
  }
  get canRedo(): boolean {
    return this.redoStack.length > 0;
  }

  undo() {
    const prev = this.undoStack.pop();
    if (!prev) return;
    this.redoStack.push(this.state);
    this.state = prev;
    this.persistence.save(this.scopeKey, this.list());
    this.emit();
  }

  redo() {
    const next = this.redoStack.pop();
    if (!next) return;
    this.undoStack.push(this.state);
    this.state = next;
    this.persistence.save(this.scopeKey, this.list());
    this.emit();
  }
}

// A single store instance for the whole app session; scoped internally by
// instrument+timeframe via setScope. One instance (not one per scope) keeps
// the undo stack intentionally scope-local (reset on switch) while letting
// every consumer share the same subscription.
let sharedStore: DrawingsStore | null = null;
export function getDrawingsStore(): DrawingsStore {
  if (!sharedStore) sharedStore = new DrawingsStore(new LocalStorageDrawingsPersistence());
  return sharedStore;
}

/**
 * React binding: subscribes to the shared store, scoped to scopeKey, and
 * returns the live list + selection + mutators. Re-renders only when the
 * store actually changes (useSyncExternalStore), not on every chart repaint.
 */
export function useDrawingsStore(scopeKey: string) {
  const store = getDrawingsStore();
  store.setScope(scopeKey);
  const snapshot = useSyncExternalStore(store.subscribe, store.getSnapshot);

  return useMemo(
    () => ({
      drawings: snapshot.order.map((id) => snapshot.byId[id]),
      selectedId: snapshot.selectedId,
      selected: snapshot.selectedId ? snapshot.byId[snapshot.selectedId] : null,
      add: store.add.bind(store),
      update: store.update.bind(store),
      remove: store.remove.bind(store),
      clear: store.clear.bind(store),
      select: store.select.bind(store),
      bringToFront: store.bringToFront.bind(store),
      undo: store.undo.bind(store),
      redo: store.redo.bind(store),
      canUndo: store.canUndo,
      canRedo: store.canRedo,
      store,
    }),
    [snapshot, store],
  );
}
