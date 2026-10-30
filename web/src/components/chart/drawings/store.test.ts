import { describe, expect, it } from "vitest";
import { DrawingsStore, type DrawingsPersistence } from "./store";
import type { Drawing, HLineDrawing } from "./types";
import { DEFAULT_STYLE } from "./types";

// An in-memory stand-in for LocalStorageDrawingsPersistence, so these tests
// exercise the store's own logic (undo/redo, scoping, mutation) without a
// DOM. LocalStorageDrawingsPersistence itself is a thin pass-through to
// window.localStorage and is not covered here (no jsdom in this project);
// this fake implements the same DrawingsPersistence contract it does.
class MemoryPersistence implements DrawingsPersistence {
  store = new Map<string, Drawing[]>();
  saves = 0;
  load(scopeKey: string): Drawing[] | null {
    return this.store.get(scopeKey) ?? null;
  }
  save(scopeKey: string, drawings: Drawing[]): void {
    this.saves++;
    this.store.set(scopeKey, drawings);
  }
}

function hline(id: string, price: number): HLineDrawing {
  return { id, kind: "hline", price, style: DEFAULT_STYLE, createdAt: 0, updatedAt: 0 };
}

describe("DrawingsStore", () => {
  it("adds and lists drawings in insertion order, selecting the new one", () => {
    const store = new DrawingsStore(new MemoryPersistence());
    store.setScope("BTC:5m");
    store.add(hline("a", 100));
    store.add(hline("b", 200));

    expect(store.list().map((d) => d.id)).toEqual(["a", "b"]);
    expect(store.selectedId).toBe("b");
  });

  it("moves (updates) a drawing's fields", () => {
    const store = new DrawingsStore(new MemoryPersistence());
    store.setScope("BTC:5m");
    store.add(hline("a", 100));

    store.update("a", { price: 150 });

    expect((store.get("a") as HLineDrawing).price).toBe(150);
  });

  it("removes a drawing and clears selection if it was selected", () => {
    const store = new DrawingsStore(new MemoryPersistence());
    store.setScope("BTC:5m");
    store.add(hline("a", 100));

    store.remove("a");

    expect(store.list()).toEqual([]);
    expect(store.selectedId).toBeNull();
  });

  it("clears every drawing", () => {
    const store = new DrawingsStore(new MemoryPersistence());
    store.setScope("BTC:5m");
    store.add(hline("a", 100));
    store.add(hline("b", 200));

    store.clear();

    expect(store.list()).toEqual([]);
  });

  it("bringToFront moves a drawing to the end of the z-order", () => {
    const store = new DrawingsStore(new MemoryPersistence());
    store.setScope("BTC:5m");
    store.add(hline("a", 100));
    store.add(hline("b", 200));
    store.add(hline("c", 300));

    store.bringToFront("a");

    expect(store.list().map((d) => d.id)).toEqual(["b", "c", "a"]);
  });

  it("undo reverts an add; redo re-applies it", () => {
    const store = new DrawingsStore(new MemoryPersistence());
    store.setScope("BTC:5m");
    store.add(hline("a", 100));

    expect(store.canUndo).toBe(true);
    store.undo();
    expect(store.list()).toEqual([]);
    expect(store.canUndo).toBe(false);
    expect(store.canRedo).toBe(true);

    store.redo();
    expect(store.list().map((d) => d.id)).toEqual(["a"]);
  });

  it("undo reverts a move back to the pre-drag value", () => {
    const store = new DrawingsStore(new MemoryPersistence());
    store.setScope("BTC:5m");
    store.add(hline("a", 100));

    store.update("a", { price: 150 });
    store.undo();

    expect((store.get("a") as HLineDrawing).price).toBe(100);
  });

  it("a drag's intermediate updates (history: false) collapse into one undo step", () => {
    const store = new DrawingsStore(new MemoryPersistence());
    store.setScope("BTC:5m");
    store.add(hline("a", 100));

    // Simulates a drag: the first frame pushes history, every subsequent
    // frame in the same gesture does not -- exactly what the controller
    // does so a whole drag undoes in one step, not one step per pixel.
    store.update("a", { price: 110 }, { history: true });
    store.update("a", { price: 120 }, { history: false });
    store.update("a", { price: 130 }, { history: false });

    expect((store.get("a") as HLineDrawing).price).toBe(130);
    store.undo();
    // Back to the value right after add (100), in one undo -- the three
    // updates collapsed into a single history entry. The add itself is
    // still a separate, further undo step.
    expect((store.get("a") as HLineDrawing).price).toBe(100);
    expect(store.canUndo).toBe(true);
    store.undo();
    expect(store.list()).toEqual([]);
    expect(store.canUndo).toBe(false);
  });

  it("a new action after an undo clears the redo stack", () => {
    const store = new DrawingsStore(new MemoryPersistence());
    store.setScope("BTC:5m");
    store.add(hline("a", 100));
    store.add(hline("b", 200));

    store.undo(); // removes b
    expect(store.canRedo).toBe(true);

    store.add(hline("c", 300));
    expect(store.canRedo).toBe(false);
    expect(store.list().map((d) => d.id)).toEqual(["a", "c"]);
  });

  it("persists on every mutating change and reloads the same scope's drawings in a new store instance", () => {
    const persistence = new MemoryPersistence();
    const first = new DrawingsStore(persistence);
    first.setScope("BTC:5m");
    first.add(hline("a", 100));
    first.add(hline("b", 200));

    expect(persistence.saves).toBeGreaterThan(0);

    const second = new DrawingsStore(persistence);
    second.setScope("BTC:5m");

    expect(second.list().map((d) => d.id)).toEqual(["a", "b"]);
  });

  it("scopes drawings by instrument+timeframe: switching scope swaps the set and resets undo history", () => {
    const persistence = new MemoryPersistence();
    const store = new DrawingsStore(persistence);

    store.setScope("BTC:5m");
    store.add(hline("a", 100));

    store.setScope("ETH:5m");
    expect(store.list()).toEqual([]);
    expect(store.canUndo).toBe(false);
    store.add(hline("x", 10));

    store.setScope("BTC:5m");
    expect(store.list().map((d) => d.id)).toEqual(["a"]);

    store.setScope("ETH:5m");
    expect(store.list().map((d) => d.id)).toEqual(["x"]);
  });
});
