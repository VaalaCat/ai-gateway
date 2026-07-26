import { defineConfig, devices } from "@playwright/test";

const repoRoot = "..";

export default defineConfig({
  testDir: "./e2e",
  globalSetup: "./e2e/global-setup.ts",
  fullyParallel: false,
  workers: 1,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  use: {
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"], baseURL: "http://localhost:8141" },
      testIgnore: /log-database-degraded\.spec\.ts/,
    },
  ],
  webServer: [
    {
      command: `go run ${repoRoot}/test/e2e_fixture --root /tmp/ai-gateway-chart-e2e --listen :8140 --web-origin http://localhost:8141 && go run ${repoRoot}/cmd master --config /tmp/ai-gateway-chart-e2e/config.yaml`,
      url: "http://localhost:8140/ping",
      timeout: 180_000,
      reuseExistingServer: false,
    },
    {
      command: "node e2e/start-next-e2e.mjs normal",
      url: "http://localhost:8141/login",
      timeout: 300_000,
      reuseExistingServer: false,
    },
  ],
});
