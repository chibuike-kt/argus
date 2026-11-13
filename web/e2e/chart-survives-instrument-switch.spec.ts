import { test, expect } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { AUTH_FILE, type E2EAuthState } from "./global-setup";

// Real stack (live-chart brief, phase 4): the production build
// (pnpm build && pnpm preview, this config's own webServer), proxying
// to a dedicated api+worker pair against argus_e2e (never the dev
// "argus" database -- Makefile's own e2e-stack target). The session,
// and the BTC-PERP/ETH-PERP instrument ids and a real setup id, all
// come from global-setup.ts's own run (the real HTTP signup flow, and
// real GET /instruments and /setups calls) -- nothing here is
// hardcoded.
let auth: E2EAuthState;

test.beforeAll(async () => {
  auth = JSON.parse(await readFile(AUTH_FILE, "utf-8")) as E2EAuthState;
});

test.beforeEach(async ({ context }) => {
  const [name, value] = auth.sessionCookie.split("=");
  await context.addCookies([{ name, value, domain: "localhost", path: "/", httpOnly: true, sameSite: "Lax" }]);
});

// The instrument picker (ChartToolbar's own SymbolPicker) is a Radix
// DropdownMenu, not a <select> -- its trigger is the one button with
// aria-haspopup="menu" that plain text/role queries for "BTC-PERP"
// cannot isolate (several other elements on the page match: the
// legend, the "other setups on BTC-PERP" strip header, etc.), and its
// items are menuitem role, not option. .first() is reliable because
// the toolbar always renders before the drawing-tool buttons that also
// carry aria-haspopup="menu".
function symbolPickerTrigger(page: import("@playwright/test").Page) {
  return page.locator('button[aria-haspopup="menu"]').first();
}

// Each menu item's accessible name is "<icon-alt> <symbol> <provider>"
// (chart-toolbar.tsx), so a plain substring match on "BTC-PERP" is not
// precise -- it would also match a symbol that merely starts with it
// (e.g. a differently-provisioned row sharing the prefix). Anchoring
// the provider at the end, with a word boundary before the symbol, is
// exact without hardcoding anything not already looked up from the
// real API in global-setup. argus_e2e is reset fresh before every run
// (Makefile's own e2e-resetdb) so this is defensive rather than a
// response to an actual leftover-row problem today.
function symbolMenuItem(page: import("@playwright/test").Page, symbol: string, provider: string) {
  const escaped = symbol.replace(/[.*+?^${}()|[\]\\-]/g, "\\$&");
  return page.getByRole("menuitem", { name: new RegExp(`\\b${escaped}\\s+${provider}$`) });
}

test("switching instrument does not rebuild the chart: same canvas element, no page reload", async ({ page }) => {
  await page.goto(`/analysis?setupId=${auth.btcSetupId}&instrumentId=${auth.btcInstrumentId}`);

  const canvas = page.locator("canvas").first();
  await expect(canvas).toBeVisible({ timeout: 15_000 });
  await expect(symbolPickerTrigger(page)).toContainText("BTC-PERP");

  // Mark the real DOM node lightweight-charts created. If a later
  // instrument switch tears the chart down and creates a new one (the
  // bug chart-surface.tsx's own doc comment describes, from before
  // Part 2A), this attribute -- set on THIS node, not "a canvas
  // matching this selector" -- would be gone, because it would be a
  // different node.
  await canvas.evaluate((el) => el.setAttribute("data-pw-original-canvas", "yes"));

  // A page-level marker proves this is still the same page (React
  // Router's client-side navigation), not a full reload -- a reload
  // would re-run this script from scratch and lose it too.
  await page.evaluate(() => {
    (window as unknown as { __pwMarker: string }).__pwMarker = "still-the-same-page";
  });

  // Switch instrument via the real UI control, not a URL navigation --
  // a page.goto() to a new URL would reload the whole app and prove
  // nothing about rebuild avoidance within a live session.
  await symbolPickerTrigger(page).click();
  await symbolMenuItem(page, "ETH-PERP", auth.ethProvider).click();

  await expect(symbolPickerTrigger(page)).toContainText("ETH-PERP", { timeout: 15_000 });
  await expect(page).toHaveURL(new RegExp(`instrumentId=${auth.ethInstrumentId}`));

  const pageMarkerSurvived = await page.evaluate(
    () => (window as unknown as { __pwMarker?: string }).__pwMarker,
  );
  expect(pageMarkerSurvived).toBe("still-the-same-page"); // no full reload happened

  const sameCanvas = page.locator('canvas[data-pw-original-canvas="yes"]');
  await expect(sameCanvas).toHaveCount(1); // the exact same node is still there, now showing ETH-PERP's own data

  await page.screenshot({ path: "e2e/screenshots/instrument-switch-same-chart.png", fullPage: true });
});

