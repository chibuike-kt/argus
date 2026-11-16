import { test, expect } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { AUTH_FILE, type E2EAuthState } from "./global-setup";

// Real bug report: analysis-workspace.tsx derived `instrument` from
// `setup?.instrument_id` alone, so an instrument with no setups
// (SOL-PERP here, db/seed/e2e_only_instruments.sql's own flat-price
// fixture -- see that file's doc comment for why it deterministically
// never produces one) could never be selected: the picker stayed on
// "Select instrument" and no candles/live subscription ever happened.
// These specs exercise the real production stack (the same
// api+worker+argus_e2e pair chart-survives-instrument-switch.spec.ts
// uses), not a mock.
let auth: E2EAuthState;

test.beforeAll(async () => {
  auth = JSON.parse(await readFile(AUTH_FILE, "utf-8")) as E2EAuthState;
});

test.beforeEach(async ({ context }) => {
  const [name, value] = auth.sessionCookie.split("=");
  await context.addCookies([{ name, value, domain: "localhost", path: "/", httpOnly: true, sameSite: "Lax" }]);
});

function symbolPickerTrigger(page: import("@playwright/test").Page) {
  return page.locator('button[aria-haspopup="menu"]').first();
}

function symbolMenuItem(page: import("@playwright/test").Page, symbol: string, provider: string) {
  const escaped = symbol.replace(/[.*+?^${}()|[\]\\-]/g, "\\$&");
  return page.getByRole("menuitem", { name: new RegExp(`\\b${escaped}\\s+${provider}$`) });
}

test("an instrumentId with no setups resolves: picker shows it, chart renders candles, right panel shows the clear empty state", async ({
  page,
}) => {
  await page.goto(`/analysis?instrumentId=${auth.solInstrumentId}`);

  // The real bug: before the fix, this stayed "Select instrument"
  // forever for an instrument with no setups.
  await expect(symbolPickerTrigger(page)).toContainText("SOL-PERP", { timeout: 15_000 });
  await expect(symbolPickerTrigger(page)).not.toContainText("Select instrument");

  // Candles/live updates/the badge still render with no setup.
  const canvas = page.locator("canvas").first();
  await expect(canvas).toBeVisible({ timeout: 15_000 });
  await expect(page.getByText(/Connected|Connecting|Reconnecting/).first()).toBeVisible({ timeout: 15_000 });

  // Every panel that reads the setup shows a clear empty state, not a
  // blank page or an error.
  await expect(page.getByText("No setups for this instrument yet.")).toBeVisible();

  await page.screenshot({ path: "e2e/screenshots/instrument-no-setups.png", fullPage: true });
});

test("switching from an instrument with setups to one without (and back) does not rebuild the chart", async ({
  page,
}) => {
  await page.goto(`/analysis?setupId=${auth.btcSetupId}&instrumentId=${auth.btcInstrumentId}`);
  const canvas = page.locator("canvas").first();
  await expect(canvas).toBeVisible({ timeout: 15_000 });
  await expect(symbolPickerTrigger(page)).toContainText("BTC-PERP");

  await canvas.evaluate((el) => el.setAttribute("data-pw-original-canvas", "yes"));

  // BTC -> SOL (no setups).
  await symbolPickerTrigger(page).click();
  await symbolMenuItem(page, "SOL-PERP", auth.solProvider).click();
  await expect(symbolPickerTrigger(page)).toContainText("SOL-PERP", { timeout: 15_000 });
  await expect(page.locator('canvas[data-pw-original-canvas="yes"]')).toHaveCount(1);
  await expect(page.getByText("No setups for this instrument yet.")).toBeVisible();

  // SOL -> back to BTC (has setups).
  await symbolPickerTrigger(page).click();
  await symbolMenuItem(page, "BTC-PERP", auth.btcProvider).click();
  await expect(symbolPickerTrigger(page)).toContainText("BTC-PERP", { timeout: 15_000 });
  await expect(page.locator('canvas[data-pw-original-canvas="yes"]')).toHaveCount(1); // still the same node throughout
  await expect(page.getByText("No setups for this instrument yet.")).not.toBeVisible();
});

test("an instrumentId that does not exist shows a clear error, not a blank page", async ({ page }) => {
  await page.goto("/analysis?instrumentId=999999999");
  await expect(page.getByText(/Instrument not found or not available/i)).toBeVisible({ timeout: 15_000 });
  // Not a blank page: the rest of the chrome (toolbar) is still there.
  await expect(symbolPickerTrigger(page)).toBeVisible();
});

test("setupId and a disagreeing instrumentId in the URL: the setup's own instrument wins", async ({ page }) => {
  // btcSetupId belongs to BTC; instrumentId names SOL instead -- they disagree.
  await page.goto(`/analysis?setupId=${auth.btcSetupId}&instrumentId=${auth.solInstrumentId}`);
  await expect(symbolPickerTrigger(page)).toContainText("BTC-PERP", { timeout: 15_000 });
  await expect(page).toHaveURL(new RegExp(`setupId=${auth.btcSetupId}`));
});
