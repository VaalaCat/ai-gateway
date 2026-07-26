import { defineConfig, devices } from "@playwright/test";

const repoRoot = "..";
const fixtureRoot = "/tmp/ai-gateway-chart-e2e-large-migration";

export default defineConfig({
  testDir: "./e2e",
  testMatch: /large-migration\.spec\.ts/,
  fullyParallel: false,
  workers: 1,
  timeout: 3_600_000,
  expect: { timeout: 20_000 },
  use: {
    ...devices["Desktop Chrome"],
    baseURL: "http://localhost:8341",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  webServer: [
    {
      command: `go run ${repoRoot}/test/mock_upstream --listen :8342 --timeout-delay 10s`,
      url: "http://localhost:8342/healthz",
      timeout: 120_000,
      reuseExistingServer: true,
    },
    {
      command: `go run ${repoRoot}/test/e2e_fixture --root ${fixtureRoot} --listen :8340 --web-origin http://localhost:8341 --legacy-history --history-days 90 --requests-per-day 10000 --trace-every 10 --mock-upstream-url http://127.0.0.1:8342 && go run ${repoRoot}/cmd master --config ${fixtureRoot}/config.yaml`,
      url: "http://localhost:8340/ping",
      timeout: 3_600_000,
      reuseExistingServer: true,
    },
    {
      command: "node e2e/start-next-e2e.mjs largeMigration",
      url: "http://localhost:8341/login",
      timeout: 300_000,
      reuseExistingServer: true,
    },
  ],
});
