import { defineConfig } from "vitest/config";

// In dev, Vite serves the client and forwards server routes to chartroomd
// on its default address (see `make dev`).
const server = "http://127.0.0.1:8080";

export default defineConfig({
  server: {
    proxy: {
      "/healthz": server,
      "/api": server,
      "/assets": server,
      "/ws": { target: server.replace(/^http/, "ws"), ws: true },
    },
  },
  test: {
    environment: "node",
  },
});
