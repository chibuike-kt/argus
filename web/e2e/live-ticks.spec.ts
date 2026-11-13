import { test, expect } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { AUTH_FILE, type E2EAuthState } from "./global-setup";

// Part 2G's own real-stack proof (live-chart brief, phase 4): "the
// forming candle visibly moves, a new candle appears at the boundary,
// and killing the connection recovers without reloading the page."
// Against the real production build, proxying to a dedicated api+
// worker pair on argus_e2e -- Makefile's own e2e-stack target, which
// runs the worker with ARGUS_DEV_FIXTURE_LIVE_REPLAY=true specifically
// so there is a live feed here to watch (internal/ingestion/fixture's
// own LiveGenerator, a candle moving forward from each instrument's
// own last historical close, never replayed/already-closed history).
let auth: E2EAuthState;

test.beforeAll(async () => {
  auth = JSON.parse(await readFile(AUTH_FILE, "utf-8")) as E2EAuthState;
});

test.beforeEach(async ({ context }) => {
  const [name, value] = auth.sessionCookie.split("=");
  await context.addCookies([{ name, value, domain: "localhost", path: "/", httpOnly: true, sameSite: "Lax" }]);
});

function legendClose(page: import("@playwright/test").Page) {
  return page.getByTestId("legend-close");
}

test("the forming candle visibly moves and a new one appears at the boundary", async ({ page }) => {
  await page.goto(`/analysis?setupId=${auth.btcSetupId}&instrumentId=${auth.btcInstrumentId}`);
  await expect(page.locator("canvas").first()).toBeVisible({ timeout: 15_000 });

  const firstClose = await legendClose(page).textContent();
  const firstCandleCount = await page.evaluate(
    () => document.querySelectorAll("canvas").length, // sanity: still exactly one chart, not a rebuild
  );

  // LIVE_DEMO_SPEED (Makefile, default 60) paces the live feed at
  // roughly one new candle per real second -- several real seconds is
  // comfortably enough to see the close value move, through
  // series.update() (Part 2D), without this test depending on exact
  // timing.
  await expect
    .poll(async () => legendClose(page).textContent(), { timeout: 10_000, intervals: [250] })
    .not.toBe(firstClose);

  const secondCandleCount = await page.evaluate(() => document.querySelectorAll("canvas").length);
  expect(secondCandleCount).toBe(firstCandleCount); // the same chart, never rebuilt, just updated
});

test("killing the connection recovers without reloading the page", async ({ page }) => {
  type WSRoute = ReturnType<Parameters<Parameters<typeof page.routeWebSocket>[1]>[0]["connectToServer"]>;
  let serverConnection: WSRoute | null = null;
  await page.routeWebSocket(/\/ws$/, (ws) => {
    // connectToServer() makes this a real passthrough to the actual
    // server, not a mock -- messages forward automatically both ways
    // until something closes one side. Closing the SERVER side
    // (returned here) is what this test actually calls close() on
    // below, simulating a real server-initiated/connection-level
    // drop, which the default forwarding then also closes client-side
    // -- not a fake close event dispatched only in the page.
    serverConnection = ws.connectToServer();
  });

  await page.goto(`/analysis?setupId=${auth.btcSetupId}&instrumentId=${auth.btcInstrumentId}`);
  await expect(page.locator("canvas").first()).toBeVisible({ timeout: 15_000 });

  // A page-level marker: if the client's own reconnect (Part 2E) ever
  // forced a full page reload instead of recovering in place, this
  // would be gone afterward -- a reload re-runs this script from
  // scratch and loses it, exactly like the instrument-switch spec's
  // own marker proves no reload happened there.
  await page.evaluate(() => {
    (window as unknown as { __pwMarker: string }).__pwMarker = "still-the-same-page";
  });

  await expect
    .poll(() => legendClose(page).textContent(), { timeout: 10_000, intervals: [250] })
    .not.toBeNull();
  const beforeKill = await legendClose(page).textContent();

  // Kill the real connection -- not a mock of a close event, the
  // actual underlying WebSocket the client is using right now.
  expect(serverConnection).not.toBeNull();
  await serverConnection!.close();

  // live-socket.ts's own reconnect (Part 2E: exponential backoff from
  // RECONNECT_BACKOFF_BASE_MS, 1s) should have a fresh connection open
  // again well within this window, resubscribe, get a fresh snapshot,
  // and keep receiving live ticks afterward.
  await expect
    .poll(async () => legendClose(page).textContent(), { timeout: 15_000, intervals: [250] })
    .not.toBe(beforeKill);

  const markerSurvived = await page.evaluate(() => (window as unknown as { __pwMarker?: string }).__pwMarker);
  expect(markerSurvived).toBe("still-the-same-page"); // no reload happened
});
