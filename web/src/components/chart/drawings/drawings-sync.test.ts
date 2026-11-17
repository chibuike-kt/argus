// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { drawingsAdapter, SyncedDrawingsPersistence } from "./drawings-sync";
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
    p.aliasLegacy("4:1m", "BTC:1m");
    const loaded = p.load("4:1m");
    expect(loaded?.map((x) => x.id)).toEqual(["old1", "old2"]);

    await vi.waitFor(() => expect(puts.length).toBeGreaterThan(0), { timeout: 3000 });
    expect(puts[0].url).toContain("instrument_id=4&timeframe=1m");
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
    p.load("4:5m");
    expect(gets).toHaveLength(0); // no user yet: nothing fetched
    p.attachUser(7);
    await vi.waitFor(() => expect(remote.at(-1)?.map((x) => x.id)).toEqual(["server1"]));
  });
});
