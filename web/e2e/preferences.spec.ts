import { test, expect, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { AUTH_FILE, type E2EAuthState } from "./global-setup";

// Phase 1 (owner decision D1): everything the user sets stays set -- across
// a refresh, a cleared browser, a second browser profile, two tabs, a
// failing server and going offline. Real stack: production build, real
// API, real Postgres.

const API_BASE = `http://localhost:${process.env.E2E_API_PORT ?? "8090"}`;
const ORIGIN = "http://localhost:4173";
let auth: E2EAuthState;

test.beforeAll(async () => {
  auth = JSON.parse(await readFile(AUTH_FILE, "utf-8")) as E2EAuthState;
});

async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${API_BASE}/api/v1${path}`, {
    method,
    headers: { Cookie: auth.sessionCookie, Origin: ORIGIN, "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`${method} ${path} -> ${res.status}: ${await res.text()}`);
  return (await res.json()) as T;
}

type PrefsDoc = { data: Record<string, unknown>; version: number };

// Start every test from an empty server-side document.
test.beforeEach(async () => {
  const cur = await api<PrefsDoc>("GET", "/me/preferences");
  await api("PUT", "/me/preferences", { data: {}, version: cur.version });
});

// A fresh browser profile: no localStorage at all, only a signed-in session.
async function freshProfile(browser: import("@playwright/test").Browser) {
  const context = await browser.newContext();
  const [name, value] = auth.sessionCookie.split("=");
  await context.addCookies([{ name, value, domain: "localhost", path: "/", httpOnly: true, sameSite: "Lax" }]);
  return context;
}

async function chooseTimeframe(page: Page, tf: string) {
  await page.locator('button[aria-label^="Timeframe:"]').click();
  await page.getByRole("menuitem", { name: new RegExp(`^${tf}$`) }).click();
}

function prefsSaved(page: Page, status = 200) {
  return page.waitForResponse((r) => r.url().includes("/me/preferences") && r.request().method() === "PUT" && r.status() === status, {
    timeout: 15_000,
  });
}

test("settings survive a cleared browser and a second browser profile", async ({ browser }) => {
  const first = await freshProfile(browser);
  const page = await first.newPage();
  await page.goto(`/analysis?instrumentId=${auth.ethInstrumentId}`);
  await expect(page.locator('button[aria-haspopup="menu"]').first()).toContainText("ETH-PERP", { timeout: 15_000 });

  const saved = prefsSaved(page);
  await chooseTimeframe(page, "5m");
  await page.getByRole("tab", { name: "Watchlist" }).click();
  await page.getByRole("button", { name: "Hide tool rail" }).click();
  await saved;
  await expect
    .poll(async () => (await api<PrefsDoc>("GET", "/me/preferences")).data, { timeout: 10_000 })
    .toMatchObject({ timeframe: "5m", right_panel_tab: "watchlist", tool_rail_collapsed: true, selected_instrument_id: auth.ethInstrumentId });
  await first.close();

  // A second profile: empty storage, signed in, and no instrument in the
  // link. Everything comes back from the server.
  const second = await freshProfile(browser);
  const page2 = await second.newPage();
  await page2.goto("/analysis");
  await expect(page2.locator('button[aria-haspopup="menu"]').first()).toContainText("ETH-PERP", { timeout: 15_000 });
  await expect(page2.locator('button[aria-label="Timeframe: 5m"]')).toBeVisible();
  await expect(page2.getByRole("tab", { name: "Watchlist" })).toHaveAttribute("aria-selected", "true");
  await expect(page2.getByRole("button", { name: "Show tool rail" })).toBeVisible();
  await page2.screenshot({ path: "e2e/screenshots/preferences-restored-second-profile.png" });
  await second.close();
});

test("two tabs changing different settings: the 409 is handled and both changes are kept", async ({ browser }) => {
  const context = await freshProfile(browser);
  const tabA = await context.newPage();
  const tabB = await context.newPage();
  const conflictsB: number[] = [];
  tabB.on("response", (r) => {
    if (r.url().includes("/me/preferences") && r.request().method() === "PUT" && r.status() === 409) conflictsB.push(r.status());
  });
  await tabA.goto(`/analysis?instrumentId=${auth.btcInstrumentId}`);
  await tabB.goto(`/analysis?instrumentId=${auth.btcInstrumentId}`);
  await expect(tabB.locator("canvas").first()).toBeVisible({ timeout: 15_000 });
  // Both tabs save the instrument from the link at load; let that settle.
  await expect.poll(async () => (await api<PrefsDoc>("GET", "/me/preferences")).data.selected_instrument_id, { timeout: 15_000 }).toBe(auth.btcInstrumentId);
  await tabA.waitForTimeout(1500);

  // Tab A changes the timeframe and saves; tab B, whose copy is now one
  // version behind, changes a different setting: its write meets a 409,
  // re-reads, keeps A's change and adds its own.
  await chooseTimeframe(tabA, "15m");
  await expect.poll(async () => (await api<PrefsDoc>("GET", "/me/preferences")).data.timeframe, { timeout: 15_000 }).toBe("15m");
  const conflictsBefore = conflictsB.length;
  await tabB.getByRole("button", { name: "Hide tool rail" }).click();
  await expect
    .poll(async () => (await api<PrefsDoc>("GET", "/me/preferences")).data, { timeout: 20_000 })
    .toMatchObject({ timeframe: "15m", tool_rail_collapsed: true });
  expect(conflictsB.length).toBeGreaterThan(conflictsBefore); // the 409 path really ran
  await context.close();
});

test("server 500, then offline: the change is kept locally and saved once the server is back", async ({ browser }) => {
  const context = await freshProfile(browser);
  const page = await context.newPage();
  await page.goto(`/analysis?instrumentId=${auth.btcInstrumentId}`);
  await expect(page.locator("canvas").first()).toBeVisible({ timeout: 15_000 });

  // The server fails every save for a while.
  await page.route("**/api/v1/me/preferences", (route) =>
    route.request().method() === "PUT" ? route.fulfill({ status: 500, body: '{"error":{"code":"internal_error","message":"x"}}' }) : route.continue(),
  );
  await page.getByRole("tab", { name: "Watchlist" }).click();
  await page.waitForTimeout(2500); // at least one failed attempt
  // Nothing alarming on screen, and the choice still applies.
  await expect(page.getByText(/error|failed/i)).toHaveCount(0);
  await expect(page.getByRole("tab", { name: "Watchlist" })).toHaveAttribute("aria-selected", "true");
  await page.unroute("**/api/v1/me/preferences");
  await expect.poll(async () => (await api<PrefsDoc>("GET", "/me/preferences")).data.right_panel_tab, { timeout: 40_000 }).toBe("watchlist");

  // Offline: change, come back online, it is pushed.
  await context.setOffline(true);
  await page.getByRole("tab", { name: "Analysis" }).click();
  await page.waitForTimeout(1500);
  await context.setOffline(false);
  await expect.poll(async () => (await api<PrefsDoc>("GET", "/me/preferences")).data.right_panel_tab, { timeout: 40_000 }).toBe("analysis");
  await context.close();
});

test("a drawing is stored on the server per chart and appears in another browser profile", async ({ browser }) => {
  const tf = "1m";
  const before = await api<{ data: unknown[]; version: number }>("GET", `/me/drawings?instrument_id=${auth.btcInstrumentId}&timeframe=${tf}`);
  const context = await freshProfile(browser);
  const page = await context.newPage();
  await page.goto(`/analysis?instrumentId=${auth.btcInstrumentId}`);
  const canvas = page.locator("canvas").first();
  await expect(canvas).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId("legend-close")).not.toHaveText("0", { timeout: 15_000 });

  const saved = page.waitForResponse((r) => r.url().includes("/me/drawings") && r.request().method() === "PUT" && r.status() === 200, { timeout: 15_000 });
  await page.getByRole("button", { name: "Trend line", exact: true }).click();
  const box = (await canvas.boundingBox())!;
  await page.mouse.click(box.x + box.width * 0.4, box.y + box.height * 0.5);
  await page.mouse.click(box.x + box.width * 0.6, box.y + box.height * 0.4);
  await saved;
  const after = await api<{ data: { id: string; kind: string }[]; version: number }>("GET", `/me/drawings?instrument_id=${auth.btcInstrumentId}&timeframe=${tf}`);
  expect(after.data.length).toBe(before.data.length + 1);
  expect(after.data.at(-1)?.kind).toBe("trendline");
  await context.close();

  // Another profile, empty storage: the drawing loads from the server.
  const other = await freshProfile(browser);
  const page2 = await other.newPage();
  const loaded = page2.waitForResponse((r) => r.url().includes("/me/drawings") && r.request().method() === "GET" && r.status() === 200, { timeout: 15_000 });
  await page2.goto(`/analysis?instrumentId=${auth.btcInstrumentId}`);
  const body = (await (await loaded).json()) as { data: { kind: string }[] };
  expect(body.data.some((d) => d.kind === "trendline")).toBe(true);
  await page2.screenshot({ path: "e2e/screenshots/preferences-drawing-second-profile.png" });
  await other.close();
});
