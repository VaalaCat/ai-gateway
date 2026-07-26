import { expect, test, type APIRequestContext, type Locator, type Page, type TestInfo } from "@playwright/test";

const credentials = { username: "admin", password: "chart-e2e-password-strong" };
const fixtureUserPassword = "fixture-password-strong";
const viewports = [
  { name: "mobile", width: 375, height: 812 },
  { name: "tablet", width: 768, height: 1024 },
  { name: "laptop", width: 1280, height: 720 },
  { name: "desktop", width: 1440, height: 900 },
] as const;
const presentations = [
  { locale: "zh", theme: "light" },
  { locale: "zh", theme: "dark" },
  { locale: "en", theme: "light" },
  { locale: "en", theme: "dark" },
] as const;
const pages = [
  "/dashboard",
  "/billing",
  "/byok",
  "/monitoring",
  "/monitoring/insight?type=agent&id=fixture-agent-1",
  "/system",
] as const;
const chartMarkSelector = [
  ".recharts-line-curve",
  ".recharts-line-dot",
  ".recharts-area-area",
  ".recharts-bar-rectangle",
  ".recharts-sector",
].join(",");

interface BillingInsights {
  trend: unknown[];
  cost_trend_stacked: { buckets: unknown[]; series_order: string[] };
  cache_saving: Record<string, unknown>;
}

interface BillingOverview {
  request_count: number;
}

interface BillingList {
  data: unknown[];
  total: number;
}

interface ApiResult<T> {
  url: URL;
  body: T;
}

async function login(
  request: APIRequestContext,
  page: Page,
  apiOrigin = "http://localhost:8140",
  webOrigin = "http://localhost:8141",
) {
  const response = await request.post(`${apiOrigin}/api/login`, { data: credentials });
  expect(response.ok(), await response.text()).toBeTruthy();
  const { token } = await response.json() as { token: string };
  await page.addInitScript((jwt: string) => localStorage.setItem("token", jwt), token);
  await page.context().addCookies([{ name: "token", value: token, url: webOrigin }]);
}

async function setPresentation(page: Page, locale: "en" | "zh", theme: "light" | "dark") {
  await page.context().addCookies([{ name: "locale", value: locale, url: "http://localhost:8141" }]);
  await page.addInitScript((selectedTheme: string) => {
    localStorage.setItem("theme", selectedTheme);
  }, theme);
}

function sameApiPath(candidate: URL, path: string) {
  return candidate.pathname.replace(/\/$/, "") === path.replace(/\/$/, "");
}

async function waitForJson<T>(
  page: Page,
  path: string,
  matches: (url: URL) => boolean = () => true,
): Promise<ApiResult<T>> {
  const response = await page.waitForResponse((candidate) => {
    const url = new URL(candidate.url());
    return candidate.ok() && sameApiPath(url, path) && matches(url);
  });
  return { url: new URL(response.url()), body: await response.json() as T };
}

function normalizedText(value: string) {
  return value.replace(/\s+/g, " ").trim();
}

