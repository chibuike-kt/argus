// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { getLiveSocket, resetLiveSocketForTests } from "../live-socket";
import { isOlder, resetLiveStatsForTests, useLiveStats } from "../live-stats";
import { compareDecimal } from "../decimal-display";
import type { ApiInstrumentStats, ApiLiveFigures } from "@/api/types";

// The page's one live stats source (Phase 3): one REST read, then one
// multiplexed subscribe_stats, newest figures win, one REST resync on
// reconnect and on a hidden tab's return.

class MockWebSocket {
  static instances: MockWebSocket[] = [];
  static readonly OPEN = 1;
  static readonly CONNECTING = 0;
  static readonly CLOSED = 3;
  readyState = MockWebSocket.CONNECTING;
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onclose: ((event: { code: number; reason: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) {
    MockWebSocket.instances.push(this);
  }
  send(data: string) {
    this.sent.push(data);
  }
  close(code = 1000, reason = "") {
    this.readyState = MockWebSocket.CLOSED;
    this.onclose?.({ code, reason });
  }
  open() {
    this.readyState = MockWebSocket.OPEN;
    this.onopen?.();
  }
  receive(data: unknown) {
    this.onmessage?.({ data: JSON.stringify(data) });
  }
  sentOfType(type: string) {
    return this.sent.map((s) => JSON.parse(s) as { type: string; payload?: { instrument_ids?: number[] } }).filter((e) => e.type === type);
  }
}

function live(id: number, price: string, asOf: string, tick: string, over: Partial<ApiLiveFigures> = {}): ApiLiveFigures {
  return {
    instrument_id: id,
    last_price: price,
    last_price_closed: false,
    as_of: asOf,
    last_tick_at: tick,
    change_24h: "1.00",
    change_24h_pct: "0.50",
    change_24h_reference_open_time: "2026-11-17T12:00:00Z",
    volume_24h: "10.0",
    volume_24h_minutes: 1440,
    ...over,
  };
}

function rest(f: ApiLiveFigures): ApiInstrumentStats {
  return {
    ...f,
    last_close: "99.00",
    last_closed_open_time: "2026-11-18T12:00:00Z",
    performance: { "1D": "0.50", "1W": null, "1M": null, "3M": null, "6M": null, "1Y": null },
  };
}

let restRows: Map<number, ApiInstrumentStats>;
let restCalls: number[][];

beforeEach(() => {
  MockWebSocket.instances = [];
  vi.stubGlobal("WebSocket", MockWebSocket);
  resetLiveSocketForTests();
  resetLiveStatsForTests();
  restRows = new Map();
  restCalls = [];
  global.fetch = vi.fn((input: string | URL | Request) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname !== "/api/v1/instruments/stats") throw new Error(`unexpected fetch ${url}`);
    const ids = (url.searchParams.get("ids") ?? "").split(",").map(Number);
    restCalls.push(ids);
    const body = ids.filter((id) => restRows.has(id)).map((id) => restRows.get(id));
    return Promise.resolve(new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }));
  }) as unknown as typeof fetch;
});

afterEach(() => {
  cleanup();
  resetLiveStatsForTests();
  vi.unstubAllGlobals();
});

describe("compareDecimal (exact, never a float)", () => {
  it.each([
    ["62540.6", "62540.5", 1],
    ["62540.5", "62540.6", -1],
    ["62540.50", "62540.5", 0],
    ["-1.5", "-1.25", -1],
    ["-0.00", "0", 0],
    ["10", "9.99", 1],
    ["0.30000000000000000001", "0.3", 1],
  ])("compare(%s, %s) = %d", (a, b, want) => {
    expect(compareDecimal(a, b)).toBe(want);
  });
});

describe("isOlder", () => {
  it("orders by candle (as_of), then by stored tick time", () => {
    const a = live(1, "1", "2026-11-18T12:01:00Z", "2026-11-18T12:01:05Z");
    expect(isOlder(live(1, "1", "2026-11-18T12:00:00Z", "2026-11-18T12:01:59Z"), a)).toBe(true);
    expect(isOlder(live(1, "1", "2026-11-18T12:01:00Z", "2026-11-18T12:01:04Z"), a)).toBe(true);
    expect(isOlder(live(1, "1", "2026-11-18T12:01:00Z", "2026-11-18T12:01:05Z"), a)).toBe(false);
  });
});

