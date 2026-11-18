import { test, expect, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { AUTH_FILE, type E2EAuthState } from "./global-setup";
import { formatDecimal, formatPercent } from "../src/lib/decimal-display";

// real-panel-data brief: every number in the side panel and the chart
// legend is real (GET /instruments/stats, computed on the server from
// stored candles) or visibly absent -- never a sample number and never
// a sample badge next to a real instrument. Run against the real stack
// (production build + real api), never mocks.
//
// The standard e2e stack (argus_e2e) seeds only provider "fixture"
// instruments, so the real-provider tests below skip there with a
// stated reason; they run when this suite is pointed at a database that
// has an enabled real-provider instrument (a throwaway copy of the dev
// database with its Hyperliquid instruments -- see the brief's report).

const API_BASE = `http://localhost:${process.env.E2E_API_PORT ?? "8090"}`;
const ORIGIN = "http://localhost:4173";

interface Instrument {
  id: number;
  native_symbol: string;
  provider: string;
  enabled: boolean;
}

interface Stats {
  instrument_id: number;
  change_24h: string | null;
  change_24h_pct: string | null;
}

let auth: E2EAuthState;

test.beforeAll(async () => {
  auth = JSON.parse(await readFile(AUTH_FILE, "utf-8")) as E2EAuthState;
});

test.beforeEach(async ({ context }) => {
  const [name, value] = auth.sessionCookie.split("=");
  await context.addCookies([{ name, value, domain: "localhost", path: "/", httpOnly: true, sameSite: "Lax" }]);
});

async function apiGet<T>(path: string): Promise<T> {
  const res = await fetch(`${API_BASE}/api/v1${path}`, { headers: { Cookie: auth.sessionCookie, Origin: ORIGIN } });
  if (!res.ok) throw new Error(`GET ${path} -> ${res.status}: ${await res.text()}`);
  return (await res.json()) as T;
}

async function enabledInstruments(): Promise<Instrument[]> {
  return apiGet<Instrument[]>("/instruments?limit=500");
}

// expectedLegend is what the legend must show for a stats row: the
// server's own strings, only formatted (the same formatter the app
// uses -- no arithmetic here either).
function expectedLegend(s: Stats | undefined): string {
  return `${formatDecimal(s?.change_24h, { signed: true })} (${formatPercent(s?.change_24h_pct)})`;
}

// expectLegendMatchesServer polls until the legend shows exactly what the
// server returns for id right now (the e2e worker's live replay closes a
// candle about every second, so a single read of each could straddle a
// close).
async function expectLegendMatchesServer(page: Page, id: number) {
  const legend = page.getByTestId("legend-change-24h");
  await expect
    .poll(
      async () => {
        const [row] = await apiGet<Stats[]>(`/instruments/stats?ids=${id}`);
        const shown = (await legend.textContent())?.trim();
        if (shown === expectedLegend(row)) {
          // Logged so a run's output records the number actually on
          // screen (compared by hand against raw SQL in the report).
          console.log(`[legend] instrument ${id}: ${shown}`);
          return "match";
        }
        return `ui=${shown} server=${expectedLegend(row)}`;
      },
      { timeout: 20_000, intervals: [250, 500, 1000] },
    )
    .toBe("match");
}

async function expectNoSampleMarkerOnPage(page: Page) {
  // SampleDataBadge's own text always contains "Sample"; nothing real
  // on these surfaces does.
  await expect(page.getByText(/sample/i)).toHaveCount(0);
}

function symbolPickerTrigger(page: Page) {
  return page.locator('button[aria-haspopup="menu"]').first();
}

function symbolMenuItem(page: Page, symbol: string, provider: string) {
  const escaped = symbol.replace(/[.*+?^${}()|[\]\\-]/g, "\\$&");
  return page.getByRole("menuitem", { name: new RegExp(`\\b${escaped}\\s+${provider}$`) });
}

test("fixture-provider instrument: legend and panel are real or absent, no Replay, no forex group", async ({
  page,
}) => {
  await page.goto(`/analysis?instrumentId=${auth.btcInstrumentId}`);
  await expect(symbolPickerTrigger(page)).toContainText("BTC-PERP", { timeout: 15_000 });
  await expectLegendMatchesServer(page, auth.btcInstrumentId);

  // Phase 2 (D2): Replay is gone for every instrument, fixture ones included.
  await expect(page.getByRole("button", { name: /Replay/ })).toHaveCount(0);

  await expect(page.getByTestId("performance-tiles")).toBeVisible();
  await expect(page.getByTestId("finalized-through")).not.toHaveText("—");
  await expect(page.getByTestId("data-quality")).not.toContainText(/\b(live|stale|degraded)\b/i);

  await page.getByRole("tab", { name: "Watchlist" }).click();
  // Phase 2 (D2): no forex group at all.
  await expect(page.getByText(/forex/i)).toHaveCount(0);
  await expect(page.getByText(/GBPUSD|AUDUSD|EURGBP/)).toHaveCount(0);
  await expect(page.getByText(/News|Avg\. volume/)).toHaveCount(0);

  await expectNoSampleMarkerOnPage(page);
  await page.screenshot({ path: "e2e/screenshots/real-panel-data-fixture.png", fullPage: true });
});

test("real-provider instrument: no sample marker in the side panel or legend, Replay hidden, legend matches the server", async ({
  page,
}) => {
  const real = (await enabledInstruments()).find((i) => i.provider !== "fixture");
  test.skip(!real, "no enabled real-provider instrument in this database (argus_e2e seeds provider \"fixture\" only)");
  if (!real) return;

  await page.goto(`/analysis?instrumentId=${real.id}`);
  await expect(symbolPickerTrigger(page)).toContainText(real.native_symbol, { timeout: 15_000 });
  await expectLegendMatchesServer(page, real.id);

  await expect(page.getByRole("button", { name: /Replay/ })).toHaveCount(0);
  await expectNoSampleMarkerOnPage(page);
  await expect(page.getByTestId("data-quality")).not.toContainText(/\b(live|stale|degraded)\b/i);

  await page.getByRole("tab", { name: "Watchlist" }).click();
  await expect(page.getByText(/forex/i)).toHaveCount(0);
  await expect(page.getByTestId("symbol-detail")).toBeVisible();
  await expectNoSampleMarkerOnPage(page);
  await page.screenshot({ path: "e2e/screenshots/real-panel-data-real-provider.png", fullPage: true });
});

test("rapid instrument switching: the legend always settles on the selected instrument's own stats", async ({ page }) => {
  const all = await enabledInstruments();
  test.skip(all.length < 2, "needs at least two enabled instruments");
  // Prefer a pair across providers (fixture and real) when both exist.
  const a = all[0];
  const b = all.find((i) => i.provider !== a.provider) ?? all[1];

  await page.goto(`/analysis?instrumentId=${a.id}`);
  await expect(symbolPickerTrigger(page)).toContainText(a.native_symbol, { timeout: 15_000 });

  // Switch back and forth without waiting for any response in between.
  for (const next of [b, a, b, a, b]) {
    await symbolPickerTrigger(page).click();
    await symbolMenuItem(page, next.native_symbol, next.provider).click();
  }
  await expect(symbolPickerTrigger(page)).toContainText(b.native_symbol);
  await expectLegendMatchesServer(page, b.id);

  // And one more switch: the old instrument's figure is replaced, not kept.
  await symbolPickerTrigger(page).click();
  await symbolMenuItem(page, a.native_symbol, a.provider).click();
  await expect(symbolPickerTrigger(page)).toContainText(a.native_symbol);
  await expectLegendMatchesServer(page, a.id);
});

test("every enabled instrument: the legend shows exactly the server's 24h change", async ({ page }) => {
  for (const inst of await enabledInstruments()) {
    await page.goto(`/analysis?instrumentId=${inst.id}`);
    await expect(symbolPickerTrigger(page)).toContainText(inst.native_symbol, { timeout: 15_000 });
    const [row] = await apiGet<Array<Stats & { last_close: string | null }>>(`/instruments/stats?ids=${inst.id}`);
    if (row.last_close === null) {
      // No closed candle at all: the chart shows its own empty state (no
      // legend), and every panel figure is absent -- em dashes, not 0.
      await expect(page.getByText("No candles in range for this instrument.")).toBeVisible();
      await expect(page.getByTestId("perf-1D")).toContainText("—");
      await expect(page.getByTestId("finalized-through")).toHaveText("—");
      continue;
    }
    await expectLegendMatchesServer(page, inst.id);
    if (inst.provider !== "fixture") await expectNoSampleMarkerOnPage(page);
  }
});
