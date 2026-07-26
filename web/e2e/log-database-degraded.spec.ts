import { chmod } from "node:fs/promises";

import { expect, test, type Page, type TestInfo } from "@playwright/test";

const fixtureRoot = "/tmp/ai-gateway-chart-e2e-degraded";
const degradedViewports = [
  { name: "mobile", width: 375, height: 812 },
  { name: "tablet", width: 768, height: 1024 },
  { name: "laptop", width: 1280, height: 720 },
  { name: "desktop", width: 1440, height: 900 },
] as const;
const degradedPresentations = [
  { locale: "zh", theme: "light" },
  { locale: "zh", theme: "dark" },
  { locale: "en", theme: "light" },
  { locale: "en", theme: "dark" },
] as const;

async function setPresentation(page: Page, locale: "en" | "zh", theme: "light" | "dark") {
  await page.context().addCookies([{ name: "locale", value: locale, url: "http://localhost:8241" }]);
  await page.evaluate((selectedTheme) => localStorage.setItem("theme", selectedTheme), theme);
}

async function chooseTTFT(page: Page) {
  const metric = page.getByRole("combobox", { name: /Metric|指标/ }).first();
  await metric.click();
  await page.getByRole("option", { name: "TTFT", exact: true }).click();
}

async function gotoPage(page: Page, path: string) {
  let lastStatus: number | undefined;
  for (let attempt = 1; attempt <= 3; attempt++) {
    try {
      const response = await page.goto(path, { waitUntil: "domcontentloaded" });
      lastStatus = response?.status();
      if (lastStatus === undefined || lastStatus < 500) return;
    } catch (error) {
      if (!(error instanceof Error) || !error.message.includes("ERR_ABORTED")) throw error;
      if (attempt === 3) throw error;
    }
    if (attempt < 3) await page.waitForTimeout(250);
  }
  throw new Error(`navigation to ${path} returned HTTP ${lastStatus ?? "unknown"} after 3 attempts`);
}

async function readKpiValue(page: Page, label: RegExp) {
  const card = page
    .locator('[data-slot="card"]')
    .filter({ has: page.getByText(label, { exact: true }) })
    .filter({ has: page.locator(".text-display") });
  await expect(card).toBeVisible();
  return (await card.locator(".text-display").textContent())?.trim() ?? "";
}

async function readStorage(page: Page) {
  return page.evaluate(async () => {
    const token = localStorage.getItem("token");
    const response = await fetch("/api/admin/system/stats", {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    });
    if (!response.ok) throw new Error(`system stats returned ${response.status}`);
    const body = await response.json();
    return body.storage as {
      log_db: { status: "available" | "unavailable" };
      log_delivery_queue: { pending: number; retry: number; inflight: number };
    };
  });
}

async function assertNoViewportOverflowOrCardOverlap(page: Page) {
  await expect.poll(() => page.evaluate(() => {
    const cards = [...document.querySelectorAll('[data-slot="card"]')]
      .filter((card) => card.getClientRects().length > 0);
    const overlaps = (left: Element, right: Element) => {
      const a = left.getBoundingClientRect();
      const b = right.getBoundingClientRect();
      return Math.min(a.right, b.right) - Math.max(a.left, b.left) > 1
        && Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > 1;
    };
    const visibleChildren = (container: Element | null) => container
      ? [...container.children].filter((child) => child.getClientRects().length > 0)
      : [];
    const siblingsClear = (container: Element | null) => {
      const children = visibleChildren(container);
      return children.every((child, index) => {
        const box = child.getBoundingClientRect();
        return box.left >= -1
          && box.right <= innerWidth + 1
          && children.slice(index + 1).every((other) => !overlaps(child, other));
      });
    };
    const cardHeaders = [...document.querySelectorAll('[data-slot="card-header"]')]
      .filter((header) => header.getClientRects().length > 0);
    const wrappedControlGroups = [...document.querySelectorAll("main .flex.flex-wrap")]
      .filter((group) => group.getClientRects().length > 0);
    const pageHeading = document.querySelector("main h1");
    const pageHeadingRow = pageHeading?.parentElement?.parentElement ?? null;
    const main = [...document.querySelectorAll("main")].at(-1) ?? null;
    const pageRoot = visibleChildren(main).at(-1) ?? null;
    return document.documentElement.scrollWidth <= innerWidth
      && cards.every((card, index) => cards.slice(index + 1).every((other) =>
        card.contains(other) || other.contains(card) || !overlaps(card, other),
      ))
      && cardHeaders.every(siblingsClear)
      && wrappedControlGroups.every(siblingsClear)
      && siblingsClear(pageHeadingRow)
      && siblingsClear(pageRoot);
  })).toBe(true);
}

