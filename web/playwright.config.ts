import { defineConfig, devices } from "@playwright/test";

// The end-to-end tests live in ../e2e (ADR-001). `npm run e2e` sets
// NODE_PATH so they can import @playwright/test from here.
export default defineConfig({
  testDir: "../e2e",
  timeout: 60_000,
  retries: 0,
  reporter: process.env["CI"] ? [["list"], ["html", { open: "never" }]] : "list",
  use: { ...devices["Desktop Chrome"], trace: "retain-on-failure" },
});
