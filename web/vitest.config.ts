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
  },
});
