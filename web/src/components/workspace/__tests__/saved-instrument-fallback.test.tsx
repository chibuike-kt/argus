// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { AnalysisWorkspaceProvider, useAnalysisWorkspace } from "../analysis-workspace";
import { preferences } from "@/lib/preferences";
import type { ApiInstrument } from "@/api/types";

// Phase 2 (D2): fixture instruments are disabled by the operator, so a user
// whose saved instrument was one of them must land on a sensible default --
// the first enabled instrument on their own watchlist -- with no error
// screen, and the saved preference corrected so it does not happen again.

function inst(id: number, symbol: string): ApiInstrument {
  return {
    id, provider: "hyperliquid", native_symbol: symbol, asset_class: "crypto_perp", base_asset: symbol,
    quote_asset: "USD", price_scale: 1, qty_scale: 5, tick_size: "0.1", supported_intervals: ["1m"], enabled: true,
  };
}

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

function installServer(savedId: number) {
  const prefPuts: Record<string, unknown>[] = [];
  global.fetch = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const method = init?.method ?? "GET";
    if (url.pathname === "/api/v1/me/preferences" && method === "GET") return json({ data: { selected_instrument_id: savedId }, version: 3 });
    if (url.pathname === "/api/v1/me/preferences" && method === "PUT") {
      const body = JSON.parse(String(init?.body)) as { data: Record<string, unknown>; version: number };
      prefPuts.push(body.data);
      return json({ data: body.data, version: body.version + 1 });
    }
    // Enabled instruments only: 3 (BTC) and 5 (ETH). 99 (an old fixture
    // instrument) is disabled, so it is not in this list.
    if (url.pathname === "/api/v1/instruments") return json([inst(3, "BTC"), inst(5, "ETH")]);
    if (url.pathname === "/api/v1/watchlist") return json([{ instrument_id: 99, position: 0 }, { instrument_id: 5, position: 1 }, { instrument_id: 3, position: 2 }]);
    if (url.pathname === "/api/v1/setups") return json([]);
    if (url.pathname === "/api/v1/instruments/stats") return json([]);
    if (url.pathname.endsWith("/candles")) return json({ candles: [], last_closed_open_time: null, last_closed_revision: null });
    throw new Error(`unexpected fetch ${url}`);
  }) as unknown as typeof fetch;
  return prefPuts;
}

function Probe() {
  const { instrument, instrumentNotFound } = useAnalysisWorkspace();
  return <div data-testid="probe" data-instrument={instrument?.native_symbol ?? ""} data-not-found={String(instrumentNotFound)} />;
}

describe("a saved instrument that is now disabled", () => {
  afterEach(() => {
    cleanup();
    preferences.detach();
    vi.restoreAllMocks();
  });

  it("lands on the first enabled watchlist item and corrects the saved preference", async () => {
    const puts = installServer(99);
    preferences.attach(42); // the signed-in user; pulls the server copy (saved id 99)
    render(
      <MemoryRouter initialEntries={["/analysis"]}>
        <AnalysisWorkspaceProvider>
          <Probe />
        </AnalysisWorkspaceProvider>
      </MemoryRouter>,
    );
    // Watchlist order is 99 (disabled, skipped), then 5 (ETH): ETH, not the
    // first instrument in the list (BTC), and no "not found" error.
    await waitFor(() => expect(screen.getByTestId("probe")).toHaveAttribute("data-instrument", "ETH"), { timeout: 5000 });
    expect(screen.getByTestId("probe")).toHaveAttribute("data-not-found", "false");
    // The saved preference is corrected on the server to 5.
    await waitFor(() => expect(puts.at(-1)?.selected_instrument_id).toBe(5), { timeout: 5000 });
  });
});
