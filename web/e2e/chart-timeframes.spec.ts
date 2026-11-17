import { test, expect, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { AUTH_FILE, type E2EAuthState } from "./global-setup";

// Item 2 (chart timeframe default view), on real Hyperliquid data: every
// timeframe opens on a readable default view -- about 120 bars ending at the
// newest, normal bar width, or a short series from the left at the same
// width -- never a few thin bars pinned right or one stretched candle. With
// no saved view (localStorage cleared). Screenshots per timeframe go to
// e2e/screenshots/ for the report. Skips on a database with no enabled
// real-provider instrument (argus_e2e has only fixture ones).

const API_BASE = `http://localhost:${process.env.E2E_API_PORT ?? "8090"}`;
const ORIGIN = "http://localhost:4173";
const TIMEFRAMES = ["1m", "5m", "1h", "4h", "1d"] as const;

let auth: E2EAuthState;

test.beforeAll(async () => {
  auth = JSON.parse(await readFile(AUTH_FILE, "utf-8")) as E2EAuthState;
});

test.beforeEach(async ({ context }) => {
  const [name, value] = auth.sessionCookie.split("=");
  await context.addCookies([{ name, value, domain: "localhost", path: "/", httpOnly: true, sameSite: "Lax" }]);
});

async function realInstrument() {
  const res = await fetch(`${API_BASE}/api/v1/instruments?limit=500`, {
    headers: { Cookie: auth.sessionCookie, Origin: ORIGIN },
  });
  const all = (await res.json()) as Array<{ id: number; native_symbol: string; provider: string; supported_intervals: string[] }>;
  return all.find((i) => i.provider !== "fixture");
}

async function selectTimeframe(page: Page, tf: string) {
  await page.locator('button[aria-label^="Timeframe:"]').click();
  await page.getByRole("menuitem", { name: new RegExp(`^${tf}$`) }).click();
  await expect(page.locator(`button[aria-label="Timeframe: ${tf}"]`)).toBeVisible();
}

test("real instrument: every timeframe opens on a readable default view (screenshots)", async ({ page }) => {
  const inst = await realInstrument();
  test.skip(!inst, "no enabled real-provider instrument in this database");
  if (!inst) return;

  // No saved views on the server either: reset the preferences document.
  const headers = { Cookie: auth.sessionCookie, Origin: ORIGIN, "Content-Type": "application/json" };
  const cur = (await (await fetch(`${API_BASE}/api/v1/me/preferences`, { headers })).json()) as { version: number };
  await fetch(`${API_BASE}/api/v1/me/preferences`, { method: "PUT", headers, body: JSON.stringify({ data: {}, version: cur.version }) });

  await page.goto("/");
  await page.evaluate(() => {
    // Saved views are preferences (D1): clear the page's cached copy here;
    // the server copy was reset above.
    for (const k of Object.keys(localStorage)) if (k.startsWith("argus:prefs:")) localStorage.removeItem(k);
  });
  await page.goto(`/analysis?instrumentId=${inst.id}`);
  await expect(page.locator("canvas").first()).toBeVisible({ timeout: 15_000 });

  for (const tf of TIMEFRAMES) {
    if (!inst.supported_intervals.includes(tf)) continue;
    const loaded = page.waitForResponse((r) => r.url().includes(`/instruments/${inst.id}/candles`) && r.status() === 200);
    if (tf !== "1m") await selectTimeframe(page, tf);
    else await page.reload();
    await loaded;
    await page.waitForTimeout(600); // let the chart lay out the new data
    await expect(page.getByText(/Could not load candles/)).toHaveCount(0);
    // No saved view was created by the default view itself (only a user
    // gesture saves one).
    const saved = await page.evaluate(
      ([userId, key]) => {
        const raw = localStorage.getItem(`argus:prefs:v1:user:${userId}`);
        return raw ? (JSON.parse(raw)?.data?.chart_views?.[key] ?? null) : null;
      },
      [auth.userId, `${inst.id}:${tf}`] as const,
    );
    expect(saved).toBeNull();
    await page.screenshot({ path: `e2e/screenshots/timeframe-${inst.native_symbol}-${tf}.png` });
  }
});
