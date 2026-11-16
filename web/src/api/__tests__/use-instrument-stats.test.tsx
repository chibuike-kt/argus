// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { useInstrumentStats } from "../use-instrument-stats";
import type { ApiInstrumentStats } from "../types";

function row(id: number, lastClose: string): ApiInstrumentStats {
  return {
    instrument_id: id,
    last_close: lastClose,
    last_closed_open_time: "2026-11-16T18:16:00Z",
    change_24h: null,
    change_24h_pct: null,
    volume_24h: null,
    performance: { "1D": null, "1W": null, "1M": null, "3M": null, "6M": null, "1Y": null },
  };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

interface Pending {
  ids: string;
  resolve: (r: Response) => void;
}

// installDeferredFetch records every /instruments/stats request and lets
// the test decide when (and in what order) each one answers. It ignores
// the AbortSignal on purpose: a stale response must be rejected by the
// hook's own request token, not merely by the browser cancelling it.
function installDeferredFetch(): Pending[] {
  const pending: Pending[] = [];
  global.fetch = vi.fn((input: string | URL | Request) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname !== "/api/v1/instruments/stats") throw new Error(`unexpected fetch ${url}`);
    return new Promise<Response>((resolve) => pending.push({ ids: url.searchParams.get("ids") ?? "", resolve }));
  }) as unknown as typeof fetch;
  return pending;
}

describe("useInstrumentStats", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("one request for every id, keyed by instrument id", async () => {
    const pending = installDeferredFetch();
    const { result } = renderHook(() => useInstrumentStats([1, 2, 3]));
    await waitFor(() => expect(pending).toHaveLength(1));
    expect(pending[0].ids).toBe("1,2,3");
    await act(async () => pending[0].resolve(jsonResponse([row(1, "10"), row(3, "30")])));
    await waitFor(() => expect(result.current.data?.size).toBe(2));
    expect(result.current.data?.get(3)?.last_close).toBe("30");
    // An id the server omitted (unknown/disabled) is simply absent.
    expect(result.current.data?.get(2)).toBeUndefined();
  });

  it("rapid instrument switch: the previous instrument's late response never shows", async () => {
    const pending = installDeferredFetch();
    const { result, rerender } = renderHook(({ ids }) => useInstrumentStats(ids), {
      initialProps: { ids: [1] },
    });
    await waitFor(() => expect(pending).toHaveLength(1));

    rerender({ ids: [2] });
    await waitFor(() => expect(pending).toHaveLength(2));
    // Immediately after the switch: nothing, not instrument 1's data.
    expect(result.current.data).toBeUndefined();

    // Instrument 2 answers first, then instrument 1's stale answer lands.
    await act(async () => pending[1].resolve(jsonResponse([row(2, "222")])));
    await waitFor(() => expect(result.current.data?.get(2)?.last_close).toBe("222"));
    await act(async () => pending[0].resolve(jsonResponse([row(1, "111")])));
    await new Promise((r) => setTimeout(r, 20));
    expect(result.current.data?.get(1)).toBeUndefined();
    expect(result.current.data?.get(2)?.last_close).toBe("222");
  });

  it("after a switch, the previous instrument's loaded data is not shown while the new one loads", async () => {
    const pending = installDeferredFetch();
    const { result, rerender } = renderHook(({ ids }) => useInstrumentStats(ids), {
      initialProps: { ids: [1] },
    });
    await waitFor(() => expect(pending).toHaveLength(1));
    await act(async () => pending[0].resolve(jsonResponse([row(1, "111")])));
    await waitFor(() => expect(result.current.data?.get(1)?.last_close).toBe("111"));

    rerender({ ids: [2] });
    // Instrument 2's request is still in flight: nothing at all, not
    // instrument 1's rows.
    expect(result.current.data).toBeUndefined();
    expect(result.current.loading).toBe(true);
  });

  it("switching back and forth: an old response for the same id set still loses to the newest", async () => {
    const pending = installDeferredFetch();
    const { result, rerender } = renderHook(({ ids }) => useInstrumentStats(ids), {
      initialProps: { ids: [1] },
    });
    await waitFor(() => expect(pending).toHaveLength(1));
    rerender({ ids: [2] });
    await waitFor(() => expect(pending).toHaveLength(2));
    rerender({ ids: [1] });
    await waitFor(() => expect(pending).toHaveLength(3));

    // Newest request for [1] answers, then the FIRST request for [1]
    // (same ids, older token) answers with older numbers.
    await act(async () => pending[2].resolve(jsonResponse([row(1, "new")])));
    await waitFor(() => expect(result.current.data?.get(1)?.last_close).toBe("new"));
    await act(async () => pending[0].resolve(jsonResponse([row(1, "old")])));
    await act(async () => pending[1].resolve(jsonResponse([row(2, "two")])));
    await new Promise((r) => setTimeout(r, 20));
    expect(result.current.data?.get(1)?.last_close).toBe("new");
    expect(result.current.data?.get(2)).toBeUndefined();
  });

  it("refreshes on its timer and when refreshKey changes", async () => {
    const pending = installDeferredFetch();
    const { result, rerender } = renderHook(({ key }) => useInstrumentStats([1], key, 80), {
      initialProps: { key: null as number | null },
    });
    await waitFor(() => expect(pending).toHaveLength(1));
    await act(async () => pending[0].resolve(jsonResponse([row(1, "1")])));

    // Timer: another request within a couple of intervals.
    await waitFor(() => expect(pending.length).toBeGreaterThanOrEqual(2), { timeout: 500 });
    await act(async () => pending[pending.length - 1].resolve(jsonResponse([row(1, "2")])));
    await waitFor(() => expect(result.current.data?.get(1)?.last_close).toBe("2"));

    // A closed candle (new refreshKey) fetches immediately.
    const before = pending.length;
    rerender({ key: 41 });
    await waitFor(() => expect(pending.length).toBe(before + 1));
  });

  it("a failed refresh keeps the last good data for the same ids and reports the error", async () => {
    let calls = 0;
    global.fetch = vi.fn(() => {
      calls++;
      if (calls === 1) return Promise.resolve(jsonResponse([row(1, "5")]));
      return Promise.resolve(jsonResponse({ error: { code: "internal_error", message: "boom" } }, 500));
    }) as unknown as typeof fetch;
    const { result, rerender } = renderHook(({ key }) => useInstrumentStats([1], key), {
      initialProps: { key: 0 },
    });
    await waitFor(() => expect(result.current.data?.get(1)?.last_close).toBe("5"));
    rerender({ key: 1 });
    await waitFor(() => expect(result.current.error).toBeDefined());
    expect(result.current.data?.get(1)?.last_close).toBe("5");
  });

  it("no ids: no request, empty data", async () => {
    global.fetch = vi.fn() as unknown as typeof fetch;
    const { result } = renderHook(() => useInstrumentStats([]));
    await waitFor(() => expect(result.current.data?.size).toBe(0));
    expect(global.fetch).not.toHaveBeenCalled();
  });
});