async function assertResponsiveLayout(
	page: Page,
	expectCharts: boolean,
	expectMarks = expectCharts,
	expected: { valid: true } | { valid: false; geometryViolation: string } = { valid: true },
) {
  await expect.poll(() => page.evaluate(() => document.readyState !== "loading")).toBe(true);
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

	let snapshot: {
		viewportWidth: number;
		layouts: Array<{ markCount: number; visibleMarkCount: number }>;
		cardsClear: boolean;
		controlsClear: boolean;
		controlViolations: string[];
		axisViolations: string[];
		markViolations: string[];
		legendShellViolations: string[];
		geometryViolations: string[];
		sectionsClear: boolean;
		sectionViolations: string[];
		valid: boolean;
	} | undefined;
  await expect.poll(async () => {
    snapshot = await page.evaluate(({ marks, requireMarks }) => {
    const rect = (element: Element | null) => {
      if (!element) return null;
      const box = element.getBoundingClientRect();
      return {
        left: box.left,
        right: box.right,
        top: box.top,
        bottom: box.bottom,
        width: box.width,
        height: box.height,
      };
    };
    const overlaps = (a: ReturnType<typeof rect>, b: ReturnType<typeof rect>) => {
      if (!a || !b) return false;
      return Math.min(a.right, b.right) - Math.max(a.left, b.left) > 1
        && Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > 1;
    };
		const contains = (outer: ReturnType<typeof rect>, inner: ReturnType<typeof rect>, tolerance = 1) =>
			outer != null
			&& inner != null
			&& inner.left >= outer.left - tolerance
			&& inner.right <= outer.right + tolerance
			&& inner.top >= outer.top - tolerance
			&& inner.bottom <= outer.bottom + tolerance;
    const frames = [...document.querySelectorAll('[data-slot="responsive-chart-frame"]')];
    const cards = [...document.querySelectorAll('[data-slot="card"]')]
      .filter((card) => card.getClientRects().length > 0);
    const layouts = frames.map((frame) => {
      const card = frame.closest('[data-slot="card"]');
      const header = card?.querySelector('[data-slot="card-header"]') ?? null;
      const plot = frame.querySelector('[data-slot="responsive-chart-plot"]');
      const legend = frame.querySelector('[data-slot="responsive-chart-legend"]');
			const legendShell = frame.querySelector('[data-slot="chart-legend-shell"]');
			const cardRect = rect(card);
      const frameRect = rect(frame);
      const plotRect = rect(plot);
      const headerRect = rect(header);
      const legendRect = rect(legend);
			const legendShellRect = rect(legendShell);
			const visibleMarks = [...frame.querySelectorAll(marks)]
				.map((mark) => ({ mark, markRect: rect(mark) }))
				.filter(({ mark, markRect }) => mark.getClientRects().length > 0
					&& markRect != null
					&& (markRect.width > 0 || markRect.height > 0));
			const markViolations = visibleMarks
				.filter(({ markRect }) => !contains(plotRect, markRect))
				.map(({ mark }) => `${mark.tagName.toLowerCase()}:outside-plot`);
			const legendShellClear = !legendShell || contains(legendRect, legendShellRect);
			const geometryViolations = [
				...(!contains(cardRect, frameRect) ? ["frame:outside-card"] : []),
				...(!contains(frameRect, plotRect) ? ["plot:outside-frame"] : []),
				...(!contains(cardRect, plotRect) ? ["plot:outside-card"] : []),
				...(legend && !contains(frameRect, legendRect) ? ["legend:outside-frame"] : []),
				...(legend && !contains(cardRect, legendRect) ? ["legend:outside-card"] : []),
			];
	      const axisViolations = [...frame.querySelectorAll(".recharts-cartesian-axis-tick-value")]
	        .filter((tick) => tick.getClientRects().length > 0)
	        .flatMap((tick) => {
	          const tickRect = rect(tick)!;
	          const violations = tickRect.left < -1 || tickRect.right > innerWidth + 1
	            ? [`${tick.textContent ?? "tick"}:outside`]
	            : [];
	          for (const otherCard of cards) {
	            if (otherCard !== card && overlaps(tickRect, rect(otherCard))) {
	              violations.push(`${tick.textContent ?? "tick"}:overlap:${otherCard.querySelector('[data-slot="card-title"]')?.textContent ?? "card"}`);
	            }
	          }
	          return violations;
	        });
      return {
        frame: frameRect,
        plot: plotRect,
        header: headerRect,
        legend: legendRect,
        markCount: frame.querySelectorAll(marks).length,
				visibleMarkCount: visibleMarks.length,
				geometryClear: geometryViolations.length === 0,
				geometryViolations: [
					...geometryViolations,
					...(!frameRect || frameRect.height <= 0 ? ["frame:empty"] : []),
					...(!plotRect || plotRect.width <= 0 || plotRect.height <= 0 ? ["plot:empty"] : []),
				],
        headerClear: !headerRect || !plotRect || headerRect.bottom <= plotRect.top + 1,
        legendClear: !overlaps(plotRect, legendRect),
				legendShellClear,
				legendShellViolations: legendShellClear ? [] : ["legend-shell:outside-legend"],
				marksClear: markViolations.length === 0,
				markViolations,
	        axesClear: axisViolations.length === 0,
	        axisViolations,
      };
    });
	    const cardsClear = cards.every((card, index) => cards.slice(index + 1).every((other) => {
	      if (card.contains(other) || other.contains(card)) return true;
	      return !overlaps(rect(card), rect(other));
	    }));
	    const visibleChildren = (container: Element | null) => container
	      ? [...container.children].filter((child) => child.getClientRects().length > 0)
	      : [];
	    const siblingViolations = (container: Element | null) => {
	      const children = visibleChildren(container);
	      return children.flatMap((child, index) => {
	        const childRect = rect(child)!;
	        const label = `${child.tagName.toLowerCase()}.${[...child.classList].join(".")}`;
	        const violations = childRect.left < -1 || childRect.right > innerWidth + 1
	          ? [`${label}:outside`]
	          : [];
	        for (const other of children.slice(index + 1)) {
	          if (overlaps(childRect, rect(other))) violations.push(`${label}:overlap:${other.tagName.toLowerCase()}.${[...other.classList].join(".")}`);
	        }
	        return violations;
	      });
	    };
	    const cardHeaders = [...document.querySelectorAll('[data-slot="card-header"]')]
	      .filter((header) => header.getClientRects().length > 0);
	    const wrappedControlGroups = [...document.querySelectorAll("main .flex.flex-wrap")]
	      .filter((group) => group.getClientRects().length > 0);
	    const pageHeading = document.querySelector("main h1");
	    const pageHeadingRow = pageHeading?.parentElement?.parentElement ?? null;
	    const controlViolations = [
	      ...cardHeaders.flatMap(siblingViolations),
	      ...wrappedControlGroups.flatMap(siblingViolations),
	      ...siblingViolations(pageHeadingRow),
	    ];
	    const controlsClear = controlViolations.length === 0;
	    const main = [...document.querySelectorAll("main")].at(-1) ?? null;
	    const pageRoot = visibleChildren(main).at(-1) ?? null;
	    const sectionViolations = siblingViolations(pageRoot);
	    const sectionsClear = sectionViolations.length === 0;
	    return {
	      viewportWidth: innerWidth,
	      layouts,
	      cardsClear,
	      controlsClear,
	      controlViolations,
	      axisViolations: layouts.flatMap((layout) => layout.axisViolations),
			markViolations: layouts.flatMap((layout) => layout.markViolations),
			legendShellViolations: layouts.flatMap((layout) => layout.legendShellViolations),
			geometryViolations: [
				...layouts.flatMap((layout) => layout.geometryViolations),
				...(requireMarks && !layouts.some((layout) => layout.visibleMarkCount > 0) ? ["marks:none-visible"] : []),
			],
	      sectionsClear,
	      sectionViolations,
      valid: layouts.every((layout) => layout.frame != null
        && layout.frame.left >= -1
        && layout.frame.right <= innerWidth + 1
        && layout.frame.height > 0
        && layout.plot != null
        && layout.plot.width > 0
        && layout.plot.height > 0
				&& layout.geometryClear
        && layout.headerClear
        && layout.legendClear
				&& layout.legendShellClear
				&& layout.marksClear
        && layout.axesClear)
				&& (!requireMarks || layouts.some((layout) => layout.visibleMarkCount > 0)),
    };
    }, { marks: chartMarkSelector, requireMarks: expectMarks });
    return snapshot;
	  }, { timeout: expectCharts ? 20_000 : 10_000 }).toMatchObject(expected.valid ? {
		valid: true,
		cardsClear: true,
		controlsClear: true,
		controlViolations: [],
		axisViolations: [],
		markViolations: [],
		legendShellViolations: [],
		geometryViolations: [],
		sectionsClear: true,
		sectionViolations: [],
	} : {
		valid: false,
		geometryViolations: expect.arrayContaining([expected.geometryViolation]),
	});

	if (expectCharts) expect(snapshot!.layouts.length).toBeGreaterThan(0);
	if (expectMarks) expect(snapshot!.layouts.some((layout) => layout.visibleMarkCount > 0)).toBe(true);
}

