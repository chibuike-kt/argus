// @vitest-environment jsdom
//
// Regression tests for the real bug this fixes: analysis-workspace.tsx
// used to derive `instrument` from `setup?.instrument_id` alone, so an
// instrument with zero setups could never be selected at all (`setup`
// resolves to undefined -- list[0] of an empty array -- so `instrument`
// came out undefined too, the symbol picker stayed on "Select
// instrument" forever, and useLiveCandles never subscribed to
// anything). These tests exercise the real AnalysisWorkspaceProvider
// end to end (real fetch mocked by URL, no mocked useAnalysisWorkspace)
// so they fail against the old `setup?.instrument_id` derivation and
// pass against the new, independent resolution.
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, useNavigate } from "react-router-dom";
import { AnalysisWorkspaceProvider, useAnalysisWorkspace } from "../analysis-workspace";
import type { ApiInstrument, ApiSetup } from "@/api/types";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function instrument(id: number, symbol: string): ApiInstrument {
  return {
    id,
    provider: "hyperliquid",
    native_symbol: symbol,
    asset_class: "crypto_perp",
    base_asset: symbol,
    quote_asset: "USD",
    price_scale: 2,
    qty_scale: 4,
    tick_size: "0.01",
    supported_intervals: ["1m", "5m", "15m", "1h", "4h", "1d"],
    enabled: true,
  };
}

function setup(id: number, instrumentId: number): ApiSetup {
  return {
    id,
    instrument_id: instrumentId,
    timeframe: "1m",
    poi_type: "order_block",
    poi_source_candle_id: 1,
    direction: "bullish",
    strategy_version: "v1",
    state: "actionable",
    poi_source_candle_revision: 1,
    plan_snapshot: {
      status: "active",
      direction: "bullish",
      entry: "100.00",
      stop: "95.00",
      targets: [{ level: "110.00", rr: "2/1" }],
      poi: {
        type: "order_block",
        direction: "bullish",
        low: "98.00",
        high: "99.00",
        order_block_candle_id: 1,
        order_block_candle_revision: 1,
        order_block_open_time: "2026-01-01T00:00:00Z",
        qmr_fail_candle_id: 0,
        qmr_fail_candle_revision: 0,
        structural_swing_id: 1,
        in_discount: true,
      },
      protected_swing_id: 1,
    },
    last_event_id: null,
    evaluation_time: "2026-01-01T00:01:00Z",
    algorithm_version: "v1",
    created_at: "2026-01-01T00:01:00Z",
    updated_at: "2026-01-01T00:01:00Z",
  };
}

// BTC (1) has a setup; ETH (2) has none at all -- the exact shape of
// the real bug report (the Hyperliquid instruments had no setups yet).
const BTC = instrument(1, "BTC");
const ETH = instrument(2, "ETH");
const BTC_SETUP = setup(10, 1);

function installFetchMock() {
  global.fetch = vi.fn((input: string | URL | Request) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/instruments") return Promise.resolve(jsonResponse([BTC, ETH]));
    if (url.pathname === "/api/v1/setups") {
      const instrumentId = Number(url.searchParams.get("instrument_id"));
      if (instrumentId === BTC.id) return Promise.resolve(jsonResponse([BTC_SETUP]));
      return Promise.resolve(jsonResponse([])); // ETH: no setups
    }
    if (url.pathname === `/api/v1/setups/${BTC_SETUP.id}`) return Promise.resolve(jsonResponse(BTC_SETUP));
    if (url.pathname.startsWith("/api/v1/setups/") && url.pathname.endsWith("/events")) {
      return Promise.resolve(jsonResponse([]));
    }
    if (url.pathname.startsWith("/api/v1/instruments/") && url.pathname.endsWith("/candles")) {
      return Promise.resolve(jsonResponse({ candles: [], last_closed_open_time: null, last_closed_revision: null }));
    }
    throw new Error(`unexpected fetch: ${url.toString()}`);
  }) as unknown as typeof fetch;
}

// Renders the resolved context fields as data attributes a test can
// read directly -- no need to mount the whole workspace UI just to
// prove the resolution logic itself.
function Probe() {
  const { instrument, instrumentNotFound, setup, instrumentsLoading } = useAnalysisWorkspace();
  return (
    <div
      data-testid="probe"
      data-instrument-id={instrument?.id ?? ""}
      data-instrument-symbol={instrument?.native_symbol ?? ""}
      data-instrument-not-found={String(instrumentNotFound)}
      data-setup-id={setup?.id ?? ""}
      data-instruments-loading={String(instrumentsLoading)}
    />
  );
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <AnalysisWorkspaceProvider>
        <Probe />
      </AnalysisWorkspaceProvider>
    </MemoryRouter>,
  );
}

