import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";

// Real stack (live-chart brief, phase 4): the real Vite dev server
// proxying to the real Go API/worker/Postgres/Redis (docker compose,
// already running against the dev database, which already has two
// enabled instruments -- BTC-PERP id=1, ETH-PERP id=2 -- seeded with
// ~1500 real candles each and real setups). No mocked backend, no
// fixture-only data path.
//
// The session cookie was created through the real signup flow
// (POST /auth/signup/start -> read the logged verification code from
// the API container's own logs, since LogMailer writes there in dev ->
// /verify -> /complete -> /me/profile -> /watchlist -> /me/onboarding/
// complete), not inserted directly into the database -- the Claude
// Code auto-mode classifier declined a direct-DB-write attempt for
// this (a Go program/test connecting straight to the dev database),
// and going through the real HTTP endpoints is the correct way to do
// this anyway, not a workaround.
const SESSION_COOKIE = readFileSync(new URL("./.session-cookie", import.meta.url), "utf-8").trim();

// The instrument picker (ChartToolbar's own SymbolPicker) is a Radix
// DropdownMenu, not a <select> -- its trigger is the one button with
// aria-haspopup="menu" (plain text/role queries for "BTC-PERP" match
// several other elements on the page: the legend, the "other setups on
// BTC-PERP" strip header, etc.) and its items are menuitem role, not
// option.
function symbolPickerTrigger(page: import("@playwright/test").Page) {
  return page.locator('button[aria-haspopup="menu"]').first();
}

test.beforeEach(async ({ context }) => {
  await context.addCookies([
    {
      name: "argus_session",
      value: SESSION_COOKIE,
      domain: "localhost",
      path: "/",
      httpOnly: true,
      sameSite: "Lax",
    },
  ]);
});

test("switching instrument does not rebuild the chart: same canvas element, no page reload", async ({ page }) => {
  await page.goto("/analysis?setupId=1&instrumentId=1");

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
  await page.getByRole("menuitem", { name: /ETH-PERP/ }).click();

  await expect(symbolPickerTrigger(page)).toContainText("ETH-PERP", { timeout: 15_000 });
  await expect(page).toHaveURL(/instrumentId=2/);

  const pageMarkerSurvived = await page.evaluate(
    () => (window as unknown as { __pwMarker?: string }).__pwMarker,
  );
  expect(pageMarkerSurvived).toBe("still-the-same-page"); // no full reload happened

  const sameCanvas = page.locator('canvas[data-pw-original-canvas="yes"]');
  await expect(sameCanvas).toHaveCount(1); // the exact same node is still there, now showing ETH-PERP's own data

  await page.screenshot({ path: "e2e/screenshots/instrument-switch-same-chart.png", fullPage: true });
});

test("switching instrument and back keeps the saved chart view for each instrument's own scope", async ({ page }) => {
  await page.goto("/analysis?setupId=1&instrumentId=1");
  await expect(page.locator("canvas").first()).toBeVisible({ timeout: 15_000 });

  // Zoom in on BTC's own chart (mouse wheel over the chart host),
  // which chart-surface.tsx's own subscribeVisibleLogicalRangeChange
  // persists to localStorage per (symbol, timeframe) scope.
  const chartHost = page.locator("canvas").first();
  const box = await chartHost.boundingBox();
  if (!box) throw new Error("chart host has no bounding box");
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.wheel(0, -400); // zoom in
  await page.waitForTimeout(300); // let the debounced localStorage save land

  const btcViewAfterZoom = await page.evaluate(() =>
    localStorage.getItem("argus:chart-view:v1:BTC-PERP:1m"),
  );
  expect(btcViewAfterZoom).not.toBeNull();

  // Switch to ETH, then back to BTC via the real UI.
  await symbolPickerTrigger(page).click();
  await page.getByRole("menuitem", { name: /ETH-PERP/ }).click();
  await expect(symbolPickerTrigger(page)).toContainText("ETH-PERP", { timeout: 15_000 });

  await symbolPickerTrigger(page).click();
  await page.getByRole("menuitem", { name: /BTC-PERP/ }).click();
  await expect(symbolPickerTrigger(page)).toContainText("BTC-PERP", { timeout: 15_000 });

  // BTC's own saved view is untouched by the trip through ETH -- it
  // is restored (chart-surface.tsx's own loadChartView call in the
  // update effect), not reset to a generic fitContent default.
  const btcViewAfterReturn = await page.evaluate(() =>
    localStorage.getItem("argus:chart-view:v1:BTC-PERP:1m"),
  );
  expect(btcViewAfterReturn).toBe(btcViewAfterZoom);
});
