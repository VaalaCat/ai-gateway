import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { createPlaywrightE2ERunProfile, routeWorkspaceExpandButtonName } from "../src/lib/playwright-e2e-config";
import { readRouteWorkspaceLoginToken } from "../src/lib/route-workspace-login";

const profile = createPlaywrightE2ERunProfile(process.env);
const { apiOrigin, webOrigin, serviceID, routeID, routeSlug } = profile;

async function login(page: Page, request: APIRequestContext) {
  const response = await request.post(`${apiOrigin}/api/login`, { data: { username: profile.username, password: profile.password } });
  const token = await readRouteWorkspaceLoginToken(response);
  await page.addInitScript((jwt: string) => localStorage.setItem("token", jwt), token);
  await page.context().addCookies([{ name: "token", value: token, url: webOrigin }]);
}

async function openRouteWorkspace(page: Page, width: number, preview: boolean) {
  await page.setViewportSize({ width, height: width === 390 ? 844 : 1024 });
  await page.goto(`${webOrigin}/api-services/detail?id=${serviceID}`, { waitUntil: "domcontentloaded" });
  await expect(page).not.toHaveURL(/\/login/);
  await page.getByRole("button", { name: routeWorkspaceExpandButtonName(routeSlug) }).click();
  await expect(page.getByTestId(`route-expanded-workspace-${routeID}`)).toBeVisible();
  if (preview) {
    await page.getByRole("button", { name: /^(Preview invocation|预览调用)$/ }).click();
    await expect(page.getByRole("region", { name: /^(Preview invocation|预览调用)$/ })).toBeVisible();
  }
}

async function routeWorkspaceGeometry(page: Page) {
  return page.evaluate((fixtureRouteID) => {
    const tableScroller = document.querySelector('[data-testid="route-data-table-region"] [data-slot="table-container"]');
    const expandedLayer = tableScroller?.querySelector('[data-slot="data-table-expanded-content"]') ?? null;
    const workspace = document.querySelector(`[data-testid="route-expanded-workspace-${fixtureRouteID}"]`);
    const previewRegion = [...document.querySelectorAll("section[aria-label]")]
      .find((element) => /^(Preview invocation|预览调用)$/.test(element.getAttribute("aria-label") ?? "")) ?? null;
    const expandedCell = expandedLayer?.closest("td[colspan]") ?? null;
    const rect = (element: Element | null) => {
      if (!element) return null;
      const box = element.getBoundingClientRect();
      return { left: box.left, right: box.right, top: box.top, bottom: box.bottom };
    };
    const contains = (outer: ReturnType<typeof rect>, inner: ReturnType<typeof rect>) => outer !== null
      && inner !== null
      && inner.left >= outer.left - 1
      && inner.right <= outer.right + 1
      && inner.top >= outer.top - 1
      && inner.bottom <= outer.bottom + 1;
    const scrollerRect = rect(tableScroller);
    return {
      scrollLeft: tableScroller?.scrollLeft ?? 0,
      layerInside: contains(scrollerRect, rect(expandedLayer)),
      workspaceInside: contains(scrollerRect, rect(workspace)),
      previewInside: contains(scrollerRect, rect(previewRegion)),
      documentWidth: [document.documentElement.clientWidth, document.documentElement.scrollWidth],
      bodyWidth: [document.body.clientWidth, document.body.scrollWidth],
      validExpandedCell: expandedCell?.matches("table > tbody > tr > td[colspan]") ?? false,
      expandedCellContainsAll: expandedCell !== null
        && expandedCell.contains(expandedLayer)
        && expandedCell.contains(workspace)
        && expandedCell.contains(previewRegion),
    };
  }, routeID);
}

function expectNoPageOverflow(widths: number[]) {
  expect(widths[1]).toBe(widths[0]);
}

test("Route workspace stays inside its real table scroller at responsive widths", async ({ page, request }) => {
  await login(page, request);

  for (const width of [390, 768]) {
    await test.step(`${width}px keeps expanded workspace and Preview in the scroller viewport`, async () => {
      await openRouteWorkspace(page, width, true);
      const scroller = page.locator('[data-testid="route-data-table-region"] [data-slot="table-container"]');
      await expect(scroller).toHaveCount(1);
      await scroller.evaluate((element) => { element.scrollLeft = element.scrollWidth; });
      await expect.poll(() => scroller.evaluate((element) => element.scrollLeft)).toBeGreaterThan(0);

      const geometry = await routeWorkspaceGeometry(page);
      expect(geometry).toMatchObject({
        layerInside: true,
        workspaceInside: true,
        previewInside: true,
        validExpandedCell: true,
        expandedCellContainsAll: true,
      });
      expect(geometry.scrollLeft).toBeGreaterThan(0);
      expectNoPageOverflow(geometry.documentWidth);
      expectNoPageOverflow(geometry.bodyWidth);
    });
  }

  await test.step("1440px keeps one semantic workspace without unnecessary table scrolling", async () => {
    await openRouteWorkspace(page, 1440, false);
    const region = page.getByTestId("route-data-table-region");
    const scroller = region.locator('[data-slot="table-container"]');
    await expect(page.getByTestId(`route-expanded-workspace-${routeID}`)).toHaveCount(1);
    await expect.poll(() => scroller.evaluate((element) => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(1);
    await expect(region.locator("table > tbody > tr > td[colspan]")).toHaveCount(1);
    await expect(region.locator("table > tbody > tr > td[colspan] [data-slot='data-table-expanded-content']")).toHaveCount(1);
    const pageWidths = await page.evaluate(() => ({
      document: [document.documentElement.clientWidth, document.documentElement.scrollWidth],
      body: [document.body.clientWidth, document.body.scrollWidth],
    }));
    expectNoPageOverflow(pageWidths.document);
    expectNoPageOverflow(pageWidths.body);
  });
});
