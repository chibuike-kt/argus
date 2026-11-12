import { defineConfig } from "vite";
import { fileURLToPath, URL } from "node:url";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// The dev server proxies /api and health endpoints to the Go backend on :8080.
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
    proxy: {
      "/api": "http://127.0.0.1:8080",
      "/healthz": "http://127.0.0.1:8080",
      "/readyz": "http://127.0.0.1:8080",
      // /ws (live-chart brief, phase 4, Part 2B): same target as the
      // REST proxy above, with ws: true so Vite upgrades the proxied
      // connection instead of treating it as a plain HTTP request.
      "/ws": { target: "http://127.0.0.1:8080", ws: true },
    },
  },
});