describe("live stats store", () => {
  it("one REST read and ONE subscribe_stats for every instrument held, however many consumers", async () => {
    for (const id of [1, 2, 3]) restRows.set(id, rest(live(id, "100.00", "2026-11-18T12:01:00Z", "2026-11-18T12:01:05Z")));
    renderHook(() => useLiveStats([1, 2]));
    renderHook(() => useLiveStats([2, 3])); // e.g. the panel and the watchlist
    await waitFor(() => expect(MockWebSocket.instances).toHaveLength(1));
    const ws = MockWebSocket.instances[0];
    act(() => ws.open());
    await waitFor(() => expect(ws.sentOfType("subscribe_stats").length).toBeGreaterThan(0));
    const last = ws.sentOfType("subscribe_stats").at(-1)!;
    expect([...last.payload!.instrument_ids!].sort()).toEqual([1, 2, 3]);
    // Never a candle subscription per row.
    expect(ws.sentOfType("subscribe")).toHaveLength(0);
    await waitFor(() => expect(restCalls.flat().sort()).toEqual([1, 2, 3]));
  });

  it("a stats message replaces the figures; an older one is ignored; the shared entry is the same for every consumer", async () => {
    restRows.set(1, rest(live(1, "100.00", "2026-11-18T12:01:00Z", "2026-11-18T12:01:05Z")));
    const panel = renderHook(() => useLiveStats([1]));
    const legend = renderHook(() => useLiveStats([1]));
    await waitFor(() => expect(panel.result.current.entries.get(1)?.figures?.last_price).toBe("100.00"));
    const ws = MockWebSocket.instances[0];
    act(() => ws.open());

    act(() => ws.receive({ v: 1, type: "stats", sub: "stats:1", payload: live(1, "100.10", "2026-11-18T12:01:00Z", "2026-11-18T12:01:06Z", { change_24h: "1.10" }) }));
    expect(panel.result.current.entries.get(1)?.figures?.change_24h).toBe("1.10");
    expect(legend.result.current.entries.get(1)).toBe(panel.result.current.entries.get(1));

    // A late, older message (an earlier tick of the same minute) is dropped.
    act(() => ws.receive({ v: 1, type: "stats", sub: "stats:1", payload: live(1, "99.00", "2026-11-18T12:01:00Z", "2026-11-18T12:01:01Z") }));
    expect(panel.result.current.entries.get(1)?.figures?.last_price).toBe("100.10");
  });

  it("a per-instrument error marks only that instrument as not available", async () => {
    restRows.set(1, rest(live(1, "100.00", "2026-11-18T12:01:00Z", "2026-11-18T12:01:05Z")));
    const h = renderHook(() => useLiveStats([1, 2]));
    await waitFor(() => expect(h.result.current.entries.get(1)?.figures).not.toBeNull());
    // REST omits the unknown/disabled id 2 already.
    await waitFor(() => expect(h.result.current.entries.get(2)?.rejected).toBe(true));
    expect(h.result.current.entries.get(1)?.rejected).toBe(false);
  });

  it("a reconnect resyncs with one REST call and resubscribes", async () => {
    restRows.set(1, rest(live(1, "100.00", "2026-11-18T12:01:00Z", "2026-11-18T12:01:05Z")));
    renderHook(() => useLiveStats([1]));
    await waitFor(() => expect(restCalls).toHaveLength(1));
    const ws = MockWebSocket.instances[0];
    act(() => ws.open());
    vi.useFakeTimers();
    try {
      act(() => ws.close(1006, ""));
      act(() => vi.advanceTimersByTime(31_000)); // past the full-jitter backoff cap
    } finally {
      vi.useRealTimers();
    }
    const ws2 = MockWebSocket.instances[1];
    expect(ws2).toBeDefined();
    act(() => ws2.open());
    expect(ws2.sentOfType("subscribe_stats")).toHaveLength(1);
    await waitFor(() => expect(restCalls).toHaveLength(2));
  });

  it("a hidden tab renders nothing, and its return resyncs once", async () => {
    restRows.set(1, rest(live(1, "100.00", "2026-11-18T12:01:00Z", "2026-11-18T12:01:05Z")));
    let renders = 0;
    const h = renderHook(() => {
      renders++;
      return useLiveStats([1]);
    });
    await waitFor(() => expect(h.result.current.entries.get(1)?.figures?.last_price).toBe("100.00"));
    const ws = MockWebSocket.instances[0];
    act(() => ws.open());

    const visibility = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    const before = renders;
    for (let i = 0; i < 50; i++) {
      act(() => ws.receive({ v: 1, type: "stats", sub: "stats:1", payload: live(1, `100.${String(i + 10)}`, "2026-11-18T12:01:00Z", `2026-11-18T12:01:${String(10 + i).padStart(2, "0")}Z`) }));
    }
    expect(renders).toBe(before); // nothing rendered while hidden
    expect(h.result.current.entries.get(1)?.figures?.last_price).toBe("100.00");

    const callsBefore = restCalls.length;
    visibility.mockReturnValue("visible");
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    await waitFor(() => expect(restCalls.length).toBe(callsBefore + 1));
    // The newest pushed figures are on screen at once, not the stale ones.
    expect(h.result.current.entries.get(1)?.figures?.last_price).toBe("100.59");
    visibility.mockRestore();
  });

  it("a new minute re-reads the closed-candle figures, once for all instruments", async () => {
    for (const id of [1, 2]) restRows.set(id, rest(live(id, "100.00", "2026-11-18T12:01:00Z", "2026-11-18T12:01:05Z")));
    const h = renderHook(() => useLiveStats([1, 2]));
    await waitFor(() => expect(h.result.current.entries.get(2)?.figures).not.toBeNull());
    const ws = MockWebSocket.instances[0];
    act(() => ws.open());
    const calls = restCalls.length;
    vi.useFakeTimers();
    try {
      act(() => ws.receive({ v: 1, type: "stats", sub: "stats:1", payload: live(1, "100.20", "2026-11-18T12:02:00Z", "2026-11-18T12:02:01Z") }));
      act(() => ws.receive({ v: 1, type: "stats", sub: "stats:2", payload: live(2, "100.20", "2026-11-18T12:02:00Z", "2026-11-18T12:02:01Z") }));
      act(() => vi.advanceTimersByTime(2_000));
    } finally {
      vi.useRealTimers();
    }
    await waitFor(() => expect(restCalls.length).toBe(calls + 1));
    expect([...restCalls.at(-1)!].sort()).toEqual([1, 2]);
  });

  it("releasing every consumer ends the stats subscription", async () => {
    const h = renderHook(() => useLiveStats([1]));
    await waitFor(() => expect(MockWebSocket.instances).toHaveLength(1));
    const ws = MockWebSocket.instances[0];
    act(() => ws.open());
    h.unmount();
    await waitFor(() => expect(ws.readyState).toBe(MockWebSocket.CLOSED));
    expect(getLiveSocket().subscriptionCount()).toBe(0);
  });
});