async function openDashboardPage(page: Page, path: string) {
  await page.goto(path, { waitUntil: "domcontentloaded" });
  await expect(page).not.toHaveURL(/\/login/);
  await expect(page.locator("main").last()).toBeVisible();
}

async function loginFixtureUser(page: Page, username: "fixture-alice" | "fixture-bob") {
  await page.goto("/login", { waitUntil: "domcontentloaded" });
  await page.locator("#username").fill(username);
  await page.locator("#password").fill(fixtureUserPassword);
  const loginResponse = page.waitForResponse((candidate) =>
    candidate.ok()
      && candidate.request().method() === "POST"
      && sameApiPath(new URL(candidate.url()), "/api/login"),
  );
  await page.locator("form").getByRole("button").click();
  const response = await loginResponse;
  expect(response.ok(), await response.text()).toBeTruthy();
  await expect(page).toHaveURL(/\/dashboard\/?$/);
}

async function logoutThroughUserMenu(page: Page) {
  await page.locator('header [data-slot="dropdown-menu-trigger"]').last().click();
  await page.getByRole("menuitem", { name: /Logout|退出登录/ }).click();
  await expect(page).toHaveURL(/\/login\/?$/);
  await expect.poll(() => page.evaluate(() => localStorage.getItem("token"))).toBeNull();
}

async function readStoredChartTopN(page: Page, userId: number) {
  return page.evaluate((id) => localStorage.getItem(`chartTopN:${id}:${location.pathname}`), userId);
}

async function chooseSelectIndex(
  page: Page,
  trigger: Locator,
  index: number,
  optionCount?: number,
) {
  await trigger.click();
  const options = page.getByRole("option").filter({ visible: true });
  if (optionCount !== undefined) await expect(options).toHaveCount(optionCount);
  await options.nth(index).click();
}

async function chooseEntity(page: Page, trigger: Locator, name: string | RegExp) {
  await trigger.click();
  await page.getByRole("option", { name, exact: typeof name === "string" }).filter({ visible: true }).click();
}

