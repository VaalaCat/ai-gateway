import { defineConfig, devices } from "@playwright/test";

const repoRoot = "..";

export default defineConfig({
  testDir: "./e2e",
  workers: 1,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  use: {
    ...devices["Desktop Chrome"],
    baseURL: "http://localhost:8241",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  testMatch: /log-database-degraded\.spec\.ts/,
  webServer: [
    {
      command: `go run ${repoRoot}/test/e2e_fixture --root /tmp/ai-gateway-chart-e2e-degraded --listen :8240 --web-origin http://localhost:8241 --degraded && go run ${repoRoot}/cmd master --config /tmp/ai-gateway-chart-e2e-degraded/config.yaml`,
      url: "http://localhost:8240/ping",
      timeout: 180_000,
      reuseExistingServer: false,
    },
    {
      command: "node e2e/start-next-e2e.mjs degraded",
      url: "http://localhost:8241/login",
      timeout: 300_000,
      reuseExistingServer: false,
    },
  ],
});
