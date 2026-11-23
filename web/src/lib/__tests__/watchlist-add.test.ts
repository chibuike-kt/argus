// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { getWatchlistStore, resetWatchlistStoreForTests } from "../watchlist-store";
import { addAvailability, filterCatalog, remainingWords, WATCHLIST_MAX_ITEMS } from "@/components/workspace/symbol-search";
import { failureWords, moveItem, trackingBadge } from "@/components/workspace/watchlist";
import type { ApiInstrument, ApiWatchlistTracking } from "@/api/types";
import { refreshLiveStats } from "../live-stats";

vi.mock("../live-stats", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../live-stats")>()),
  refreshLiveStats: vi.fn(),
}));

// Phase 4.1a: optimistic add and reorder with rollback, the dialog's rules,
// and the row helpers.

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}
const list = (ids: number[]) => ids.map((instrument_id, position) => ({ instrument_id, position }));

let server: number[];
let addStatus: number;
let addCode: string;
let orderStatus: number;
let orderCode: string;
let resolveAdd: (() => void) | null;

beforeEach(() => {
  vi.mocked(refreshLiveStats).mockClear();
  resetWatchlistStoreForTests();
  server = [1, 2, 3];
  addStatus = 200;
  addCode = "";
  orderStatus = 200;
  orderCode = "";
  resolveAdd = null;
  global.fetch = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const method = init?.method ?? "GET";
    if (url.pathname === "/api/v1/watchlist" && method === "GET") return json(list(server));
    if (url.pathname === "/api/v1/watchlist/items" && method === "POST") {
      const { instrument_id } = JSON.parse(String(init?.body));
      await new Promise<void>((r) => (resolveAdd = r));
      if (addStatus !== 200) return json({ error: { code: addCode, message: `refused: ${addCode}` } }, addStatus);
      if (!server.includes(instrument_id)) server = [...server, instrument_id];
      return json(list(server));
    }
    if (url.pathname === "/api/v1/watchlist/order" && method === "PUT") {
      const { instrument_ids } = JSON.parse(String(init?.body));
      if (orderStatus !== 200) return json({ error: { code: orderCode, message: `refused: ${orderCode}` } }, orderStatus);
      server = instrument_ids;
      return json(list(server));
    }
    throw new Error(`unexpected ${method} ${url}`);
  }) as unknown as typeof fetch;
});

async function loaded() {
  const s = getWatchlistStore();
  s.subscribe(() => {});
  await vi.waitFor(() => expect(s.getSnapshot().ids).toEqual([1, 2, 3]));
  return s;
}

describe("watchlist add", () => {
  it("shows the row at once and keeps the server's list when it confirms", async () => {
    const s = await loaded();
    const done = s.add(9);
    expect(s.getSnapshot().ids).toEqual([1, 2, 3, 9]);
    expect(s.getSnapshot().adding).toEqual([9]);
    await vi.waitFor(() => expect(resolveAdd).not.toBeNull());
    resolveAdd!();
    expect(await done).toBe(true);
    expect(s.getSnapshot().ids).toEqual([1, 2, 3, 9]);
    expect(s.getSnapshot().adding).toEqual([]);
    expect(s.getSnapshot().version).toBe(1);
    // Its live figures were refused while it was disabled: asked again.
    expect(refreshLiveStats).toHaveBeenCalledWith([9]);
  });

  it("a refused add takes only its own row out again, with the server's reason", async () => {
    const s = await loaded();
    addStatus = 409;
    addCode = "tracked_cap_reached";
    const done = s.add(9);
    // Meanwhile another row is removed (in its undo window): that stays.
    s.remove(1);
    await vi.waitFor(() => expect(resolveAdd).not.toBeNull());
    resolveAdd!();
    expect(await done).toBe(false);
    expect(s.getSnapshot().ids).toEqual([2, 3]);
    expect(s.getSnapshot().pending).toEqual([1]);
    expect(s.getSnapshot().failure).toMatchObject({ id: 9, action: "add", code: "tracked_cap_reached" });
    expect(refreshLiveStats).not.toHaveBeenCalled();
    expect(failureWords(s.getSnapshot().failure!, "AVAX")).toBe("Could not add AVAX: refused: tracked_cap_reached. It was taken off the list again.");
  });

  it("adding a row still in its removal undo window is the undo, with no request", async () => {
    const s = await loaded();
    s.remove(2);
    expect(await s.add(2)).toBe(true);
    expect(s.getSnapshot().ids).toEqual([1, 2, 3]);
    expect(resolveAdd).toBeNull();
  });
});

