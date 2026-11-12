import { defineConfig, devices } from "@playwright/test";

// Against the real stack (live-chart brief, phase 4): the real Vite
// dev server, proxying to the real Go API/worker/Postgres/Redis
// (docker compose, already running) -- never a mocked backend.
// webServer is left unset deliberately: the dev server and the
// backend containers are started once, outside this config, and
// reused across runs rather than this file owning their lifecycle
// (the backend is a whole docker-compose stack, not something
// Playwright itself should start/stop per test run).
export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  use: {
    baseURL: "http://localhost:5173",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