async function readFrameSizes(page: Page) {
  return page.getByTestId("responsive-chart-frame").evaluateAll((frames) => frames.map((frame) => {
    const box = frame.getBoundingClientRect();
    return { width: box.width, height: box.height };
  }));
}

async function expectFrameSizes(page: Page, expected: Array<{ width: number; height: number }>) {
  await expect.poll(() => readFrameSizes(page)).toEqual(expected);
}

async function chooseAndWaitForTrend(
  page: Page,
  trigger: Locator,
  index: number,
  metric: "ttft" | "tps",
  stat: "p95" | "p5",
) {
  const response = waitForJson(page, "/api/stats/metric-trend", (url) =>
    url.searchParams.get("metric") === metric && url.searchParams.get("stat") === stat,
  );
  await chooseSelectIndex(page, trigger, index, 2);
  await response;
}

async function expectTooltipWithinViewport(page: Page, frame: Locator, requireScroll = false) {
  const wrapper = frame.locator(".recharts-wrapper").first();
  await expect(wrapper).toBeVisible();
  const box = await wrapper.boundingBox();
  expect(box).not.toBeNull();
  const tooltip = page.locator('[role="tooltip"]:visible').last();
  const marks = frame.locator(chartMarkSelector);
  for (let index = 0; index < Math.min(await marks.count(), 3); index++) {
    await marks.nth(index).hover({ force: true });
    if (await tooltip.isVisible().catch(() => false)) break;
  }
  for (const fraction of [0.5, 0.35, 0.65, 0.2, 0.8]) {
    if (await tooltip.isVisible().catch(() => false)) break;
    await page.mouse.move(box!.x + box!.width * fraction, box!.y + box!.height * 0.45);
  }
  await expect(tooltip).toBeVisible();
  const metrics = await tooltip.evaluate((element) => {
    const rect = element.getBoundingClientRect();
    return {
      left: rect.left,
      right: rect.right,
      top: rect.top,
      bottom: rect.bottom,
      width: rect.width,
      height: rect.height,
      clientHeight: element.clientHeight,
      scrollHeight: element.scrollHeight,
      viewportWidth: innerWidth,
      viewportHeight: innerHeight,
    };
  });
  expect(metrics.left).toBeGreaterThanOrEqual(-1);
  expect(metrics.right).toBeLessThanOrEqual(metrics.viewportWidth + 1);
  expect(metrics.top).toBeGreaterThanOrEqual(-1);
  expect(metrics.bottom).toBeLessThanOrEqual(metrics.viewportHeight + 1);
  expect(metrics.width).toBeLessThanOrEqual(metrics.viewportWidth - 32 + 1);
  expect(metrics.height).toBeLessThanOrEqual(metrics.viewportHeight / 2 + 1);
  if (requireScroll) expect(metrics.scrollHeight).toBeGreaterThan(metrics.clientHeight);
}

async function screenshot(page: Page, testInfo: TestInfo, name: string) {
  await page.screenshot({ path: testInfo.outputPath(`${name}.png`), fullPage: true });
}

async function withTemporaryTransform(
	element: Locator,
	transform: string,
	assertion: () => Promise<void>,
) {
	const originalStyle = await element.getAttribute("style");
	await element.evaluate((node, value) => {
		(node as HTMLElement).style.transform = value;
	}, transform);
	try {
		await assertion();
	} finally {
		await element.evaluate((node, style) => {
			if (style == null) node.removeAttribute("style");
			else node.setAttribute("style", style);
		}, originalStyle);
	}
}

async function expectActiveSystemPanelContained(page: Page, expected = true) {
	await expect.poll(() => page.evaluate(() => {
		const selectedTab = document.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]');
		const tablist = selectedTab?.closest('[role="tablist"]') ?? null;
		const panelID = selectedTab?.getAttribute("aria-controls");
		const panel = panelID ? document.getElementById(panelID) : null;
		if (!selectedTab || !tablist || !panel || panel.getClientRects().length === 0) return false;

		const panelRect = panel.getBoundingClientRect();
		const tablistRect = tablist.getBoundingClientRect();
		const visibleSections = [...panel.children]
			.filter((section) => section.getClientRects().length > 0);
		const visibleCards = [...panel.querySelectorAll('[data-slot="card"]')]
			.filter((card) => card.getClientRects().length > 0);
		const overlaps = (a: DOMRect, b: DOMRect) =>
			Math.min(a.right, b.right) - Math.max(a.left, b.left) > 1
			&& Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > 1;
		const contains = (outer: DOMRect, inner: DOMRect, tolerance = 1) =>
			inner.left >= outer.left - tolerance
			&& inner.right <= outer.right + tolerance
			&& inner.top >= outer.top - tolerance
			&& inner.bottom <= outer.bottom + tolerance;
		const containedAndSeparate = (elements: Element[]) => elements.every((element, index) => {
			const rect = element.getBoundingClientRect();
			return contains(panelRect, rect)
			&& rect.left >= -1
			&& rect.right <= innerWidth + 1
			&& elements.slice(index + 1).every((other) =>
				element.contains(other)
				|| other.contains(element)
				|| !overlaps(rect, other.getBoundingClientRect()));
		});

		return document.documentElement.scrollWidth <= innerWidth
			&& panelRect.left >= -1
			&& panelRect.right <= innerWidth + 1
			&& !overlaps(panelRect, tablistRect)
			&& containedAndSeparate(visibleSections)
			&& containedAndSeparate(visibleCards);
	})).toBe(expected);
}