// Waits for the actual expected value, not a generic "loading flags
// are false" proxy -- instrumentsLoading turning false and the
// resolvedInstrumentId effect committing are two separate renders
// (the fetch's own .then()/.finally() chain resolves across separate
// microtasks), so asserting against a stale DOM snapshot right after
// a loading-flag-only wait is flaky. This waits on the specific field
// each test actually cares about.
async function waitForInstrumentId(expected: string) {
  await waitFor(() => {
    expect(screen.getByTestId("probe").dataset.instrumentId).toBe(expected);
  });
}
async function waitForNotFound(expected: string) {
  await waitFor(() => {
    expect(screen.getByTestId("probe").dataset.instrumentNotFound).toBe(expected);
  });
}

describe("AnalysisWorkspaceProvider instrument resolution", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("resolves an instrumentId in the URL for an instrument with NO setups (the real bug)", async () => {
    installFetchMock();
    renderAt("/analysis?instrumentId=2");
    await waitForInstrumentId("2");
    const probe = screen.getByTestId("probe");
    expect(probe.dataset.instrumentSymbol).toBe("ETH");
    expect(probe.dataset.instrumentNotFound).toBe("false");
    expect(probe.dataset.setupId).toBe(""); // no setup -- and that must not block instrument resolution
  });

  it("resolves the first instrument by default even when it has no setups", async () => {
    // ETH listed first this time -- "the first instrument" must not
    // secretly mean "the first instrument WITH a setup".
    global.fetch = vi.fn((input: string | URL | Request) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/v1/instruments") return Promise.resolve(jsonResponse([ETH, BTC]));
      if (url.pathname === "/api/v1/setups") return Promise.resolve(jsonResponse([]));
      if (url.pathname.startsWith("/api/v1/instruments/") && url.pathname.endsWith("/candles")) {
        return Promise.resolve(jsonResponse({ candles: [], last_closed_open_time: null, last_closed_revision: null }));
      }
      throw new Error(`unexpected fetch: ${url.toString()}`);
    }) as unknown as typeof fetch;
    renderAt("/analysis");
    await waitForInstrumentId(String(ETH.id));
    expect(screen.getByTestId("probe").dataset.instrumentSymbol).toBe("ETH");
  });

  it("flags an instrumentId that does not exist (or is disabled) as instrumentNotFound, not just 'no instrument'", async () => {
    installFetchMock();
    renderAt("/analysis?instrumentId=999");
    await waitForNotFound("true");
    expect(screen.getByTestId("probe").dataset.instrumentId).toBe("");
  });

  it("setup wins when setupId and instrumentId in the URL disagree", async () => {
    installFetchMock();
    // setupId=10 belongs to BTC (instrument 1); instrumentId=2 (ETH) disagrees.
    renderAt("/analysis?setupId=10&instrumentId=2");
    await waitForInstrumentId("1");
    const probe = screen.getByTestId("probe");
    expect(probe.dataset.instrumentSymbol).toBe("BTC");
    expect(probe.dataset.setupId).toBe("10");
  });

  it("rapid switching between three instruments resolves to the last one requested, never a stale one", async () => {
    const THIRD = instrument(3, "SOL");
    global.fetch = vi.fn((input: string | URL | Request) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/v1/instruments") return Promise.resolve(jsonResponse([BTC, ETH, THIRD]));
      if (url.pathname === "/api/v1/setups") return Promise.resolve(jsonResponse([]));
      if (url.pathname.startsWith("/api/v1/instruments/") && url.pathname.endsWith("/candles")) {
        return Promise.resolve(jsonResponse({ candles: [], last_closed_open_time: null, last_closed_revision: null }));
      }
      throw new Error(`unexpected fetch: ${url.toString()}`);
    }) as unknown as typeof fetch;

    // A real client-side navigation (useNavigate), not a fresh
    // MemoryRouter per render -- initialEntries is only consumed once
    // at construction, so re-passing a different value on rerender()
    // would not actually navigate the SAME router instance the way a
    // real rapid symbol-picker switch does.
    let navigate: ((path: string) => void) | null = null;
    function Harness() {
      navigate = useNavigate();
      return <Probe />;
    }
    render(
      <MemoryRouter initialEntries={["/analysis?instrumentId=1"]}>
        <AnalysisWorkspaceProvider>
          <Harness />
        </AnalysisWorkspaceProvider>
      </MemoryRouter>,
    );
    await waitForInstrumentId("1");
    act(() => navigate!("/analysis?instrumentId=2"));
    act(() => navigate!("/analysis?instrumentId=3"));
    await waitForInstrumentId("3");
    expect(screen.getByTestId("probe").dataset.instrumentSymbol).toBe("SOL");
  });
});
