import { defineConfig, devices } from "@playwright/test";

// Against the real stack (live-chart brief, phase 4), but never the dev
// one: the production build (pnpm build, then pnpm preview -- not the
// dev server, which skips bundling/minification entirely), proxying to
// a dedicated backend on argus_e2e (never the dev "argus" database --
// `make e2e-stack`), documented in this package's own README.md under
// "Running the e2e suite".
const E2E_API_PORT = process.env.E2E_API_PORT ?? "8090";
const BASE_URL = "http://localhost:4173";

export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  fullyParallel: false, // the whole suite shares one backend + one throwaway user from globalSetup
  // fullyParallel: false alone still let Playwright run different spec
  // FILES in parallel workers (observed directly: two unrelated specs
  // both stalled for over 11 minutes waiting on a canvas that should
  // appear in under 15s, once they were contending for the same CPU,
  // the same single dev-fixture backend, and the same one shared
  // session/browser-storage state from globalSetup at the same time).
  // workers: 1 is the actual fix -- one test at a time, the whole suite
  // through.
  workers: 1,
  globalSetup: "./e2e/global-setup.ts",
  globalTeardown: "./e2e/global-teardown.ts",
  use: {
    baseURL: BASE_URL,
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: "pnpm build && pnpm preview",
    url: BASE_URL,
    reuseExistingServer: false,
    // 60s was occasionally too tight for `tsc -b && vite build` alone
    // under load (this machine running the e2e-stack's own worker/api
    // processes concurrently) -- observed as a real webServer timeout
    // with pnpm preview itself starting in well under 5s once the
    // build actually finished.
    timeout: 120_000,
    env: { E2E_API_PORT },
  },
});
