// @vitest-environment jsdom
// Scrolling back through stored history (L1.2): useLiveCandles reads the
// latest FIRST_PAGE_BARS first, then older pages of HISTORY_PAGE, one at a
// time, never duplicating a bar, until nothing older is stored or the
// MAX_HISTORY_BARS memory cap is reached.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import type { ApiCandle, ApiCandleHistory } from "@/api/types";

const HOUR = 3_600_000;
// The fake store: stored 1h bars, oldest first, newest at NEWEST.
const NEWEST = Date.UTC(2026, 10, 24, 12);
let stored: ApiCandle[] = [];
let calls: { order?: string; from?: string; to?: string; limit?: number }[] = [];
let gate: Promise<void> | null = null;

function bar(t: number): ApiCandle {
  return {
    open_time: new Date(t).toISOString(),
    close_time: new Date(t + HOUR).toISOString(),
    open: "1.0", high: "1.0", low: "1.0", close: "1.0", volume: "1",
    is_closed: true, revision: 1,
  };
}

function seed(n: number) {
  stored = [];
  for (let i = n - 1; i >= 0; i--) stored.push(bar(NEWEST - i * HOUR));
}

vi.mock("@/api/instruments", () => ({
  listInstrumentCandles: vi.fn(async (_id: number, p: { order?: string; from?: string; to?: string; limit?: number }): Promise<ApiCandleHistory> => {
    calls.push(p);
    if (gate) await gate;
    const limit = p.limit ?? 500;
    let rows = stored;
    if (p.from) rows = rows.filter((c) => Date.parse(c.open_time) >= Date.parse(p.from!));
    if (p.to) rows = rows.filter((c) => Date.parse(c.open_time) < Date.parse(p.to!));
    rows = p.order === "desc" ? [...rows].reverse().slice(0, limit) : rows.slice(0, limit);
    const last = stored[stored.length - 1];
    return { candles: rows, last_closed_open_time: last?.open_time ?? null, last_closed_revision: 1 };
  }),
}));

vi.mock("../live-socket", () => ({
  getLiveSocket: () => ({ subscribe: () => () => {} }),
}));

const { useLiveCandles, FIRST_PAGE_BARS, HISTORY_PAGE, MAX_HISTORY_BARS } = await import("../use-live-candles");

function times(h: ApiCandleHistory | null): number[] {
  return (h?.candles ?? []).map((c) => Date.parse(c.open_time));
}

function assertAscendingUnique(ts: number[]) {
  for (let i = 1; i < ts.length; i++) expect(ts[i]).toBeGreaterThan(ts[i - 1]);
}

describe("useLiveCandles paging", () => {
  beforeEach(() => {
    calls = [];
    gate = null;
  });
  afterEach(() => cleanup());

  it("loads the latest bars first, then older pages without duplicates until the stored start", async () => {
    seed(2_500);
    const { result } = renderHook(() => useLiveCandles(1, "1h"));
    await waitFor(() => expect(result.current.history?.candles.length).toBe(FIRST_PAGE_BARS));
    expect(calls[0]).toMatchObject({ order: "desc", limit: FIRST_PAGE_BARS });
    expect(times(result.current.history).at(-1)).toBe(NEWEST);

    let outcome = await act(() => result.current.loadOlder());
    expect(outcome).toBe("loaded");
    expect(result.current.history?.candles.length).toBe(FIRST_PAGE_BARS + HISTORY_PAGE);
    outcome = await act(() => result.current.loadOlder());
    expect(outcome).toBe("loaded");
    expect(result.current.history?.candles.length).toBe(2_500);
    assertAscendingUnique(times(result.current.history));
    expect(times(result.current.history)[0]).toBe(Date.parse(stored[0].open_time));
    expect(result.current.historyEdge.exhausted).toBe(false);

    outcome = await act(() => result.current.loadOlder());
    expect(outcome).toBe("exhausted");
    expect(result.current.historyEdge).toEqual({ exhausted: true, capped: false });
    // At the start, no further read is made.
    const before = calls.length;
    expect(await act(() => result.current.loadOlder())).toBe("exhausted");
    expect(calls.length).toBe(before);
  });

  it("keeps one load in flight: a second call while one runs does nothing", async () => {
    seed(3_000);
    const { result } = renderHook(() => useLiveCandles(1, "1h"));
    await waitFor(() => expect(result.current.history?.candles.length).toBe(FIRST_PAGE_BARS));
    let release!: () => void;
    gate = new Promise((r) => (release = r));
    const first = result.current.loadOlder();
    const second = await result.current.loadOlder();
    expect(second).toBe("busy");
    const pagesAsked = calls.filter((c) => c.order === "asc").length;
    expect(pagesAsked).toBe(1);
    gate = null;
    release();
    expect(await act(() => first)).toBe("loaded");
    expect(result.current.history?.candles.length).toBe(FIRST_PAGE_BARS + HISTORY_PAGE);
    assertAscendingUnique(times(result.current.history));
  });

  it("stops at the memory cap", async () => {
    seed(12_000);
    // One missing hour, so the pages are not exact multiples of 1,000 and
    // the page that crosses the cap has to be trimmed.
    stored.splice(stored.length - 1_500, 1);
    const { result } = renderHook(() => useLiveCandles(1, "1h"));
    await waitFor(() => expect(result.current.history?.candles.length).toBe(FIRST_PAGE_BARS));
    let outcome = "loaded";
    for (let i = 0; i < 20 && outcome === "loaded"; i++) outcome = await act(() => result.current.loadOlder());
    expect(outcome).toBe("capped");
    expect(result.current.history?.candles.length).toBe(MAX_HISTORY_BARS);
    expect(result.current.historyEdge).toEqual({ exhausted: false, capped: true });
    assertAscendingUnique(times(result.current.history));
    // The newest bars are the ones kept.
    expect(times(result.current.history).at(-1)).toBe(NEWEST);
  });

  it("a range button waits for a scroll page in flight and never merges a page twice", async () => {
    seed(4_000);
    const { result } = renderHook(() => useLiveCandles(1, "1h"));
    await waitFor(() => expect(result.current.history?.candles.length).toBe(FIRST_PAGE_BARS));
    let release!: () => void;
    gate = new Promise((r) => (release = r));
    const scroll = result.current.loadOlder();
    const range = result.current.loadHistoryBack(new Date(NEWEST - 2_500 * HOUR).toISOString());
    gate = null;
    release();
    await act(async () => {
      await scroll;
      await range;
    });
    const held = times(result.current.history);
    assertAscendingUnique(held);
    expect(held[0]).toBeLessThanOrEqual(NEWEST - 2_500 * HOUR);
  });
});
