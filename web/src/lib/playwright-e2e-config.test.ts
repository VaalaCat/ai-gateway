import { describe, expect, it } from "vitest";

import { createPlaywrightE2EConfig, createPlaywrightE2ERunProfile, routeWorkspaceExpandButtonName } from "./playwright-e2e-config";

const externalEnv = {
  AIGW_E2E_EXTERNAL_API_ORIGIN: "http://127.0.0.1:18140/",
  AIGW_E2E_EXTERNAL_WEB_ORIGIN: "http://127.0.0.1:18140/",
  AIGW_ROUTE_WORKSPACE_USERNAME: "reviewer",
  AIGW_ROUTE_WORKSPACE_PASSWORD: "external-password",
  AIGW_ROUTE_WORKSPACE_SERVICE_ID: "7",
  AIGW_ROUTE_WORKSPACE_ROUTE_ID: "9",
  AIGW_ROUTE_WORKSPACE_ROUTE_SLUG: "forecast",
};

describe("createPlaywrightE2EConfig", () => {
  it("keeps the isolated fixture runner trace on failure", () => {
    const config = createPlaywrightE2EConfig(createPlaywrightE2ERunProfile({}));

    expect(config.globalSetup).toBe("./e2e/global-setup.ts");
    expect(config.webServer).toHaveLength(2);
    expect(config.use).toMatchObject({
      trace: "retain-on-failure",
      screenshot: "only-on-failure",
    });
    expect(config.preserveOutput).toBe("always");
    expect(config.projects?.[0]?.use).toMatchObject({ baseURL: "http://localhost:8141" });
    expect(config.testMatch).toBeUndefined();
  });

  it("turns traces off when an external origin can receive supplied credentials", () => {
    const profile = createPlaywrightE2ERunProfile(externalEnv);
    const config = createPlaywrightE2EConfig(profile);

    expect(config.globalSetup).toBeUndefined();
    expect(config.webServer).toBeUndefined();
    expect(config.use).toMatchObject({
      trace: "off",
      screenshot: "off",
    });
    expect(config.preserveOutput).toBe("never");
    expect(config.projects?.[0]?.use).toMatchObject({ baseURL: "http://127.0.0.1:18140" });
    expect(config.testMatch).toEqual(/route-workspace-responsive\.spec\.ts/);
    expect(profile).toMatchObject({
      kind: "external",
      apiOrigin: "http://127.0.0.1:18140",
      webOrigin: "http://127.0.0.1:18140",
      serviceID: 7,
      routeID: 9,
      routeSlug: "forecast",
    });
  });

  it.each([
    { partial: { AIGW_E2E_EXTERNAL_API_ORIGIN: externalEnv.AIGW_E2E_EXTERNAL_API_ORIGIN }, label: "API-only" },
    { partial: { AIGW_E2E_EXTERNAL_WEB_ORIGIN: externalEnv.AIGW_E2E_EXTERNAL_WEB_ORIGIN }, label: "Web-only" },
  ])("rejects the $label external profile before a runner can issue requests", ({ partial }) => {
    expect(() => createPlaywrightE2ERunProfile(partial)).toThrow(/complete external E2E profile/);
  });

  it("requires every external identity and fixture field", () => {
    const missingPassword = { ...externalEnv, AIGW_ROUTE_WORKSPACE_PASSWORD: undefined };
    expect(() => createPlaywrightE2ERunProfile(missingPassword)).toThrow(/AIGW_ROUTE_WORKSPACE_PASSWORD/);
  });

  it.each(["forecast.v2", `a${"a".repeat(63)}`])("accepts the production Route slug contract for %s", (routeSlug) => {
    expect(createPlaywrightE2ERunProfile({ ...externalEnv, AIGW_ROUTE_WORKSPACE_ROUTE_SLUG: routeSlug }).routeSlug).toBe(routeSlug);
  });

  it.each(["Forecast", `a${"a".repeat(64)}`, "forecast["])("rejects an invalid external Route slug %s before browser setup", (routeSlug) => {
    expect(() => createPlaywrightE2ERunProfile({ ...externalEnv, AIGW_ROUTE_WORKSPACE_ROUTE_SLUG: routeSlug })).toThrow(/valid Route slug/);
  });

  it("escapes a legal Route slug before interpolating it into the E2E locator", () => {
    const matcher = routeWorkspaceExpandButtonName("forecast.v2");
    expect(matcher.test("Expand forecast.v2")).toBe(true);
    expect(matcher.test("Expand forecastXv2")).toBe(false);
  });

  it.each(["file:///tmp/app", "ws://example.test"])("rejects the non-HTTP origin %s", (origin) => {
    expect(() => createPlaywrightE2ERunProfile({
      ...externalEnv,
      AIGW_E2E_EXTERNAL_API_ORIGIN: origin,
      AIGW_E2E_EXTERNAL_WEB_ORIGIN: origin,
    })).toThrow(/HTTP\(S\)/);
  });

  it("rejects a cross-origin pair before a token can be injected", () => {
    expect(() => createPlaywrightE2ERunProfile({
      ...externalEnv,
      AIGW_E2E_EXTERNAL_WEB_ORIGIN: "https://console.example.test",
      AIGW_E2E_EXTERNAL_API_ORIGIN: "https://api.example.test",
    })).toThrow(/same origin/);
  });
});