for (const viewport of [viewports[0], viewports[3]]) {
	test(`System maintenance tabs preserve unsaved drafts at ${viewport.name}`, async ({ page, request }, testInfo) => {
		test.setTimeout(120_000);
		await page.setViewportSize(viewport);
		await login(request, page);
		await setPresentation(page, "en", "light");
		let updateRequests = 0;
		page.on("request", (candidate) => {
			if (candidate.method() === "PUT" && sameApiPath(new URL(candidate.url()), "/api/admin/system/settings")) {
				updateRequests += 1;
			}
		});
		await openDashboardPage(page, "/system");

		const tabs = page.getByRole("tablist", { name: /System maintenance sections|系统维护分类/ });
		const tabNames = [
			/Overview|概览/,
			/Request path|请求链路/,
			/Policy & billing|策略与计费/,
			/^BYOK$/,
			/Data maintenance|数据维护/,
		];
		await expect(tabs.getByRole("tab")).toHaveCount(tabNames.length);
		expect(await tabs.getByRole("tab").allTextContents()).toEqual([
			"Overview",
			"Request path",
			"Policy & billing",
			"BYOK",
			"Data maintenance",
		]);

		for (const name of tabNames) {
			const tab = tabs.getByRole("tab", { name });
			await tab.click();
			await expect(tab).toHaveAttribute("aria-selected", "true");
			await expectActiveSystemPanelContained(page);
		}

		await tabs.getByRole("tab", { name: /Request path|请求链路/ }).click();
		const fallbackField = page.getByText(/Fallback interval|Fallback 间隔/, { exact: true })
			.locator("..").getByRole("spinbutton");
		await expect(fallbackField).toBeVisible();
		const fallbackDraft = String(Number(await fallbackField.inputValue()) === 4321 ? 4322 : 4321);
		await fallbackField.fill(fallbackDraft);
		await tabs.getByRole("tab", { name: /Policy & billing|策略与计费/ }).click();
		await expectActiveSystemPanelContained(page);
		await tabs.getByRole("tab", { name: /Request path|请求链路/ }).click();
		await expect(fallbackField).toHaveValue(fallbackDraft);

		await tabs.getByRole("tab", { name: /^BYOK$/ }).click();
		const maxChannelsField = page.getByText(/Max private channels per user|每个用户最多 private channel 数/)
			.locator("..").getByRole("spinbutton");
		await expect(maxChannelsField).toBeVisible();
		const maxChannelsDraft = String(Number(await maxChannelsField.inputValue()) === 37 ? 38 : 37);
		await maxChannelsField.fill(maxChannelsDraft);
		await tabs.getByRole("tab", { name: /Overview|概览/ }).click();
		await expectActiveSystemPanelContained(page);
		await tabs.getByRole("tab", { name: /^BYOK$/ }).click();
		await expect(maxChannelsField).toHaveValue(maxChannelsDraft);
		expect(updateRequests).toBe(0);
		await expectActiveSystemPanelContained(page);
		await screenshot(page, testInfo, `system-tabs-${viewport.name}`);
	});
}

test("containment assertions reject translated chart and System content", async ({ page, request }) => {
	test.setTimeout(120_000);
	await page.setViewportSize(viewports[0]);
	await login(request, page);
	await setPresentation(page, "en", "light");
	await openDashboardPage(page, "/dashboard");

	const frameWithLegend = page.getByTestId("responsive-chart-frame")
		.filter({ has: page.locator('[data-slot="chart-legend-shell"]') })
		.first();
	const legend = frameWithLegend.getByTestId("responsive-chart-legend");
	await expect(legend).toBeVisible();
	await withTemporaryTransform(legend, "translateY(16px)", async () => {
		await assertResponsiveLayout(page, true, true, {
			valid: false,
			geometryViolation: "legend:outside-frame",
		});
	});
	await assertResponsiveLayout(page, true);

	await openDashboardPage(page, "/system");
	const selectedTab = page.getByRole("tab", { name: /Overview|概览/ });
	const panelID = await selectedTab.getAttribute("aria-controls");
	expect(panelID).not.toBeNull();
	const panel = page.locator(`[id="${panelID}"]`);
	const card = panel.locator('[data-slot="card"]').first();
	await withTemporaryTransform(card, "translateY(-16px)", async () => {
		await expectActiveSystemPanelContained(page, false);
	});
	await expectActiveSystemPanelContained(page);
	await withTemporaryTransform(panel, "translateY(-40px)", async () => {
		await expectActiveSystemPanelContained(page, false);
	});
	await expectActiveSystemPanelContained(page);
});

