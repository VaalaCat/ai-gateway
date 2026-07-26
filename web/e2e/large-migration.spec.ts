import { expect, test, type APIRequestContext, type Page } from "@playwright/test";

const apiOrigin = "http://localhost:8340";
const webOrigin = "http://localhost:8341";
const apiToken = "large-migration-token";
const expectActiveMigration = process.env.AI_GATEWAY_EXPECT_ACTIVE_MIGRATION !== "false";

interface BillingOverview {
  request_count: number;
  total_cost: number;
}

interface BillingTokenList {
  data: Array<{ token_name: string; request_count: number }>;
}

async function callModel(request: APIRequestContext, model: string, stream = false, timeout = 30_000, requestID?: string) {
  return request.post(`${apiOrigin}/v1/chat/completions`, {
    timeout,
    headers: {
      Authorization: `Bearer ${apiToken}`,
      ...(requestID ? { "X-Vaala-Request-ID": requestID } : {}),
    },
    data: {
      model,
      stream,
      messages: [{ role: "user", content: `exercise ${model}` }],
    },
  });
}

async function login(page: Page, request: APIRequestContext) {
  const response = await request.post(`${apiOrigin}/api/login`, {
    data: { username: "admin", password: "large-migration-password" },
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  const { token } = await response.json() as { token: string };
  await page.addInitScript((jwt: string) => localStorage.setItem("token", jwt), token);
  await page.context().addCookies([{ name: "token", value: token, url: webOrigin }]);
  return token;
}

async function expectNoViewportOverflow(page: Page) {
  await expect.poll(() => page.evaluate(() =>
    document.documentElement.scrollWidth <= window.innerWidth,
  )).toBe(true);
}

async function expectChartsWithinViewport(page: Page) {
  const charts = page.locator("[data-chart]:visible");
  await expect(charts.first()).toBeVisible();
  const violations = await charts.evaluateAll((elements) => elements.flatMap((element) => {
    const rect = element.getBoundingClientRect();
    return rect.left < -1 || rect.right > window.innerWidth + 1 || element.scrollWidth > element.clientWidth + 1
      ? [{ left: rect.left, right: rect.right, viewport: window.innerWidth, scrollWidth: element.scrollWidth, clientWidth: element.clientWidth }]
      : [];
  }));
  expect(violations).toEqual([]);
}

test("large legacy migration remains usable through mock upstream scenarios", async ({ page, request }) => {
  const jwt = await login(page, request);
  await page.goto("/system", { waitUntil: "domcontentloaded" });
  await page.getByTestId("system-maintenance-tab-data-maintenance").click();
  const history = page.getByTestId("history-backfill");
  await expect(history).toBeVisible();
  await expect(page.getByTestId("history-backfill-state")).toHaveAttribute(
    "data-state-value",
    expectActiveMigration ? "copying_billing" : "complete",
    { timeout: 30_000 },
  );
  await expect(page.getByTestId("history-backfill-skip")).toBeDisabled();
  await expect(page.getByTestId("history-backfill-backup")).toContainText(".pre-split.bak");
  await expect(page.getByTestId("history-backfill-billing-cursor")).toHaveText(/\d+ \/ 900000/);
  await expectNoViewportOverflow(page);
  expect(await history.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  await page.screenshot({ path: "test-results/large-migration-system-desktop.png", fullPage: true });

  const success = await callModel(request, "mock-success");
  const successBody = await success.text();
  expect(success.status(), successBody).toBe(200);
  expect(successBody).toContain("mock success");

  const noUsage = await callModel(request, "mock-no-usage");
  const noUsageBody = await noUsage.text();
  expect(noUsage.status(), noUsageBody).toBe(200);
  expect(noUsageBody).toContain("mock success without usage");

  const stream = await callModel(request, "mock-stream", true);
  const streamBody = await stream.text();
  expect(stream.status(), streamBody).toBe(200);
  expect(streamBody).toContain("[DONE]");

  for (const [model, status] of [["mock-429", 429], ["mock-500", 502]] as const) {
    const response = await callModel(request, model);
    expect(response.status(), await response.text()).toBe(status);
  }
  await expect(callModel(request, "mock-timeout", false, 1_000)).rejects.toThrow();
  const connection = await callModel(request, "mock-connection");
  expect(connection.status(), await connection.text()).toBe(502);

  const duplicateRequestID = `large-migration-duplicate-${Date.now()}`;
  for (let attempt = 0; attempt < 2; attempt += 1) {
    const duplicate = await callModel(request, "mock-success", false, 30_000, duplicateRequestID);
    expect(duplicate.status(), await duplicate.text()).toBe(200);
  }

  for (const path of ["/dashboard", "/billing", "/logs", "/monitoring"]) {
    await page.goto(path, { waitUntil: "domcontentloaded" });
    await expect(page).not.toHaveURL(/\/login/);
    await expect(page.locator("main").last()).toBeVisible();
    await expect(page.locator("body")).not.toContainText(/Failed to fetch|Network Error/);
    await expectNoViewportOverflow(page);
  }

  await expect.poll(async () => {
    const response = await request.get(`${apiOrigin}/api/admin/system/stats`, {
      headers: { Authorization: `Bearer ${jwt}` },
    });
    if (!response.ok()) return `http-${response.status()}`;
    const body = await response.json() as { storage: { history_backfill: { state: string } } };
    return body.storage.history_backfill.state;
  }, { timeout: 2_700_000, intervals: [250, 500, 1_000, 2_000] }).toBe("complete");

  await expect.poll(async () => {
    const response = await request.get(`${apiOrigin}/api/logs`, {
      headers: { Authorization: `Bearer ${jwt}` },
      params: { request_id: duplicateRequestID, page: "1", page_size: "20" },
    });
    if (!response.ok()) return -1;
    const body = await response.json() as { total: number };
    return body.total;
  }, { timeout: 30_000, intervals: [250, 500, 1_000] }).toBe(1);

  const overviewResponse = page.waitForResponse((response) =>
    response.ok() && new URL(response.url()).pathname.replace(/\/$/, "") === "/api/billing/overview",
  );
  const tokensResponse = page.waitForResponse((response) =>
    response.ok() && new URL(response.url()).pathname.replace(/\/$/, "") === "/api/billing/tokens",
  );
  await page.goto("/billing", { waitUntil: "domcontentloaded" });
  const [overview, tokens] = await Promise.all([
    overviewResponse.then((response) => response.json() as Promise<BillingOverview>),
    tokensResponse.then((response) => response.json() as Promise<BillingTokenList>),
  ]);
  expect(overview.request_count).toBeGreaterThan(0);
  expect(overview.total_cost).toBeGreaterThan(0);
  expect(tokens.data).toEqual(expect.arrayContaining([
    expect.objectContaining({ token_name: "Large migration trace token", request_count: expect.any(Number) }),
  ]));
  await expect(page.locator("main").last()).toContainText("Large migration trace token");

  for (const path of ["/dashboard", "/billing", "/monitoring"]) {
    await page.goto(path, { waitUntil: "domcontentloaded" });
    await expectChartsWithinViewport(page);
  }

  await page.setViewportSize({ width: 390, height: 844 });
  for (const path of ["/dashboard", "/billing", "/logs", "/monitoring", "/system"]) {
    await page.goto(path, { waitUntil: "domcontentloaded" });
    await expect(page.locator("main").last()).toBeVisible();
    await expectNoViewportOverflow(page);
    if (path !== "/logs" && path !== "/system") await expectChartsWithinViewport(page);
  }

  await page.getByTestId("system-maintenance-tab-data-maintenance").click();
  await expect(page.getByTestId("history-backfill")).toBeVisible();
  expect(await page.getByTestId("history-backfill").evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  await page.screenshot({ path: "test-results/large-migration-system-mobile.png", fullPage: true });

  await page.goto("/logs", { waitUntil: "domcontentloaded" });
  await expect(page.locator("main").last()).toContainText(/mock-|legacy\/history-model/);
  await page.screenshot({ path: "test-results/large-migration-logs.png", fullPage: true });
});
