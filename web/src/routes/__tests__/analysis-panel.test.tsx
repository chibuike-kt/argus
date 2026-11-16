// @vitest-environment jsdom
import { describe, expect, it, vi, afterEach } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { TooltipProvider } from "@/components/ui/tooltip";
import { AnalysisWorkspaceProvider } from "@/components/workspace/analysis-workspace";
import { ArgusActivityProvider } from "@/components/workspace/argus-activity-feed";
import { RightPanel } from "@/components/workspace/right-panel";
import type { ApiInstrument, ApiInstrumentStats, ApiSetup, ApiSetupEvent } from "@/api/types";

// This exercises /analysis's data-driven right panel (bias gauge, plan
// card, evidence list, Watchlist tab) through the real
// AnalysisWorkspaceProvider + ArgusActivityProvider, the same providers
// analysis-preview.tsx wraps RightPanel in -- without ChartSurface/
// ChartToolbar/DrawingProvider, which pull in lightweight-charts and
// need real Canvas/ResizeObserver support jsdom does not provide. The
// chart itself is out of scope for a component test at this layer; the
// data binding (setup -> plan card/evidence, Sample markers, the
// fixture toast feed) is what this brief's own Part 6 asks for and is
// exactly what RightPanel alone already covers.

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
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
    id: 1,
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

function makeEvents(setupId: number): ApiSetupEvent[] {
  return [
    {
      id: 1,
      setup_id: setupId,
      event_type: "detected",
      plan_snapshot: makeApiSetup({ id: setupId }).plan_snapshot,
      poi_source_candle_id: 1,
      poi_source_candle_revision: 1,
      evaluation_time: "2026-01-01T00:00:00Z",
      algorithm_version: "v1",
      created_at: "2026-01-01T00:00:00Z",
    },
  ];
}

// candles(n) is a minimal, valid closed-candle series: just enough for
// the provider's own candle effect to resolve without error. The chart
// that would render them is not mounted in this test.
function candles(n = 3) {
  return Array.from({ length: n }, (_, i) => ({
    open_time: `2026-01-01T00:${String(i).padStart(2, "0")}:00Z`,
    close_time: `2026-01-01T00:${String(i + 1).padStart(2, "0")}:00Z`,
    open: "90.00",
    high: "91.00",
    low: "89.00",
    close: "90.50",
    volume: "1.0000",
    is_closed: true,
    revision: 1,
  }));
}

// realStats is a hand-written GET /instruments/stats row for the
// test instrument: about three days of history, so only 1D can exist.
const realStats: ApiInstrumentStats = {
  instrument_id: 1,
  last_close: "95123.40",
  last_closed_open_time: "2026-11-16T18:16:00Z",
  change_24h: "-1234.50",
  change_24h_pct: "-1.28",
  volume_24h: "12345.6789",
  performance: { "1D": "-1.28", "1W": null, "1M": null, "3M": null, "6M": null, "1Y": null },
};

const absentStats: ApiInstrumentStats = {
  instrument_id: 1,
  last_close: null,
  last_closed_open_time: null,
  change_24h: null,
  change_24h_pct: null,
  volume_24h: null,
  performance: { "1D": null, "1W": null, "1M": null, "3M": null, "6M": null, "1Y": null },
};

function installFetchMock(opts: {
  setupsById: Record<number, ApiSetup>;
  eventsBySetupId?: Record<number, ApiSetupEvent[]>;
  setupError?: boolean;
  stats?: ApiInstrumentStats;
}) {
  global.fetch = vi.fn((input: string | URL | Request) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/instruments") {
      return Promise.resolve(jsonResponse([instrument]));
    }
    const setupMatch = /^\/api\/v1\/setups\/(\d+)$/.exec(url.pathname);
    if (setupMatch) {
      if (opts.setupError) return Promise.resolve(jsonResponse({ error: { code: "not_found", message: "boom" } }, 404));
      const id = Number(setupMatch[1]);
      const setup = opts.setupsById[id];
      return setup
        ? Promise.resolve(jsonResponse(setup))
        : Promise.resolve(jsonResponse({ error: { code: "not_found", message: "not found" } }, 404));
    }
    const eventsMatch = /^\/api\/v1\/setups\/(\d+)\/events$/.exec(url.pathname);
    if (eventsMatch) {
      const id = Number(eventsMatch[1]);
      return Promise.resolve(jsonResponse(opts.eventsBySetupId?.[id] ?? []));
    }
    if (url.pathname === "/api/v1/setups") {
      // "Other setups on the same instrument" -- the provider's own
      // effect 4, unrelated to RightPanel, still fires regardless.
      return Promise.resolve(jsonResponse(Object.values(opts.setupsById)));
    }
    if (url.pathname === "/api/v1/watchlist") {
      return Promise.resolve(jsonResponse([{ instrument_id: 1, position: 0 }]));
    }
    if (url.pathname === "/api/v1/instruments/stats") {
      return Promise.resolve(jsonResponse([opts.stats ?? realStats]));
    }
    if (/^\/api\/v1\/instruments\/\d+\/candles$/.test(url.pathname)) {
      return Promise.resolve(jsonResponse(candles()));
    }
    throw new Error(`unexpected fetch: ${url.toString()}`);
  }) as unknown as typeof fetch;
}

