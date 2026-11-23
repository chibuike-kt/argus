// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SymbolSearchProvider } from "../symbol-search";
import { resetWatchlistStoreForTests } from "@/lib/watchlist-store";
import { resetCatalogForTests } from "@/lib/use-watchlist-tracking";

// The symbol search dialog, keyboard first (Phase 4.1a). Waits for values,
// never a one-shot check after finding an element.

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}
const perp = (id: number, sym: string, enabled: boolean) => ({
  id, provider: "hyperliquid", native_symbol: sym, asset_class: "crypto_perp", base_asset: sym, quote_asset: "USD",
  price_scale: 2, qty_scale: 2, tick_size: "0.01", supported_intervals: ["1m"], enabled,
});

let server: number[];
let posted: number[];
let catalogFails: number;
let floorCount: number;

beforeEach(() => {
  resetWatchlistStoreForTests();
  resetCatalogForTests();
  server = [1];
  posted = [];
  catalogFails = 0;
  floorCount = 0;
  global.fetch = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const method = init?.method ?? "GET";
    if (url.pathname === "/api/v1/instruments") {
      if (catalogFails > 0) {
        catalogFails--;
        return json({ error: { code: "internal", message: "catalog down" } }, 500);
      }
      return json([perp(1, "BTC", true), perp(2, "AVAX", false), perp(3, "AAVE", false), perp(4, "ETH", true)]);
    }
    if (url.pathname === "/api/v1/watchlist") return json(server.map((instrument_id, position) => ({ instrument_id, position })));
    if (url.pathname === "/api/v1/watchlist/tracking") {
      return json({ can_enable: true, cap: 8, remaining: 7 - floorCount, tracked_instrument_ids: [1], items: server.map((id) => ({ instrument_id: id, tracked: true, state: "live" })) });
    }
    if (url.pathname === "/api/v1/watchlist/items" && method === "POST") {
      const { instrument_id } = JSON.parse(String(init?.body));
      posted.push(instrument_id);
      server = [...server, instrument_id];
      return json(server.map((id, position) => ({ instrument_id: id, position })));
    }
    throw new Error(`unexpected ${method} ${url}`);
  }) as unknown as typeof fetch;
});

afterEach(() => cleanup());

function renderApp() {
  return render(
    <SymbolSearchProvider>
      <button type="button" data-testid="opener">
        somewhere
      </button>
    </SymbolSearchProvider>,
  );
}

describe("symbol search dialog", () => {
  it("Ctrl+K opens it focused; arrows move; Enter adds the highlighted symbol; Esc closes and restores focus", async () => {
    const user = userEvent.setup();
    renderApp();
    screen.getByTestId("opener").focus();
    await user.keyboard("{Control>}k{/Control}");
    const input = await screen.findByTestId("symbol-search-input");
    await waitFor(() => expect(document.activeElement).toBe(input));
    await waitFor(() => expect(screen.getByTestId("symbol-search-row-AVAX")).toBeInTheDocument());
    await waitFor(() => expect(screen.getByTestId("symbol-search-added-BTC")).toHaveTextContent("Added"));
    await waitFor(() => expect(screen.getByTestId("symbol-search-cap")).toHaveTextContent("7 more symbols can be added"));

    await user.type(input, "a");
    // "a": AAVE and AVAX start with it; ETH does not contain it.
    await waitFor(() => expect(screen.queryByTestId("symbol-search-row-ETH")).toBeNull());
    await user.keyboard("{ArrowDown}");
    await waitFor(() => expect(screen.getByTestId("symbol-search-row-AVAX")).toHaveAttribute("aria-selected", "true"));
    await user.keyboard("{Enter}");
    await waitFor(() => expect(posted).toEqual([2]));
    await waitFor(() => expect(screen.getByTestId("symbol-search-added-AVAX")).toHaveTextContent("Added"));
    await waitFor(() => expect(screen.getByTestId("symbol-search-status")).toHaveTextContent("Added AVAX to your watchlist"));
    await waitFor(() => expect(screen.getByTestId("symbol-search-count")).toHaveTextContent("2 of 50"));

    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByTestId("symbol-search")).toBeNull());
    expect(document.activeElement).toBe(screen.getByTestId("opener"));
  });

  it("shows the error with a working Retry, then the empty state for no match", async () => {
    catalogFails = 1;
    const user = userEvent.setup();
    renderApp();
    await user.keyboard("{Control>}k{/Control}");
    await waitFor(() => expect(screen.getByTestId("symbol-search-error")).toHaveTextContent("catalog down"));
    await user.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(screen.getByTestId("symbol-search-row-BTC")).toBeInTheDocument());
    await user.type(screen.getByTestId("symbol-search-input"), "zzz");
    await waitFor(() => expect(screen.getByTestId("symbol-search-empty")).toHaveTextContent("Nothing matches 'zzz'"));
  });

  it("with no slot left (the floor counted by the server), Add is disabled with the reason", async () => {
    floorCount = 7; // remaining 0
    const user = userEvent.setup();
    renderApp();
    await user.keyboard("{Control>}k{/Control}");
    await waitFor(() => expect(screen.getByTestId("symbol-search-cap")).toHaveTextContent("no more symbols can be added"));
    const add = screen.getByTestId("symbol-search-add-AVAX");
    expect(add).toBeDisabled();
    expect(add).toHaveAttribute("title", expect.stringMatching(/no more symbols can be added\. Remove a symbol from every watchlist/));
    await user.click(add);
    expect(posted).toEqual([]);
  });
});
