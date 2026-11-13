import { defineConfig } from "vite";
import { fileURLToPath, URL } from "node:url";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// The dev server proxies /api and health endpoints to the Go backend on
// :8080. E2E_API_PORT overrides the target for `pnpm preview` only
// (live-chart brief, phase 4: Playwright runs against the production
// build, proxying to a dedicated test-database backend on its own
// port -- see Makefile's own e2e-stack target -- never the dev :8080
// backend sitting on the dev "argus" database).
const apiTarget = `http://127.0.0.1:${process.env.E2E_API_PORT ?? "8080"}`;

const proxy = {
  "/api": apiTarget,
  "/healthz": apiTarget,
  "/readyz": apiTarget,
  // /ws (live-chart brief, phase 4, Part 2B): same target as the REST
  // proxy above, with ws: true so Vite upgrades the proxied connection
  // instead of treating it as a plain HTTP request.
  "/ws": { target: apiTarget, ws: true },
};

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    port: 5173,
    // The backend's CSRF rule allows exactly http://localhost:5173 and
    // http://127.0.0.1:5173 by default (ARGUS_AUTH_ALLOWED_ORIGINS) --
    // if 5173 is taken, Vite would otherwise silently pick the next
    // free port, and every POST/PUT/PATCH/DELETE would then fail with
    // csrf_rejected for a reason that isn't obvious from the error
    // alone. strictPort makes that failure loud instead (Vite refuses
    // to start) -- see web/README.md.
    strictPort: true,
    proxy,
  },
  preview: {
    port: 4173,
    strictPort: true,
    proxy,
  },
});
