// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { AuthProvider } from "@/auth/auth-context";
import { TooltipProvider } from "@/components/ui/tooltip";
import { DrawingProvider } from "@/components/chart/drawing-context";
import { AnalysisWorkspaceProvider } from "@/components/workspace/analysis-workspace";
import { CommandPaletteProvider } from "@/components/workspace/command-palette";
import { ArgusActivityProvider } from "@/components/workspace/argus-activity-feed";
import { ChartToolbar } from "../chart-toolbar";
import type { ApiAuthUser, ApiInstrument } from "@/api/types";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const BASE_USER: ApiAuthUser = {
  id: 1,
  email: "a@example.com",
  email_verified: true,
  created_at: "2026-01-01T00:00:00Z",
  display_name: null,
  onboarding_completed: true,
};

function makeInstrument(provider: string): ApiInstrument {
  return {
    id: 7,
    provider,
    native_symbol: "BTC",
    asset_class: "crypto_perp",
    base_asset: "BTC",
    quote_asset: "USD",
    price_scale: 1,
    qty_scale: 5,
    tick_size: "0.1",
    supported_intervals: ["1m"],
    enabled: true,
  };
}

function installFetchMock(me: ApiAuthUser, instruments: ApiInstrument[] = []) {
  global.fetch = vi.fn((input: string | URL | Request) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/auth/me") return Promise.resolve(jsonResponse(me));
    if (url.pathname === "/api/v1/instruments") return Promise.resolve(jsonResponse(instruments));
    if (url.pathname === "/api/v1/setups") return Promise.resolve(jsonResponse([]));
    if (url.pathname === "/api/v1/instruments/stats") return Promise.resolve(jsonResponse([]));
    if (/^\/api\/v1\/instruments\/\d+\/candles$/.test(url.pathname)) {
      return Promise.resolve(jsonResponse({ candles: [], last_closed_open_time: null, last_closed_revision: null }));
    }
    throw new Error(`unexpected fetch: ${url.toString()}`);
  }) as unknown as typeof fetch;
}

function renderToolbar() {
  return render(
    <MemoryRouter initialEntries={["/analysis"]}>
      <TooltipProvider delayDuration={150}>
        <AuthProvider>
          <AnalysisWorkspaceProvider>
            <DrawingProvider>
              <CommandPaletteProvider>
                <ArgusActivityProvider>
                  <ChartToolbar />
                </ArgusActivityProvider>
              </CommandPaletteProvider>
            </DrawingProvider>
          </AnalysisWorkspaceProvider>
        </AuthProvider>
      </TooltipProvider>
    </MemoryRouter>,
  );
}

describe("ChartToolbar account avatar", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("shows the first letter of display_name, uppercased, when set", async () => {
    installFetchMock({ ...BASE_USER, display_name: "kevyn" });
    renderToolbar();
    expect(await screen.findByText("K")).toBeInTheDocument();
  });

  it("falls back to the first letter of the email when there is no display_name", async () => {
    installFetchMock({ ...BASE_USER, display_name: null, email: "zed@example.com" });
    renderToolbar();
    expect(await screen.findByText("Z")).toBeInTheDocument();
  });

  it("shows a placeholder before the user loads", () => {
    global.fetch = vi.fn(() => new Promise<Response>(() => {})) as unknown as typeof fetch;
    const { container } = renderToolbar();
    const avatar = container.querySelector(".bg-surface-2.rounded-full, .rounded-full.bg-surface-2");
    expect(avatar).toHaveTextContent("?");
  });
});

// Replay plays back sample (fixture) analysis steps, so it exists only
// for an instrument whose own provider is "fixture" (real-panel-data
// brief rule 8) -- decided by the selected instrument, not build mode.
describe("ChartToolbar Replay", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("is shown for a fixture-provider instrument", async () => {
    installFetchMock(BASE_USER, [makeInstrument("fixture")]);
    renderToolbar();
    expect(await screen.findByRole("button", { name: /Replay/ })).toBeInTheDocument();
  });

  it("is hidden for a real-provider instrument", async () => {
    installFetchMock(BASE_USER, [makeInstrument("hyperliquid")]);
    renderToolbar();
    // Wait until the instrument has resolved into the symbol picker.
    expect(await screen.findByText("BTC")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Replay/ })).toBeNull();
  });
});
