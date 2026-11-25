// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { drawingsAdapter, mergeDrawingLists, SyncedDrawingsPersistence } from "./drawings-sync";
import { legacyDrawingScopes } from "../drawing-context";
import type { Drawing } from "./types";

const d = (id: string, extra: Record<string, unknown> = {}) => ({ id, kind: "hline", price: 1, ...extra }) as unknown as Drawing;

describe("drawings merge by id", () => {
  it("adds, replaces, deletes and reorders by id", () => {
    let doc = [d("a"), d("b")];
    doc = drawingsAdapter.set(doc, "c", d("c"));
    doc = drawingsAdapter.set(doc, "a", d("a", { price: 9 }));
    doc = drawingsAdapter.set(doc, "b", undefined);
    expect(doc.map((x) => x.id)).toEqual(["a", "c"]);
    expect((doc[0] as unknown as { price: number }).price).toBe(9);
    doc = drawingsAdapter.set(doc, "__order", ["c", "a"]);
    expect(doc.map((x) => x.id)).toEqual(["c", "a"]);
  });
});

describe("SyncedDrawingsPersistence", () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  // Drawings that exist only in this browser (made before server sync)
  // must be PUSHED on first sync, not replaced by an empty server copy.
  it("first sync of a chart pushes the drawings already cached locally (migration)", async () => {
    localStorage.setItem("argus:drawings:v1:BTC:1m", JSON.stringify([d("old1"), d("old2")]));
    const puts: { url: string; body: { data: Drawing[]; version: number } }[] = [];
    global.fetch = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "PUT") {
        const body = JSON.parse(String(init.body));
        puts.push({ url, body });
        return new Response(JSON.stringify({ data: body.data, version: 1, instrument_id: 4, timeframe: "1m" }), { status: 200 });
      }
      return new Response(JSON.stringify({ data: [], version: 0, instrument_id: 4, timeframe: "1m" }), { status: 200 });
    }) as unknown as typeof fetch;

    const p = new SyncedDrawingsPersistence();
    p.attachUser(7);
    p.aliasLegacy("4", legacyDrawingScopes(4, "BTC"));
    const loaded = p.load("4");
    expect(loaded?.map((x) => x.id)).toEqual(["old1", "old2"]);

    await vi.waitFor(() => expect(puts.length).toBeGreaterThan(0), { timeout: 3000 });
    expect(puts[0].url).toContain("instrument_id=4");
    expect(puts[0].url).not.toContain("timeframe");
    expect(puts[0].body.data.map((x) => x.id)).toEqual(["old1", "old2"]);
  });

  it("a chart loaded before sign-in starts syncing once the user is attached", async () => {
    const gets: string[] = [];
    global.fetch = vi.fn(async (input: string | URL | Request) => {
      gets.push(String(input));
      return new Response(JSON.stringify({ data: [d("server1")], version: 3, instrument_id: 4, timeframe: "5m" }), { status: 200 });
    }) as unknown as typeof fetch;
    const p = new SyncedDrawingsPersistence();
    const remote: Drawing[][] = [];
    p.onRemote = (_scope, ds) => remote.push(ds);
    p.load("4");
    expect(gets).toHaveLength(0); // no user yet: nothing fetched
    p.attachUser(7);
    await vi.waitFor(() => expect(remote.at(-1)?.map((x) => x.id)).toEqual(["server1"]));
  });

  // D17: the caches kept per timeframe (by id, and older, by symbol) merge
  // into the instrument's one cache, once: none lost, a duplicate keeping
  // its most recently changed copy.
  it("merges the per timeframe caches of an instrument once", () => {
    global.fetch = vi.fn(async () => new Response(JSON.stringify({ data: [], version: 0 }), { status: 200 })) as unknown as typeof fetch;
    localStorage.setItem("argus:drawings:v1:BTC:1d", JSON.stringify([d("e", { updatedAt: 1 })]));
    localStorage.setItem("argus:drawings:v1:4:1m", JSON.stringify([d("a", { updatedAt: 1, price: 1 }), d("b", { updatedAt: 1 })]));
    localStorage.setItem("argus:drawings:v1:4:1h", JSON.stringify([d("c", { updatedAt: 2 }), d("a", { updatedAt: 5, price: 7 })]));
    const p = new SyncedDrawingsPersistence();
    p.aliasLegacy("4", legacyDrawingScopes(4, "BTC"));
    const loaded = p.load("4")!;
    expect(loaded.map((x) => x.id).sort()).toEqual(["a", "b", "c", "e"]);
    expect((loaded.find((x) => x.id === "a") as unknown as { price: number }).price).toBe(7);
    expect(JSON.parse(localStorage.getItem("argus:drawings:v1:4")!).length).toBe(4);
    // Once: a later change to an old cache is not merged again.
    localStorage.setItem("argus:drawings:v1:4:1m", JSON.stringify([d("late")]));
    const again = new SyncedDrawingsPersistence();
    again.aliasLegacy("4", legacyDrawingScopes(4, "BTC"));
    expect(again.load("4")!.map((x) => x.id).sort()).toEqual(["a", "b", "c", "e"]);
  });

  // A real failure, as the browser sees it: the server stops answering
  // while drawings are edited, then comes back. Nothing is lost: the local
  // copy keeps every edit, and the sync pushes them once the server is back.
  it("server down while editing, then back: every drawing reaches the server", async () => {
    let up = false;
    let stored: { data: Drawing[]; version: number } = { data: [d("s1")], version: 1 };
    global.fetch = vi.fn(async (_input: string | URL | Request, init?: RequestInit) => {
      if (!up) throw new TypeError("Failed to fetch"); // connection refused
      if (init?.method === "PUT") {
        const body = JSON.parse(String(init.body));
        if (body.version !== stored.version) return new Response(JSON.stringify({ error: { code: "version_conflict", message: "stale" } }), { status: 409 });
        stored = { data: body.data, version: stored.version + 1 };
      }
      return new Response(JSON.stringify(stored), { status: 200 });
    }) as unknown as typeof fetch;
    const p = new SyncedDrawingsPersistence();
    p.attachUser(7);
    p.load("4");
    p.save("4", [d("n1")]);
    p.save("4", [d("n1"), d("n2")]);
    await new Promise((r) => setTimeout(r, 1200));
    expect(JSON.parse(localStorage.getItem("argus:drawings:v1:4")!).map((x: Drawing) => x.id)).toEqual(["n1", "n2"]);
    up = true;
    p.nudge();
    await vi.waitFor(() => expect(stored.data.map((x) => x.id).sort()).toEqual(["n1", "n2", "s1"]), { timeout: 8000 });
  }, 15000);
});

describe("mergeDrawingLists", () => {
  it("keeps every id once, the newest copy of a duplicate, in a sensible z order", () => {
    const merged = mergeDrawingLists([
      [d("a", { updatedAt: 1 }), d("b", { updatedAt: 1 })],
      [d("c", { updatedAt: 1 }), d("a", { updatedAt: 9, price: 9 })],
    ]);
    expect(merged.map((x) => x.id)).toEqual(["b", "c", "a"]);
    expect((merged[2] as unknown as { price: number }).price).toBe(9);
    // An older copy in a later list does not replace a newer one.
    const kept = mergeDrawingLists([[d("x", { updatedAt: 9, price: 9 })], [d("x", { updatedAt: 1, price: 1 })]]);
    expect((kept[0] as unknown as { price: number }).price).toBe(9);
  });
});