function renderAnalysisRightPanel(initialEntry: string) {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <TooltipProvider delayDuration={150}>
        <AnalysisWorkspaceProvider>
          <ArgusActivityProvider>
            <RightPanel />
          </ArgusActivityProvider>
        </AnalysisWorkspaceProvider>
      </TooltipProvider>
    </MemoryRouter>,
  );
}

describe("/analysis right panel", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllEnvs();
  });

  it("shows a loading state before the setup resolves", () => {
    global.fetch = vi.fn(() => new Promise<Response>(() => {})) as unknown as typeof fetch;
    renderAnalysisRightPanel("/analysis?setupId=1");
    expect(screen.getByText(/Loading plan/)).toBeInTheDocument();
  });

  it("shows the empty state when the instrument has no setups", async () => {
    // No setupId/instrumentId in the URL: the provider falls back to the
    // first known instrument's own most-recently-updated setup
    // (analysis-workspace.tsx's own resolve()), which is genuinely
    // "none" here -- not a 404 on a specific id, which is the error
    // path below, a different case.
    installFetchMock({ setupsById: {} });
    renderAnalysisRightPanel("/analysis");
    expect(await screen.findByText(/No setups for this instrument yet/)).toBeInTheDocument();
  });

  it("shows an error state when the setup fetch fails", async () => {
    installFetchMock({ setupsById: {}, setupError: true });
    renderAnalysisRightPanel("/analysis?setupId=1");
    expect(await screen.findByText(/Could not load setup/)).toBeInTheDocument();
  });

  it("renders the plan card and evidence list for the selected setup", async () => {
    const setup = makeApiSetup({ id: 1 });
    installFetchMock({ setupsById: { 1: setup }, eventsBySetupId: { 1: makeEvents(1) } });
    renderAnalysisRightPanel("/analysis?setupId=1");

    expect(await screen.findByText("The active plan")).toBeInTheDocument();
    expect(screen.getByText("90")).toBeInTheDocument(); // Entry, decimal-string-derived
    expect(screen.getByText("88")).toBeInTheDocument(); // Stop
    expect(await screen.findByText("Detected")).toBeInTheDocument(); // evidence item label
  });

  it("selecting a different setup (via the URL, the same way Link navigation works) updates the plan card and evidence list", async () => {
    const setupA = makeApiSetup({ id: 1, plan_snapshot: { ...makeApiSetup().plan_snapshot, entry: "90.00", stop: "88.00" } });
    const setupB = makeApiSetup({
      id: 2,
      plan_snapshot: { ...makeApiSetup().plan_snapshot, entry: "150.00", stop: "140.00" },
    });
    installFetchMock({
      setupsById: { 1: setupA, 2: setupB },
      eventsBySetupId: { 1: makeEvents(1), 2: makeEvents(2) },
    });

    const first = renderAnalysisRightPanel("/analysis?setupId=1");
    expect(await screen.findByText("90")).toBeInTheDocument();
    first.unmount();
    cleanup();

    renderAnalysisRightPanel("/analysis?setupId=2");
    expect(await screen.findByText("150")).toBeInTheDocument();
    expect(screen.queryByText("90")).toBeNull();
  });

  it("fixture mode (VITE_USE_FIXTURES=true) shows no invented performance numbers", async () => {
    vi.stubEnv("VITE_USE_FIXTURES", "true");
    // Fixture mode never calls fetch (fixtures-as-api.ts's own
    // fixturesEnabled() branch in every api/*.ts function) -- a fetch
    // mock here would never be hit, so none is installed; a stray real
    // fetch would fail loudly instead of silently passing. There is no
    // stats source in this mode, so the tiles are absent (em dashes),
    // not the old fixture percentages. (The app shell's own global
    // "all data is sample" badge covers this mode; it is outside
    // RightPanel.)
    renderAnalysisRightPanel("/analysis");
    const tile = await screen.findByTestId("perf-1D");
    expect(tile).toHaveTextContent("—");
    expect(screen.queryByText(/0\.53|2\.84|4\.12/)).toBeNull();
  });

  it("real instrument: performance, data quality and no Sample marker anywhere in the panel", async () => {
    const setup = makeApiSetup({ id: 1 });
    installFetchMock({ setupsById: { 1: setup }, eventsBySetupId: { 1: makeEvents(1) } });
    renderAnalysisRightPanel("/analysis?setupId=1");
    await screen.findByText("The active plan");
    expect(await screen.findByTestId("perf-1D")).toHaveTextContent("-1.28%");
    for (const tf of ["1W", "1M", "3M", "6M", "1Y"]) {
      expect(screen.getByTestId(`perf-${tf}`)).toHaveTextContent("—");
    }
    // last_closed_open_time 18:16 + one minute = finalized through 18:17 UTC.
    expect(screen.getByTestId("finalized-through")).toHaveTextContent("16 Nov, 18:17 UTC");
    expect(screen.getByTestId("finalized-age")).toHaveTextContent(/\d+(s|m|h|d) ago/);
    expect(screen.queryByText(/Sample/i)).toBeNull();
    // Rule 7: no freshness verdict in the data-quality row.
    expect(screen.getByTestId("data-quality").textContent).not.toMatch(/\b(live|stale|degraded)\b/i);
  });

  it("watchlist tab: real Last/Chg/Chg%, symbol detail from stats, forex as one plain line", async () => {
    const setup = makeApiSetup({ id: 1 });
    installFetchMock({ setupsById: { 1: setup }, eventsBySetupId: { 1: makeEvents(1) } });
    renderAnalysisRightPanel("/analysis?setupId=1");
    await userEvent.setup().click(await screen.findByRole("tab", { name: "Watchlist" }));

    const row = await screen.findByTestId("watchlist-row-BTC-PERP");
    await within(row).findByText("95,123.40");
    expect(row).toHaveTextContent("-1,234.50");
    expect(row).toHaveTextContent("-1.28%");

    expect(await screen.findByTestId("detail-last")).toHaveTextContent("95,123.40");
    expect(screen.getByTestId("detail-change-24h")).toHaveTextContent("-1,234.50");
    expect(screen.getByTestId("detail-change-24h-pct")).toHaveTextContent("(-1.28%)");
    expect(screen.getByTestId("detail-volume-24h")).toHaveTextContent(/^12,345\.6789\s*BTC$/);
    expect(screen.getByTestId("symbol-detail")).toHaveTextContent("base asset volume");

    // Rules 3, 5, 6: no average volume, no news, no forex numbers or badge.
    expect(screen.queryByText(/Avg\. volume/)).toBeNull();
    expect(screen.queryByText(/News/)).toBeNull();
    expect(screen.getByTestId("forex-not-connected")).toHaveTextContent("Forex is not connected yet");
    expect(screen.queryByText(/GBPUSD|AUDUSD|EURGBP/)).toBeNull();
    expect(screen.queryByText(/Sample/i)).toBeNull();
  });

  it("absent stats render as em dashes, never zero", async () => {
    const setup = makeApiSetup({ id: 1 });
    installFetchMock({ setupsById: { 1: setup }, eventsBySetupId: { 1: makeEvents(1) }, stats: absentStats });
    renderAnalysisRightPanel("/analysis?setupId=1");
    await screen.findByText("The active plan");
    // Wait for the stats request to settle, then check every tile.
    await new Promise((r) => setTimeout(r, 50));
    for (const tf of ["1D", "1W", "1M", "3M", "6M", "1Y"]) {
      expect(screen.getByTestId(`perf-${tf}`)).toHaveTextContent("—");
    }
    expect(screen.getByTestId("finalized-through")).toHaveTextContent("—");
    expect(screen.queryByTestId("finalized-age")).toBeNull();
    expect(screen.queryByText(/0\.00%/)).toBeNull();
  });

  it("shows no Sample markers tied to real plan/evidence data when real data is active", async () => {
    const setup = makeApiSetup({ id: 1 });
    installFetchMock({ setupsById: { 1: setup }, eventsBySetupId: { 1: makeEvents(1) } });
    renderAnalysisRightPanel("/analysis?setupId=1");
    await screen.findByText("The active plan");
    // Performance tiles and the data-quality row are real now (GET
    // /instruments/stats); see the "no Sample marker anywhere" test
    // above. This one asserts the plan card/evidence specifically.
    expect(screen.queryByText(/90\.00|88\.00/)).toBeNull(); // no raw unscaled fixture-style numbers leaking through
  });

  it("never renders the fixture toast feed when real data is active (Part 1 regression)", async () => {
    const setup = makeApiSetup({ id: 1 });
    installFetchMock({ setupsById: { 1: setup }, eventsBySetupId: { 1: makeEvents(1) } });
    renderAnalysisRightPanel("/analysis?setupId=1");
    await screen.findByText("The active plan");
    // Give the toast feed's own 500ms mount-delay timer a chance to
    // fire if it were going to (it should not, with fixtures disabled).
    await new Promise((resolve) => setTimeout(resolve, 600));
    expect(screen.queryByText(/Data verified/)).toBeNull();
    expect(screen.queryByText(/Liquidity swept/)).toBeNull();
    expect(screen.queryByText(/Structure read/)).toBeNull();
  });
});