async function screenshot(page: Page, testInfo: TestInfo, name: string) {
  await page.screenshot({ path: testInfo.outputPath(`${name}.png`), fullPage: true });
}

test("real degraded master recovers queued logs without hiding core data", async ({ page, request }, testInfo) => {
  test.setTimeout(300_000);
  const response = await request.post("http://localhost:8240/api/login", {
    data: { username: "admin", password: "chart-e2e-password-strong" },
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  const { token } = await response.json() as { token: string };
  await page.addInitScript((jwt: string) => localStorage.setItem("token", jwt), token);
  await page.context().addCookies([{ name: "token", value: token, url: "http://localhost:8241" }]);

  const initialStats = page.waitForResponse((candidate) => {
    const url = new URL(candidate.url());
    return url.pathname.endsWith("/api/admin/system/stats/") && candidate.status() === 200 && candidate.ok();
  });
  await gotoPage(page, "/system");
  await initialStats;
  await expect(page.locator("main").last()).toBeVisible();
  const storageCard = page
    .getByText(/Storage delivery|存储投递/, { exact: true })
    .locator("xpath=ancestor::*[@data-slot='card']");
  const unavailableLogDatabase = storageCard
    .getByText(/Log database|日志数据库/, { exact: true })
    .locator("xpath=ancestor::section");
  await expect(unavailableLogDatabase.getByText(/^(Unavailable|不可用)$/)).toBeVisible();

  const retry = storageCard.getByRole("button", { name: /Retry queued logs|立即重试日志队列/ });
  const clear = storageCard.getByRole("button", { name: /Clear backlog|清空积压/ });
  await expect(retry).toBeEnabled();
  await expect(clear).toBeEnabled();

  await clear.click();
  const clearDialog = page.getByRole("alertdialog");
  await expect(clearDialog.getByText(/Clear queued log backlog|清空日志队列积压/)).toBeVisible();
  await expect(clearDialog).toContainText("2");
  await clearDialog.getByRole("button", { name: /Cancel|取消/ }).click();
  await expect(clearDialog).not.toBeVisible();
  await expect(retry).toBeEnabled();
  await expect(clear).toBeEnabled();
  await expect.poll(async () => {
    const queue = (await readStorage(page)).log_delivery_queue;
    return queue.pending + queue.retry;
  }).toBe(2);

  try {
    for (const viewport of degradedViewports) {
      for (const presentation of degradedPresentations) {
        await page.setViewportSize(viewport);
        await setPresentation(page, presentation.locale, presentation.theme);
        await gotoPage(page, "/dashboard");
        await expect(page.locator("main").last()).toBeVisible();
        await expect(page.getByText(presentation.locale === "zh" ? "系统总览" : "Dashboard", { exact: true }).first()).toBeVisible();
        await expect.poll(() => page.locator("html").evaluate((element) => element.classList.contains("dark")))
          .toBe(presentation.theme === "dark");
        const requestValue = await readKpiValue(page, /Requests|请求数/);
        expect(requestValue).not.toBe("—");
        expect(Number(requestValue.replaceAll(",", ""))).toBeGreaterThan(0);
        const tokenValue = await readKpiValue(page, /Tokens/);
        expect(tokenValue).not.toBe("—");
        expect(Number.parseFloat(tokenValue)).toBeGreaterThan(0);
        await chooseTTFT(page);
        await expect(page.getByText(/Performance metrics are temporarily unavailable|性能指标.*不可用/)).toBeVisible();
        await expect(page).not.toHaveURL(/\/login/);
        await assertNoViewportOverflowOrCardOverlap(page);

        const logsResponse = page.waitForResponse((candidate) => {
          const url = new URL(candidate.url());
          return /\/api\/logs\/?$/.test(url.pathname) && candidate.status() === 503;
        });
        await gotoPage(page, "/logs");
        await logsResponse;
        const logUnavailableAlert = page
          .getByRole("alert")
          .filter({ hasText: /Log database unavailable|日志数据库不可用/ });
        await expect(logUnavailableAlert).toContainText(/Log database unavailable|日志数据库不可用/);
        await expect(page.getByRole("button", { name: /Retry|重试/ })).toBeVisible();
        await expect(page.locator("main").last().locator("table")).toHaveCount(0);
        await assertNoViewportOverflowOrCardOverlap(page);
        await screenshot(page, testInfo, `${viewport.name}-${presentation.locale}-${presentation.theme}-log-database-unavailable`);
      }
    }
  } finally {
    await chmod(`${fixtureRoot}/log.db`, 0o600);
  }

  await gotoPage(page, "/system");
  const recoveredStorageCard = page
    .getByText(/Storage delivery|存储投递/, { exact: true })
    .locator("xpath=ancestor::*[@data-slot='card']");
  const recoveredRetry = recoveredStorageCard.getByRole("button", { name: /Retry queued logs|立即重试日志队列/ });
  const retryResponse = page.waitForResponse((candidate) =>
    candidate.url().includes("/api/admin/system/log-queue/retry") && candidate.status() === 200,
  );
  await recoveredRetry.click();
  await retryResponse;
  await expect.poll(async () => {
    const storage = await readStorage(page);
    return {
      status: storage.log_db.status,
      pending: storage.log_delivery_queue.pending,
      retry: storage.log_delivery_queue.retry,
      inflight: storage.log_delivery_queue.inflight,
    };
  }, { timeout: 20_000 }).toEqual({ status: "available", pending: 0, retry: 0, inflight: 0 });

  const refreshedStats = page.waitForResponse((candidate) => {
    const url = new URL(candidate.url());
    return url.pathname.endsWith("/api/admin/system/stats/") && candidate.status() === 200;
  });
  await page.getByRole("button", { name: /Refresh|刷新/, exact: true }).click();
  await refreshedStats;
  const recoveredLogDatabase = recoveredStorageCard
    .getByText(/Log database|日志数据库/, { exact: true })
    .locator("xpath=ancestor::section");
  await expect(recoveredLogDatabase.getByText(/^(Available|可用)$/)).toBeVisible();
  for (const label of [/Pending|待投递/, /Retry|待重试/, /In flight|投递中/]) {
    await expect(
      recoveredStorageCard.getByText(label, { exact: true }).locator("..").getByText("0", { exact: true }),
    ).toBeVisible();
  }
  await expect(recoveredRetry).toBeDisabled();
  await expect(recoveredStorageCard.getByRole("button", { name: /Clear backlog|清空积压/ })).toBeDisabled();

  await gotoPage(page, "/dashboard");
  await chooseTTFT(page);
  await expect(page.getByText(/Performance metrics are temporarily unavailable|性能指标.*不可用/)).not.toBeVisible();
  const trendFrame = page.getByTestId("responsive-chart-frame").first();
  await expect(trendFrame).toBeVisible();
  await expect(trendFrame.getByTestId("responsive-chart-plot")).toBeVisible();
});