for (const viewport of viewports) {
  for (const presentation of presentations) {
    test(`loaded pages stay responsive at ${viewport.name} in ${presentation.locale} ${presentation.theme}`, async ({ page, request }, testInfo) => {
	  test.setTimeout(180_000);
      await page.setViewportSize(viewport);
      await login(request, page);
      await setPresentation(page, presentation.locale, presentation.theme);
      for (const path of pages) {
		await openDashboardPage(page, path);
		await expect.poll(() => page.locator("html").evaluate((element) => element.classList.contains("dark")))
		  .toBe(presentation.theme === "dark");
		if (path === "/dashboard") {
		  await expect(page.getByText(presentation.locale === "zh" ? "系统总览" : "Dashboard", { exact: true }).first()).toBeVisible();
		}
		if (path === "/billing") {
		  await expect(page.getByText(presentation.locale === "zh" ? "计费" : "Billing", { exact: true }).first()).toBeVisible();
		}
		const expectCharts = path === "/dashboard"
		  || path === "/billing"
		  || path === "/monitoring"
		  || path.startsWith("/monitoring/insight");
		await assertResponsiveLayout(page, expectCharts);
		const slug = path.split("?")[0].replaceAll("/", "-").replace(/^-/, "") || "home";
		await screenshot(page, testInfo, `${viewport.name}-${presentation.locale}-${presentation.theme}-${slug}`);
      }
    });
  }
}

for (const viewport of viewports) {
test(`Dashboard interactions preserve chart frames at ${viewport.name}`, async ({ page, request }, testInfo) => {
  test.setTimeout(120_000);
  await page.setViewportSize(viewport);
  await login(request, page);
  await setPresentation(page, "en", "light");
  const distribution5 = waitForJson<{ series_order: string[] }>(page, "/api/stats/model-distribution", (url) => url.searchParams.get("top_n") === "5");
  const market5 = waitForJson<{ series_order: string[] }>(page, "/api/stats/market-share", (url) => url.searchParams.get("top_n") === "5");
  await openDashboardPage(page, "/dashboard?top_n=5");
  await Promise.all([distribution5, market5]);
  await expect(page.getByTestId("responsive-chart-frame").first()).toBeVisible();

  const dimension = page.getByRole("combobox", { name: /Group by|维度/ }).first();
  const grouped5 = waitForJson<{ series_order: string[] }>(page, "/api/stats/metric-trend", (url) =>
    url.searchParams.get("dim") === "model" && url.searchParams.get("top_n") === "5",
  );
  await chooseSelectIndex(page, dimension, 1, 3);
  await grouped5;
  const initialFrames = await readFrameSizes(page);
  const topN = page.getByRole("combobox", { name: /Top N|前 N 项/ });

  for (const value of [10, 20] as const) {
    const distribution = waitForJson<{ series_order: string[] }>(page, "/api/stats/model-distribution", (url) => url.searchParams.get("top_n") === String(value));
    const market = waitForJson<{ series_order: string[] }>(page, "/api/stats/market-share", (url) => url.searchParams.get("top_n") === String(value));
    const grouped = waitForJson<{ series_order: string[] }>(page, "/api/stats/metric-trend", (url) => url.searchParams.get("top_n") === String(value));
    await chooseSelectIndex(page, topN, value === 10 ? 1 : 2, 3);
    const results = await Promise.all([distribution, market, grouped]);
    for (const result of results) {
      expect(result.url.searchParams.get("top_n")).toBe(String(value));
      expect(result.body.series_order.length).toBeGreaterThan(0);
      expect(result.body.series_order.length).toBeLessThanOrEqual(value + 1);
    }
    await expectFrameSizes(page, initialFrames);
  }

  const trendFrame = page.getByTestId("responsive-chart-frame").first();
  const legend = trendFrame.getByRole("region", { name: /Chart series|图表系列/ });
  const legendButton = legend.getByRole("button").first();
  await expect(legendButton).toHaveAttribute("aria-pressed", "true");
  const marksBefore = await trendFrame.locator(chartMarkSelector).count();
  await legendButton.click();
  await expect(legendButton).toHaveAttribute("aria-pressed", "false");
  await expect.poll(() => trendFrame.locator(chartMarkSelector).count()).toBeLessThan(marksBefore);
  await expectTooltipWithinViewport(page, trendFrame, true);
  await assertResponsiveLayout(page, true);
  await screenshot(page, testInfo, `${viewport.name}-dashboard-top-20-interactive`);

  const metric = page.getByRole("combobox", { name: /Metric|指标/ }).first();
  const ttftAverage = waitForJson(page, "/api/stats/metric-trend", (url) =>
    url.searchParams.get("metric") === "ttft" && url.searchParams.get("stat") === "avg",
  );
  await chooseSelectIndex(page, metric, 3, 6);
  await ttftAverage;
  await expect(metric).toContainText("TTFT");
  const statistic = page.getByRole("combobox", { name: /Statistic|统计口径/ });
  await expect(statistic).toContainText(/Average|平均/);
  const ttftFrames = await readFrameSizes(page);
  await chooseAndWaitForTrend(page, statistic, 1, "ttft", "p95");
  await expect(statistic).toContainText(/P95 \(estimated\)|P95（估算）/);
  await expectFrameSizes(page, ttftFrames);

  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(page.getByTestId("responsive-chart-frame").first()).toBeVisible();
  const tpsAverage = waitForJson(page, "/api/stats/metric-trend", (url) =>
    url.searchParams.get("metric") === "tps" && url.searchParams.get("stat") === "avg",
  );
  await chooseSelectIndex(page, dimension, 1, 3);
  await chooseSelectIndex(page, metric, 4, 6);
  await tpsAverage;
  await expect(metric).toContainText("TPS");
  await expect(statistic).toContainText(/Average|平均/);
  const tpsFrames = await readFrameSizes(page);
  await chooseAndWaitForTrend(page, statistic, 1, "tps", "p5");
  await expect(statistic).toContainText(/P5 \(estimated\)|P5（估算）/);
  await expectFrameSizes(page, tpsFrames);
  await assertResponsiveLayout(page, true);
});
}

