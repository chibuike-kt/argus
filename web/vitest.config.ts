import { defineConfig } from "vitest/config";
import { fileURLToPath, URL } from "node:url";

// Store and hit-test unit tests are pure logic (no DOM), so the default
// node environment is enough -- no jsdom dependency needed.
export default defineConfig({
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  test: {
    environment: "node",
    setupFiles: ["./src/test-setup.ts"],
    // e2e/ is Playwright's own suite (live-chart brief, phase 4,
    // Part 2A's real-stack proof) -- a different test runner, run via
    // `pnpm exec playwright test`, never vitest's own discovery.
    exclude: ["**/node_modules/**", "**/e2e/**"],
  },
});
