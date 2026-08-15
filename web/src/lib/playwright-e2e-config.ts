import { devices, type PlaywrightTestConfig } from "@playwright/test";

import { isRouteSlug } from "./api/route-slug";

const repoRoot = "..";

const webServers = [
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
];

type E2EEnvironment = Record<string, string | undefined>;

export interface PlaywrightE2ERunProfile {
  kind: "isolated" | "external";
  apiOrigin: string;
  webOrigin: string;
  username: string;
  password: string;
  serviceID: number;
  routeID: number;
  routeSlug: string;
}

const externalRequiredFields = [
  "AIGW_ROUTE_WORKSPACE_USERNAME",
  "AIGW_ROUTE_WORKSPACE_PASSWORD",
  "AIGW_ROUTE_WORKSPACE_SERVICE_ID",
  "AIGW_ROUTE_WORKSPACE_ROUTE_ID",
  "AIGW_ROUTE_WORKSPACE_ROUTE_SLUG",
] as const;

function normalizedHTTPOrigin(value: string, field: string) {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    throw new Error(`${field} must be an HTTP(S) origin`);
  }
  if ((url.protocol !== "http:" && url.protocol !== "https:") || url.origin === "null") {
    throw new Error(`${field} must be an HTTP(S) origin`);
  }
  if (url.pathname !== "/" || url.search || url.hash || url.username || url.password) {
    throw new Error(`${field} must be an HTTP(S) origin without path, credentials, query, or fragment`);
  }
  return url.origin;
}

function positiveID(value: string, field: string) {
  const id = Number(value);
  if (!Number.isSafeInteger(id) || id <= 0) throw new Error(`${field} must be a positive integer`);
  return id;
}

function requiredExternalValue(environment: E2EEnvironment, field: typeof externalRequiredFields[number]) {
  const value = environment[field]?.trim();
  if (!value) throw new Error(`External E2E profile requires ${field}`);
  return value;
}

function externalRouteSlug(environment: E2EEnvironment) {
  const slug = requiredExternalValue(environment, "AIGW_ROUTE_WORKSPACE_ROUTE_SLUG");
  if (!isRouteSlug(slug)) throw new Error("AIGW_ROUTE_WORKSPACE_ROUTE_SLUG must be a valid Route slug");
  return slug;
}

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

export function routeWorkspaceExpandButtonName(routeSlug: string) {
  return new RegExp(`^(Expand|展开) ${escapeRegExp(routeSlug)}$`);
}

export function createPlaywrightE2ERunProfile(environment: E2EEnvironment): PlaywrightE2ERunProfile {
  const apiOriginValue = environment.AIGW_E2E_EXTERNAL_API_ORIGIN?.trim();
  const webOriginValue = environment.AIGW_E2E_EXTERNAL_WEB_ORIGIN?.trim();
  if (Boolean(apiOriginValue) !== Boolean(webOriginValue)) {
    throw new Error("A complete external E2E profile requires both API and Web origins");
  }
  if (!apiOriginValue || !webOriginValue) {
    return {
      kind: "isolated",
      apiOrigin: "http://localhost:8140",
      webOrigin: "http://localhost:8141",
      username: "admin",
      password: "chart-e2e-password-strong",
      serviceID: 101,
      routeID: 401,
      routeSlug: "responsive-route-workspace",
    };
  }

  const apiOrigin = normalizedHTTPOrigin(apiOriginValue, "AIGW_E2E_EXTERNAL_API_ORIGIN");
  const webOrigin = normalizedHTTPOrigin(webOriginValue, "AIGW_E2E_EXTERNAL_WEB_ORIGIN");
  if (apiOrigin !== webOrigin) throw new Error("External E2E API and Web URLs must use the same origin");
  const values = Object.fromEntries(externalRequiredFields.map((field) => [field, requiredExternalValue(environment, field)]));
  return {
    kind: "external",
    apiOrigin,
    webOrigin,
    username: values.AIGW_ROUTE_WORKSPACE_USERNAME,
    password: values.AIGW_ROUTE_WORKSPACE_PASSWORD,
    serviceID: positiveID(values.AIGW_ROUTE_WORKSPACE_SERVICE_ID, "AIGW_ROUTE_WORKSPACE_SERVICE_ID"),
    routeID: positiveID(values.AIGW_ROUTE_WORKSPACE_ROUTE_ID, "AIGW_ROUTE_WORKSPACE_ROUTE_ID"),
    routeSlug: externalRouteSlug(environment),
  };
}

export function createPlaywrightE2EConfig(profile: PlaywrightE2ERunProfile): PlaywrightTestConfig {
  const external = profile.kind === "external";
  return {
    testDir: "./e2e",
    testMatch: external ? /route-workspace-responsive\.spec\.ts/ : undefined,
    globalSetup: external ? undefined : "./e2e/global-setup.ts",
    fullyParallel: false,
    workers: 1,
    timeout: 60_000,
    expect: { timeout: 10_000 },
    preserveOutput: external ? "never" : "always",
    use: {
      trace: external ? "off" : "retain-on-failure",
      screenshot: external ? "off" : "only-on-failure",
    },
    projects: [{
      name: "chromium",
      use: { ...devices["Desktop Chrome"], baseURL: profile.webOrigin },
      testIgnore: /log-database-degraded\.spec\.ts/,
    }],
    webServer: external ? undefined : webServers,
  };
}