test("billing trend token and Top N remain isolated across real user sessions", async ({ page }, testInfo) => {
  test.setTimeout(120_000);
  await loginFixtureUser(page, "fixture-alice");
  const initialInsights = waitForJson<BillingInsights>(page, "/api/billing/insights");
  const initialOverview = waitForJson<BillingOverview>(page, "/api/billing/overview");
  const initialTokens = waitForJson<BillingList>(page, "/api/billing/tokens");
  await openDashboardPage(page, "/billing?top_n=5");
  const [aliceInsights, aliceOverview, aliceBilling] = await Promise.all([initialInsights, initialOverview, initialTokens]);
  await expect(page.getByTestId("responsive-chart-frame").first()).toBeVisible();
  expect(aliceOverview.body.request_count).toBeGreaterThan(0);
  expect(aliceBilling.body.total).toBe(2);
  expect(aliceBilling.body.data.length).toBe(2);
  const kpisBefore = await page.locator("main").last().locator(".text-display").allTextContents();
  const tokenTable = page.locator("main").last().getByRole("table");
  const tokenTableBefore = normalizedText(await tokenTable.innerText());
  expect(tokenTableBefore).toContain("Alice trend token A");
  expect(tokenTableBefore).toContain("Alice trend token B");
  expect(tokenTableBefore).not.toContain("Bob trend token A");

  const requested: string[] = [];
  page.on("request", (candidate) => {
    if (candidate.url().includes("/api/")) requested.push(candidate.url());
  });
  const tokenPicker = page.getByRole("combobox").filter({ hasText: /Select token|选择令牌/ }).first();
  const filteredPromise = waitForJson<BillingInsights>(page, "/api/billing/insights", (url) =>
    url.searchParams.get("token_id") === "1",
  );
  await chooseEntity(page, tokenPicker, "Alice trend token A");
  const filtered = await filteredPromise;
  await expect(page).toHaveURL(/trend_token_id=1/);
  expect(filtered.body.trend).not.toEqual(aliceInsights.body.trend);
  expect(filtered.body.cost_trend_stacked).not.toEqual(aliceInsights.body.cost_trend_stacked);
  expect(filtered.body.cache_saving).toEqual(aliceInsights.body.cache_saving);
  expect(await page.locator("main").last().locator(".text-display").allTextContents()).toEqual(kpisBefore);
  expect(normalizedText(await tokenTable.innerText())).toBe(tokenTableBefore);
  expect(requested.some((url) => url.includes("/api/billing/insights") && url.includes("token_id=1"))).toBe(true);
  for (const raw of requested.filter((candidate) => !candidate.includes("/api/billing/insights"))) {
    expect(new URL(raw).searchParams.has("token_id")).toBe(false);
  }
  const billingFrames = await readFrameSizes(page);
  const topN = page.getByRole("combobox", { name: /Top N|前 N 项/ });
  expect(filtered.url.searchParams.get("top_n")).toBe("5");
  for (const [index, value] of [[1, 10], [2, 20]] as const) {
    const response = waitForJson<BillingInsights>(page, "/api/billing/insights", (url) =>
      url.searchParams.get("token_id") === "1" && url.searchParams.get("top_n") === String(value),
    );
    await chooseSelectIndex(page, topN, index, 3);
    const result = await response;
    expect(result.body.cost_trend_stacked.series_order.length).toBeGreaterThan(0);
    expect(result.body.cost_trend_stacked.series_order.length).toBeLessThanOrEqual(value + 1);
    await expectFrameSizes(page, billingFrames);
  }
  await expect(topN).toContainText("20");
  await expectFrameSizes(page, billingFrames);
  expect(await readStoredChartTopN(page, 2)).toBe("20");

  await logoutThroughUserMenu(page);
  await loginFixtureUser(page, "fixture-bob");
  const bobInsights = waitForJson<BillingInsights>(page, "/api/billing/insights", (url) =>
    url.searchParams.get("top_n") === "5" && !url.searchParams.has("token_id"),
  );
  await openDashboardPage(page, "/billing");
  await bobInsights;
  await expect(page).not.toHaveURL(/trend_token_id/);
  await expect(page.getByRole("combobox", { name: /Top N|前 N 项/ })).toContainText("5");
  expect(await readStoredChartTopN(page, 3)).toBeNull();
  const bobTokenPicker = page.getByRole("combobox").filter({ hasText: /Select token|选择令牌/ }).first();
  await bobTokenPicker.click();
  await expect(page.getByRole("option", { name: "Bob trend token A", exact: true })).toBeVisible();
  await expect(page.getByRole("option", { name: /Alice trend token/ })).toHaveCount(0);
  await page.keyboard.press("Escape");

  await logoutThroughUserMenu(page);
  await loginFixtureUser(page, "fixture-alice");
  const restoredAliceInsights = waitForJson<BillingInsights>(page, "/api/billing/insights", (url) =>
    url.searchParams.get("top_n") === "20" && !url.searchParams.has("token_id"),
  );
  await openDashboardPage(page, "/billing");
  await restoredAliceInsights;
  await expect(page).not.toHaveURL(/trend_token_id/);
  await expect(page.getByRole("combobox", { name: /Top N|前 N 项/ })).toContainText("20");
  const restoredAliceTokenPicker = page.getByRole("combobox").filter({ hasText: /Select token|选择令牌/ }).first();
  await restoredAliceTokenPicker.click();
  await expect(page.getByRole("option", { name: "Alice trend token A", exact: true })).toBeVisible();
  await expect(page.getByRole("option", { name: "Alice trend token B", exact: true })).toBeVisible();
  await expect(page.getByRole("option", { name: /Bob trend token/ })).toHaveCount(0);
  await page.keyboard.press("Escape");
  await screenshot(page, testInfo, "billing-token-user-isolation");
});