describe("watchlist reorder", () => {
  it("shows the new order at once and saves it, with pending removals sent at the end", async () => {
    const s = await loaded();
    s.remove(1);
    expect(await s.reorder([3, 2])).toBe(true);
    expect(server).toEqual([3, 2, 1]);
    expect(s.getSnapshot().ids).toEqual([3, 2]);
  });

  it("a failed save puts the previous order back", async () => {
    const s = await loaded();
    orderStatus = 500;
    orderCode = "internal";
    const p = s.reorder([3, 1, 2]);
    expect(s.getSnapshot().ids).toEqual([3, 1, 2]);
    expect(await p).toBe(false);
    expect(s.getSnapshot().ids).toEqual([1, 2, 3]);
    expect(s.getSnapshot().failure).toMatchObject({ action: "reorder" });
  });

  it("a stale order (another tab changed the list) reloads the server's list", async () => {
    const s = await loaded();
    server = [1, 2, 3, 7]; // another tab added 7
    orderStatus = 409;
    orderCode = "watchlist_changed";
    expect(await s.reorder([3, 2, 1])).toBe(false);
    await vi.waitFor(() => expect(s.getSnapshot().ids).toEqual([1, 2, 3, 7]));
    expect(failureWords(s.getSnapshot().failure!, undefined)).toMatch(/changed in another tab; it was reloaded/);
  });
});

const inst = (id: number, sym: string, enabled = false): ApiInstrument =>
  ({ id, native_symbol: sym, provider: "hyperliquid", enabled, base_asset: sym, quote_asset: "USD" }) as ApiInstrument;

const tracking = (over: Partial<ApiWatchlistTracking> = {}): ApiWatchlistTracking => ({
  can_enable: true,
  cap: 8,
  remaining: 5,
  tracked_instrument_ids: [1, 2, 3],
  items: [],
  ...over,
});

describe("symbol search rules", () => {
  it("filters by symbol, prefix matches first, any case", () => {
    const cat = [inst(1, "BTC"), inst(2, "WBTC"), inst(3, "ETH"), inst(4, "BTCDOM")];
    expect(filterCatalog(cat, "btc").map((i) => i.native_symbol)).toEqual(["BTC", "BTCDOM", "WBTC"]);
    expect(filterCatalog(cat, "  ").length).toBe(4);
    expect(filterCatalog(cat, "zzz")).toEqual([]);
  });

  it("says why an Add is not offered, the same rules as the server", () => {
    const base = { onList: new Set([1]), listCount: 3, tracking: tracking() };
    expect(addAvailability(inst(1, "BTC", true), base)).toEqual({ kind: "added" });
    expect(addAvailability(inst(9, "AVAX"), base)).toEqual({ kind: "add" });
    expect(addAvailability(inst(9, "AVAX"), { ...base, listCount: WATCHLIST_MAX_ITEMS }).kind).toBe("blocked");
    const prod = addAvailability(inst(9, "AVAX"), { ...base, tracking: tracking({ can_enable: false }) });
    expect(prod).toEqual({ kind: "blocked", reason: "Needs to be enabled by the operator." });
    const full = tracking({ cap: 3, remaining: 0 });
    expect(addAvailability(inst(9, "AVAX"), { ...base, tracking: full })).toEqual({
      kind: "blocked",
      reason: "ARGUS tracks at most 3 symbols at once, including the ones it always tracks: no more symbols can be added. Remove a symbol from every watchlist to add another.",
    });
    // The server's remaining decides (it counts the floor): with one left, Add is offered.
    expect(addAvailability(inst(9, "AVAX"), { ...base, tracking: tracking({ cap: 8, remaining: 1 }) })).toEqual({ kind: "add" });
    expect(remainingWords(tracking({ cap: 8, remaining: 1 }))).toMatch(/: 1 more symbol can be added\./);
    expect(remainingWords(tracking({ cap: 8, remaining: 4 }))).toMatch(/: 4 more symbols can be added\./);
    // A symbol already tracked (another user watches it) can be added at the cap.
    expect(addAvailability(inst(2, "ETH", true), { ...base, tracking: full })).toEqual({ kind: "add" });
  });

  it("filters 10x the catalog (1,790 rows) quickly", () => {
    const big = Array.from({ length: 1790 }, (_, i) => inst(i + 1, `SYM${i}X`));
    const t0 = performance.now();
    for (let k = 0; k < 50; k++) filterCatalog(big, String(k % 10));
    const perKeystroke = (performance.now() - t0) / 50;
    expect(perKeystroke).toBeLessThan(16); // well inside a frame
  });
});

describe("row helpers", () => {
  it("moves an item up or down, clamped at the ends", () => {
    expect(moveItem([1, 2, 3], 2, -1)).toEqual([1, 3, 2]);
    expect(moveItem([1, 2, 3], 0, -1)).toEqual([1, 2, 3]);
    expect(moveItem([1, 2, 3], 0, 2)).toEqual([2, 3, 1]);
  });

  it("names the row state, nothing while live", () => {
    expect(trackingBadge({ instrument_id: 1, tracked: true, state: "live" })).toBeNull();
    expect(trackingBadge({ instrument_id: 1, tracked: true, state: "loading_history" })?.text).toBe("Loading history");
    expect(trackingBadge({ instrument_id: 1, tracked: true, state: "retrying", attempt: 3, reason: "Unknown coin X" })?.title).toBe(
      "Retrying (attempt 3): Unknown coin X",
    );
    expect(trackingBadge({ instrument_id: 1, tracked: true, state: "needs_attention", reason: "price precision changed" })?.text).toBe("Needs attention");
    expect(trackingBadge({ instrument_id: 1, tracked: false, state: "waiting", reason: "over the system cap of 8 tracked symbols" })?.title).toMatch(/cap of 8/);
  });
});
