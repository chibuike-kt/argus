import { defineConfig, devices } from "@playwright/test";

// Against the real stack (live-chart brief, phase 4), but never the dev
// one: the production build (pnpm build, then pnpm preview -- not the
// dev server, which skips bundling/minification entirely), proxying to
// a dedicated backend on argus_test (never the dev "argus" database --
// `make e2e-stack`, documented in README's own "Running the e2e suite"
// section and in this repo's web/e2e/README.md).
const E2E_API_PORT = process.env.E2E_API_PORT ?? "8090";
const BASE_URL = "http://localhost:4173";

export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  fullyParallel: false, // the whole suite shares one backend + one throwaway user from globalSetup
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
    timeout: 60_000,
    env: { E2E_API_PORT },
  },
});