for (const viewport of viewports) {
test(`real loading and empty responses retain the chart layout at ${viewport.name}`, async ({ page, request }, testInfo) => {
  test.setTimeout(90_000);
  await page.setViewportSize(viewport);
  await login(request, page);
  await setPresentation(page, "en", "light");
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Network.enable");
  await cdp.send("Network.emulateNetworkConditions", {
    offline: false,
    latency: 900,
    downloadThroughput: 10 * 1024 * 1024,
    uploadThroughput: 10 * 1024 * 1024,
  });
  const futureStart = Math.floor(Date.now() / 1000) + 365 * 86400;
  const futureEnd = futureStart + 7 * 86400;
  const emptyInsights = waitForJson<BillingInsights>(page, "/api/billing/insights", (url) =>
    url.searchParams.get("start") === String(futureStart) && url.searchParams.get("end") === String(futureEnd),
  );
  await openDashboardPage(page, `/billing?start=${futureStart}&end=${futureEnd}&gran=day`);
  await expect(page.locator('[data-slot="skeleton"]').first()).toBeVisible();
  await assertResponsiveLayout(page, true, false);
  await screenshot(page, testInfo, `${viewport.name}-real-network-loading`);
  await cdp.send("Network.emulateNetworkConditions", {
    offline: false,
    latency: 0,
    downloadThroughput: -1,
    uploadThroughput: -1,
  });
  const result = await emptyInsights;
  expect(result.body.trend).toEqual([]);
  expect(result.body.cost_trend_stacked.buckets).toEqual([]);
  await expect(page.getByText(/No data|暂无数据/).first()).toBeVisible();
  await assertResponsiveLayout(page, true, false);
  await screenshot(page, testInfo, `${viewport.name}-real-empty-state`);
});
}
