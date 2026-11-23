// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { AnalysisWorkspaceProvider, useAnalysisWorkspace } from "../analysis-workspace";
import { getWatchlistStore, resetWatchlistStoreForTests } from "@/lib/watchlist-store";
import type { ApiInstrument } from "@/api/types";

// Phase 4.1a: a watchlist add may enable an instrument (D15.1) that the
// workspace's enabled-instrument list, read at load, does not have. After
// the add the workspace reads the list again, so selecting the new row
// finds it instead of "instrument not found".

function inst(id: number, symbol: string): ApiInstrument {
  return {
    id, provider: "hyperliquid", native_symbol: symbol, asset_class: "crypto_perp", base_asset: symbol,
    quote_asset: "USD", price_scale: 1, qty_scale: 5, tick_size: "0.1", supported_intervals: ["1m"], enabled: true,
  };
}
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

let enabled: ApiInstrument[];
let watchlist: number[];
let instrumentReads: number;

beforeEach(() => {
  resetWatchlistStoreForTests();
  enabled = [inst(3, "BTC")];
  watchlist = [3];
  instrumentReads = 0;
  global.fetch = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const method = init?.method ?? "GET";
    if (url.pathname === "/api/v1/instruments") {
      instrumentReads++;
      return json(enabled);
    }
    if (url.pathname === "/api/v1/watchlist") return json(watchlist.map((instrument_id, position) => ({ instrument_id, position })));
    if (url.pathname === "/api/v1/watchlist/items" && method === "POST") {
      const { instrument_id } = JSON.parse(String(init?.body));
      enabled = [...enabled, inst(instrument_id, "AVAX")]; // the server enabled it
      watchlist = [...watchlist, instrument_id];
      return json(watchlist.map((id, position) => ({ instrument_id: id, position })));
    }
    if (url.pathname === "/api/v1/me/preferences") return json({ data: {}, version: 1 });
    if (url.pathname === "/api/v1/setups") return json([]);
    if (url.pathname === "/api/v1/instruments/stats") return json([]);
    if (url.pathname.endsWith("/candles")) return json({ candles: [], last_closed_open_time: null, last_closed_revision: null });
    throw new Error(`unexpected ${method} ${url}`);
  }) as unknown as typeof fetch;
});

afterEach(() => cleanup());

function Probe() {
  const { instrument, instrumentNotFound, instrumentsLoading } = useAnalysisWorkspace();
  return (
    <div
      data-testid="probe"
      data-instrument={instrument?.native_symbol ?? ""}
      data-not-found={String(instrumentNotFound)}
      data-loading={String(instrumentsLoading)}
    />
  );
}

describe("workspace after a watchlist add", () => {
  it("reads the enabled instruments again, without a loading state, so the new row resolves", async () => {
    render(
      <MemoryRouter initialEntries={["/analysis?instrumentId=40"]}>
        <AnalysisWorkspaceProvider>
          <Probe />
        </AnalysisWorkspaceProvider>
      </MemoryRouter>,
    );
    const probe = screen.getByTestId("probe");
    await waitFor(() => expect(probe).toHaveAttribute("data-not-found", "true"));
    expect(instrumentReads).toBe(1);

    const loadingSeen: string[] = [];
    const observer = new MutationObserver(() => loadingSeen.push(probe.getAttribute("data-loading") ?? ""));
    observer.observe(probe, { attributes: true, attributeFilter: ["data-loading"] });
    await act(async () => {
      expect(await getWatchlistStore().add(40)).toBe(true);
    });
    await waitFor(() => expect(probe).toHaveAttribute("data-instrument", "AVAX"));
    expect(probe).toHaveAttribute("data-not-found", "false");
    expect(instrumentReads).toBe(2);
    observer.disconnect();
    expect(loadingSeen).not.toContain("true");
  });
});
