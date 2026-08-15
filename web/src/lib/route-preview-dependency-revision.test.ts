import { describe, expect, it } from "vitest";

import type { APIBackend, APIRoute, APIUpstream } from "./api/api-services";
import { routePreviewDependencyRevision } from "./route-preview-dependency-revision";

const route: APIRoute = {
  id: 9, api_service_id: 7, backend_id: 17, slug: "forecast", protocols: ["http"], allowed_methods: ["GET"], upstream_path: "/forecast", forward_subpath: true,
  example_request: { method: "GET", subpath: "today", query: "unit=c", headers: { Authorization: "secret-header" }, body: "secret-body" }, status: 1, updated_at: 100,
};
const target: APIBackend = { id: 17, api_service_id: 7, name: "Primary", route_count: 1, upstream_count: 2, enabled_upstream_count: 2, endpoint_hosts: ["a.test", "b.test"], updated_at: 200 };
const endpoints: APIUpstream[] = [
  { id: 31, backend_id: 17, name: "A", base_url: "https://a.test", weight: 1, priority: 0, auth_type: "bearer", status: 1, credential_configured: true, proxy_url_configured: false },
  { id: 32, backend_id: 17, name: "B", base_url: "https://b.test", weight: 1, priority: 1, auth_type: "none", status: 1, credential_configured: false, proxy_url_configured: false },
];

describe("routePreviewDependencyRevision", () => {
  it("is stable for equivalent dependencies and Endpoint order", () => {
    expect(routePreviewDependencyRevision(route, target, endpoints)).toBe(
      routePreviewDependencyRevision({ ...route }, { ...target }, [...endpoints].reverse()),
    );
  });

  it("changes for Route, Target, and Endpoint display dependencies", () => {
    const revision = routePreviewDependencyRevision(route, target, endpoints);
    expect(routePreviewDependencyRevision({ ...route, upstream_path: "/v2" }, target, endpoints)).not.toBe(revision);
    expect(routePreviewDependencyRevision(route, { ...target, updated_at: 201 }, endpoints)).not.toBe(revision);
    expect(routePreviewDependencyRevision(route, target, [{ ...endpoints[0], base_url: "https://new.test" }, endpoints[1]])).not.toBe(revision);
  });

  it("returns an opaque value without request headers, body, or Endpoint credentials", () => {
    const revision = routePreviewDependencyRevision(route, target, endpoints);
    expect(revision).not.toContain("secret-header");
    expect(revision).not.toContain("secret-body");
    expect(revision).not.toContain("https://a.test");
  });
});
