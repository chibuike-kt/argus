// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { Onboarding } from "../onboarding";
import { AuthProvider } from "@/auth/auth-context";
import type { ApiAuthUser } from "@/api/types";

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
  onboarding_completed: false,
};

const INSTRUMENT = {
  id: 7,
  provider: "test-provider",
  native_symbol: "BTCUSDT.P",
  asset_class: "crypto_perp",
  base_asset: "BTC",
  quote_asset: "USDT",
  price_scale: 2,
  qty_scale: 4,
  tick_size: "0.01",
  supported_intervals: ["1m"],
  enabled: true,
};

function installFetchMock(opts: {
  me: ApiAuthUser;
  watchlist?: { instrument_id: number; position: number }[];
  completeOnboarding?: ApiAuthUser;
}) {
  const fetchMock = vi.fn((input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const method = init?.method ?? "GET";
    if (url.pathname === "/api/v1/auth/me") return Promise.resolve(jsonResponse(opts.me));
    if (url.pathname === "/api/v1/instruments") return Promise.resolve(jsonResponse([INSTRUMENT]));
    if (url.pathname === "/api/v1/me/profile" && method === "PUT") {
      const body = JSON.parse(String(init?.body));
      return Promise.resolve(jsonResponse({ ...opts.me, display_name: body.display_name }));
    }
    if (url.pathname === "/api/v1/watchlist" && method === "GET") {
      return Promise.resolve(jsonResponse(opts.watchlist ?? []));
    }
    if (url.pathname === "/api/v1/watchlist" && method === "PUT") {
      const body = JSON.parse(String(init?.body));
      return Promise.resolve(
        jsonResponse(body.instrument_ids.map((id: number, i: number) => ({ instrument_id: id, position: i }))),
      );
    }
    if (url.pathname === "/api/v1/me/onboarding/complete" && method === "POST") {
      return Promise.resolve(jsonResponse(opts.completeOnboarding ?? { ...opts.me, onboarding_completed: true }));
    }
    throw new Error(`unexpected fetch: ${method} ${url.toString()}`);
  });
  global.fetch = fetchMock as unknown as typeof fetch;
  return fetchMock;
}

function renderOnboarding() {
  return render(
    <MemoryRouter initialEntries={["/onboarding"]}>
      <AuthProvider>
        <Routes>
          <Route path="/onboarding" element={<Onboarding />} />
          <Route path="/setups" element={<div>SETUPS PAGE</div>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe("Onboarding route", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("walks display name -> watchlist -> /setups on the happy path", async () => {
    installFetchMock({ me: BASE_USER });
    renderOnboarding();

    await userEvent.type(await screen.findByLabelText(/Display name/), "Ada");
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));

    await screen.findByText(/Pick a few pairs to watch/);
    await userEvent.click(await screen.findByText("BTCUSDT"));
    await userEvent.click(screen.getByRole("button", { name: "Finish" }));

    expect(await screen.findByText("SETUPS PAGE")).toBeInTheDocument();
  });

  it("starts at the watchlist step when display_name is already set", async () => {
    installFetchMock({ me: { ...BASE_USER, display_name: "Ada" } });
    renderOnboarding();

    expect(await screen.findByText(/Pick a few pairs to watch/)).toBeInTheDocument();
  });

  it("redirects to /setups immediately when onboarding is already complete", async () => {
    installFetchMock({ me: { ...BASE_USER, display_name: "Ada", onboarding_completed: true } });
    renderOnboarding();

    expect(await screen.findByText("SETUPS PAGE")).toBeInTheDocument();
  });

  it("keeps Finish disabled until at least one instrument is picked", async () => {
    installFetchMock({ me: { ...BASE_USER, display_name: "Ada" } });
    renderOnboarding();

    await screen.findByText(/Pick a few pairs to watch/);
    expect(screen.getByRole("button", { name: "Finish" })).toBeDisabled();

    await userEvent.click(await screen.findByText("BTCUSDT"));
    expect(screen.getByRole("button", { name: "Finish" })).toBeEnabled();
  });
});