// Bug repro (reported after this test passed only 1 run in 3): the
// saved view under argus:chart-view:v1:BTC-PERP:1m went from a real
// zoomed range to something like {from: -70, to: 3} -- the same width,
// shifted down by about the history length. Root cause, confirmed by
// reading chart-surface.tsx: every visible-range change was persisted,
// including the ones caused by the component's own setData (clearing
// to [] while the new instrument's history is still loading) and by
// the restore/default-fit calls themselves -- not just a change the
// trader actually made. A slow fetch makes the empty intermediate
// render far more likely to land (it can land on a fast one too, which
// is why the old version of this test only failed intermittently); an
// artificially delayed response makes it land every time, so this test
// does not depend on the real network happening to be slow enough on
// any given run.
test("switching instrument and back keeps the saved chart view for each instrument's own scope", async ({ page }) => {
  await page.goto(`/analysis?setupId=${auth.btcSetupId}&instrumentId=${auth.btcInstrumentId}`);
  await expect(page.locator("canvas").first()).toBeVisible({ timeout: 15_000 });

  // Zoom in on BTC's own chart (mouse wheel over the chart host),
  // which chart-surface.tsx's own subscribeVisibleLogicalRangeChange
  // persists to localStorage per (symbol, timeframe) scope.
  const chartHost = page.locator("canvas").first();
  const box = await chartHost.boundingBox();
  if (!box) throw new Error("chart host has no bounding box");
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.wheel(0, -400); // zoom in
  await page.waitForTimeout(300); // let the localStorage save land

  const btcViewAfterZoom = await page.evaluate(() =>
    localStorage.getItem("argus:chart-view:v1:BTC-PERP:1m"),
  );
  expect(btcViewAfterZoom).not.toBeNull();

  // Switch to ETH at ordinary speed -- nothing about this leg needs to
  // be slow.
  await symbolPickerTrigger(page).click();
  await symbolMenuItem(page, "ETH-PERP", auth.ethProvider).click();
  await expect(symbolPickerTrigger(page)).toContainText("ETH-PERP", { timeout: 15_000 });

  // Deliberately delay BTC's own candle history response for the
  // switch back, so the empty-candles render this bug depends on
  // really happens -- every run, not only when the real network
  // happens to be slow enough on its own. Registered only now, so it
  // does not also delay the initial BTC load or the ETH switch above.
  await page.route(`**/api/v1/instruments/${auth.btcInstrumentId}/candles**`, async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 500));
    await route.continue();
  });

  await symbolPickerTrigger(page).click();
  await symbolMenuItem(page, "BTC-PERP", auth.btcProvider).click();
  await expect(symbolPickerTrigger(page)).toContainText("BTC-PERP", { timeout: 15_000 });

  // The delayed response (and the render it triggers) needs a moment
  // past the UI's own optimistic symbol-label update, above, to land.
  await page.waitForTimeout(800);

  // BTC's own saved view is untouched by the trip through ETH -- it
  // is restored (chart-surface.tsx's own loadChartView call in the
  // update effect), not reset to a generic fitContent default, and not
  // overwritten by the empty render's own spurious range.
  const btcViewAfterReturn = await page.evaluate(() =>
    localStorage.getItem("argus:chart-view:v1:BTC-PERP:1m"),
  );
  expect(btcViewAfterReturn).not.toBeNull();

  const parseView = (raw: string) => JSON.parse(raw) as { from: number; to: number };
  const zoomed = parseView(btcViewAfterZoom!);
  const returned = parseView(btcViewAfterReturn!);
  const tolerance = 1; // bar
  expect(Math.abs(returned.from - zoomed.from)).toBeLessThanOrEqual(tolerance);
  expect(Math.abs(returned.to - zoomed.to)).toBeLessThanOrEqual(tolerance);
});
