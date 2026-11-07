// @vitest-environment jsdom
import { describe, expect, it, vi, afterEach } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { Setups } from "../setups";
import type { ApiInstrument, ApiSetup } from "@/api/types";

// Closes the gap the previous SSR-only smoke test (react-dom/server)
// left explicitly documented: effects never run during server
// rendering, so the empty/error/success branches (everything after the
// first fetch resolves) were never reachable. jsdom + @testing-library/
// react are now approved dependencies (web-wiring-followup brief, Part
// 6), so this replaces that smoke test with the real thing.

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const instrument: ApiInstrument = {
  id: 1,
  provider: "test-provider",
  native_symbol: "BTC-PERP",
  asset_class: "crypto_perp",
  base_asset: "BTC",
  quote_asset: "USDT",
  price_scale: 2,
  qty_scale: 4,
  tick_size: "0.01",
  supported_intervals: ["1m"],
  enabled: true,
};

function makeApiSetup(overrides: Partial<ApiSetup> = {}): ApiSetup {
  return {
    id: 7,
    instrument_id: instrument.id,
    timeframe: "1m",
    poi_type: "order_block",
    poi_source_candle_id: 1,
    direction: "bullish",
    strategy_version: "v1",
    state: "triggered",
    poi_source_candle_revision: 1,
    plan_snapshot: {
      status: "triggered",
      direction: "bullish",
      entry: "90.00",
      stop: "88.00",
      targets: [{ level: "94.00", rr: "2/1" }],
      poi: {
        type: "order_block",
        direction: "bullish",
        low: "88.00",
        high: "90.00",
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
    last_event_id: 1,
    evaluation_time: "2026-01-01T00:10:00Z",
    algorithm_version: "v1",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:10:00Z",
    ...overrides,
  };
}

// Routes a fetch mock by URL shape, the same way the real API client
// builds URLs (api/client.ts): /api/v1/instruments and
// /api/v1/setups?instrument_id=...&state=....
function installFetchMock(opts: {
  instruments?: ApiInstrument[];
  instrumentsError?: boolean;
  setupsFor?: (url: URL) => ApiSetup[];
  setupsError?: boolean;
}) {
  const fetchMock = vi.fn((input: string | URL | Request) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/instruments") {
      if (opts.instrumentsError) return Promise.resolve(jsonResponse({ error: { code: "internal_error", message: "boom" } }, 500));
      return Promise.resolve(jsonResponse(opts.instruments ?? []));
    }
    if (url.pathname === "/api/v1/setups") {
      if (opts.setupsError) return Promise.resolve(jsonResponse({ error: { code: "internal_error", message: "boom" } }, 500));
      return Promise.resolve(jsonResponse(opts.setupsFor ? opts.setupsFor(url) : []));
    }
    throw new Error(`unexpected fetch: ${url.toString()}`);
  });
  global.fetch = fetchMock as unknown as typeof fetch;
  return fetchMock;
}

function renderSetups() {
  return render(
    <MemoryRouter>
      <Setups />
    </MemoryRouter>,
  );
}

describe("Setups route", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("shows the loading state before any fetch resolves", async () => {
    // A deliberately held-open, manually-settled instruments fetch --
    // not installFetchMock's own neverResolveSetups path, which (by
    // design) never settles even past unmount/abort. That left a truly
    // eternal pending promise/registered abort listener alive for the
    // rest of the process once this test's own cleanup() unmounted the
    // component, which turned out to intermittently corrupt whichever
    // async test ran right after it in the same file -- resolving this
    // one before the test ends avoids that category of problem
    // entirely, rather than chasing the exact mechanism further.
    let resolveInstruments!: (v: Response) => void;
    global.fetch = vi.fn((input: string | URL | Request) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/v1/instruments") {
        return new Promise<Response>((resolve) => {
          resolveInstruments = resolve;
        });
      }
      throw new Error(`unexpected fetch: ${url.toString()}`);
    }) as unknown as typeof fetch;

    renderSetups();
    // "Loading…" appears twice while loading (the header subtitle and
    // the table's own spinner row) -- both are the loading state, so
    // asserting at least one match is the right check, not a sign two
    // separate things need disambiguating.
    expect(screen.getAllByText(/Loading/).length).toBeGreaterThan(0);

    resolveInstruments(jsonResponse([]));
    await screen.findByText(/No setups yet/);
  });

  it("shows the empty state once fetches resolve with no setups", async () => {
    installFetchMock({ instruments: [instrument], setupsFor: () => [] });
    renderSetups();
    expect(await screen.findByText(/No setups yet/)).toBeInTheDocument();
  });

  it("shows the error state when a fetch fails", async () => {
    installFetchMock({ instruments: [instrument], setupsError: true });
    renderSetups();
    expect(await screen.findByText(/Could not load setups/)).toBeInTheDocument();
  });

  it("renders a row for each setup once data resolves", async () => {
    installFetchMock({ instruments: [instrument], setupsFor: () => [makeApiSetup()] });
    renderSetups();
    expect(await screen.findByText("BTC")).toBeInTheDocument();
    expect(screen.getByText("Order block")).toBeInTheDocument();
  });

  it("sends the selected state filter as the setups request's own state param", async () => {
    const fetchMock = installFetchMock({ instruments: [instrument], setupsFor: () => [makeApiSetup()] });
    renderSetups();
    await screen.findByText("BTC");

    await userEvent.click(screen.getByRole("button", { name: "Triggered" }));

    await waitFor(() => {
      const setupsCalls = fetchMock.mock.calls
        .map(([input]) => new URL(String(input), "http://localhost"))
        .filter((u) => u.pathname === "/api/v1/setups");
      expect(setupsCalls.some((u) => u.searchParams.get("state") === "triggered")).toBe(true);
    });
  });

  it("omits the state param for the All filter", async () => {
    const fetchMock = installFetchMock({ instruments: [instrument], setupsFor: () => [makeApiSetup()] });
    renderSetups();
    await screen.findByText("BTC");

    const setupsCalls = fetchMock.mock.calls
      .map(([input]) => new URL(String(input), "http://localhost"))
      .filter((u) => u.pathname === "/api/v1/setups");
    expect(setupsCalls.every((u) => u.searchParams.get("state") === null)).toBe(true);
  });
});

describe("Setups route (multi-row filtering)", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("shows only rows the backend returned for the active filter (server-side filtering, not client-side)", async () => {
    const triggered = makeApiSetup({ id: 7 });
    installFetchMock({
      instruments: [instrument],
      setupsFor: (url) => (url.searchParams.get("state") === "triggered" ? [triggered] : []),
    });
    render(
      <MemoryRouter>
        <Setups />
      </MemoryRouter>,
    );
    // Default filter is "all", with no state param -- this mock returns
    // [] for that case, proving the component renders exactly what the
    // (mocked) server sent, not a client-side re-filter of a fixed list.
    expect(await screen.findByText(/No setups yet/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Triggered" }));
    expect(await screen.findByText("BTC")).toBeInTheDocument();
    expect(screen.queryByText(/No setups yet/)).toBeNull();
  });
});
