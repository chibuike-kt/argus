// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getWatchlistStore, resetWatchlistStoreForTests, UNDO_WINDOW_MS } from "../watchlist-store";
import { nextAfterRemoval } from "@/components/workspace/watchlist";

// Optimistic watchlist removal with undo (fix/chart-interaction, item 3).

interface Call {
  method: string;
  path: string;
  keepalive: boolean;
}

let calls: Call[];
let deleteStatus: number;

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

beforeEach(() => {
  resetWatchlistStoreForTests();
  calls = [];
  deleteStatus = 200;
  global.fetch = vi.fn((input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    calls.push({ method: init?.method ?? "GET", path: url.pathname, keepalive: init?.keepalive === true });
    if (url.pathname === "/api/v1/watchlist") {
      return Promise.resolve(json([1, 2, 3].map((id, position) => ({ instrument_id: id, position }))));
    }
    if (url.pathname.startsWith("/api/v1/watchlist/items/")) {
      return Promise.resolve(
        deleteStatus === 200 ? json([]) : json({ error: { code: "internal", message: "database unavailable" } }, deleteStatus),
      );
    }
    throw new Error(`unexpected fetch ${url}`);
  }) as unknown as typeof fetch;
});

afterEach(() => {
  vi.useRealTimers();
});

async function loaded() {
  const s = getWatchlistStore();
  const unsub = s.subscribe(() => {});
  await vi.waitFor(() => expect(s.getSnapshot().ids).toEqual([1, 2, 3]));
  return { s, unsub };
}

const deletes = () => calls.filter((c) => c.method === "DELETE");

describe("watchlist store removal", () => {
  it("removes at once, sends DELETE only when the undo window ends, and the row stays gone", async () => {
    const { s } = await loaded();
    vi.useFakeTimers();
    s.remove(2);
    expect(s.getSnapshot().ids).toEqual([1, 3]);
    expect(s.getSnapshot().pending).toEqual([2]);
    expect(deletes()).toHaveLength(0);

    await vi.advanceTimersByTimeAsync(UNDO_WINDOW_MS);
    expect(deletes()).toEqual([{ method: "DELETE", path: "/api/v1/watchlist/items/2", keepalive: false }]);
    expect(s.getSnapshot().ids).toEqual([1, 3]);
    expect(s.getSnapshot().pending).toEqual([]);
  });

  it("undo inside the window restores the row in place and sends nothing", async () => {
    const { s } = await loaded();
    vi.useFakeTimers();
    s.remove(2);
    s.undo(2);
    expect(s.getSnapshot().ids).toEqual([1, 2, 3]);
    await vi.advanceTimersByTimeAsync(UNDO_WINDOW_MS * 2);
    expect(deletes()).toHaveLength(0);
  });

  it("a server failure puts the row back in its old place, with a message", async () => {
    deleteStatus = 500;
    const { s } = await loaded();
    vi.useFakeTimers();
    s.remove(2);
    await vi.advanceTimersByTimeAsync(UNDO_WINDOW_MS);
    expect(deletes()).toHaveLength(1);
    expect(s.getSnapshot().ids).toEqual([1, 2, 3]);
    expect(s.getSnapshot().failure).toEqual({ id: 2, message: "database unavailable" });
    s.dismissFailure();
    expect(s.getSnapshot().failure).toBeNull();
  });

  it("leaving the page sends every pending removal at once, with keepalive", async () => {
    const { s } = await loaded();
    vi.useFakeTimers();
    s.remove(1);
    s.remove(3);
    window.dispatchEvent(new Event("pagehide"));
    await vi.advanceTimersByTimeAsync(0);
    expect(deletes().map((c) => [c.path, c.keepalive])).toEqual([
      ["/api/v1/watchlist/items/1", true],
      ["/api/v1/watchlist/items/3", true],
    ]);
  });

  it("removing twice, or an id not on the list, is a no-op", async () => {
    const { s } = await loaded();
    vi.useFakeTimers();
    s.remove(2);
    s.remove(2);
    s.remove(99);
    expect(s.getSnapshot().pending).toEqual([2]);
    await vi.advanceTimersByTimeAsync(UNDO_WINDOW_MS);
    expect(deletes()).toHaveLength(1);
  });
});

describe("nextAfterRemoval", () => {
  it("picks the row that takes its place, else the one above, else none", () => {
    expect(nextAfterRemoval([1, 2, 3], 2)).toBe(3);
    expect(nextAfterRemoval([1, 2, 3], 3)).toBe(2);
    expect(nextAfterRemoval([1], 1)).toBeUndefined();
    expect(nextAfterRemoval([1, 2], 9)).toBeUndefined();
  });
});
