import { describe, expect, it } from "vitest";

import type { APIBackend, APIRoute, APIRoutePreview, APIService } from "@/lib/api/api-services";

import { buildEndpointSegmentedURL, buildPublicSegmentedURL, buildRoutePreviewViewModel } from "./_components/route-table/route-preview-url";

const service: APIService = {
  id: 7,
  slug: "weather",
  name: "Weather API",
  description: "Weather data",
  price_per_call: 1,
  status: 1,
};

const backend: APIBackend = {
  id: 17,
  api_service_id: 7,
  name: "Production Target",
  route_count: 1,
  upstream_count: 2,
  enabled_upstream_count: 2,
  endpoint_hosts: ["primary.test", "backup.test"],
};

const route: APIRoute = {
  id: 9,
  api_service_id: 7,
  backend_id: 17,
  slug: "forecast",
  protocols: ["http"],
  allowed_methods: ["GET", "POST"],
  upstream_path: "/v2",
  forward_subpath: true,
  example_request: { method: "GET", subpath: "forecast", query: "", headers: {}, body: "" },
  status: 1,
};

const preview: APIRoutePreview = {
  endpoints: [
    { upstream_id: 3, upstream_name: "primary", status: 1, priority: 0, weight: 1, final_url: "https://primary.test/v2/forecast" },
    { upstream_id: 4, upstream_name: "backup", status: 1, priority: 1, weight: 1, final_url: "https://backup.test/v2/forecast" },
  ],
  diagnostics: [],
};

function previewModel(overrides: Partial<APIRoute> = {}) {
  return buildRoutePreviewViewModel({
    origin: "https://gateway.test",
    serviceSlug: service.slug,
    route: { ...route, ...overrides },
    backend,
    preview,
  });
}

describe("route preview URL", () => {

  it("builds the public URL and every final Endpoint URL from one pure model", () => {
    const model = previewModel();

    expect(model.methods).toEqual(["GET", "POST"]);
    expect(model.publicURL.text).toBe("https://gateway.test/v1/api/weather/forecast/forecast");
    expect(model.targetName).toBe("Production Target");
    expect(model.endpoints.map((endpoint) => endpoint.finalURL.text)).toEqual([
      "https://primary.test/v2/forecast",
      "https://backup.test/v2/forecast",
    ]);
  });

  it("does not turn diagnostics into a fake final URL", () => {
    const model = buildRoutePreviewViewModel({
      origin: "https://gateway.test",
      serviceSlug: service.slug,
      route,
      backend,
      preview: { endpoints: [], diagnostics: ["https://diagnostic.invalid/not-an-endpoint"] },
    });

    expect(model.endpoints).toEqual([]);
    expect(model.publicURL.text).not.toContain("diagnostic.invalid");
    expect(model.diagnostics).toEqual(["https://diagnostic.invalid/not-an-endpoint"]);
  });

  it("uses the all-method boundary and keeps an empty Endpoint list empty", () => {
    const model = buildRoutePreviewViewModel({
      origin: "https://gateway.test",
      serviceSlug: service.slug,
      route: { ...route, allowed_methods: [] },
      backend,
      preview: { endpoints: [], diagnostics: [] },
    });

    expect(model.methods).toBe("all");
    expect(model.endpoints).toEqual([]);
  });

  it("segments the public path instead of a matching Service name in the host", () => {
    const value = buildPublicSegmentedURL("https://weather.test", "weather", route);

    expect(value.segments.map((segment) => value.text.slice(segment.start, segment.end))).toEqual(["weather", "forecast"]);
    expect(value.segments[0]?.start).toBeGreaterThan("https://weather.test".length);
  });

  it.each([
    ["base path", "https://edge.test/base/v2/forecast", "/v2/forecast", "", ["https://edge.test/base", "/v2/forecast"]],
    ["explicit default port", "https://edge.test:443/base/forecast", "/forecast", "", ["https://edge.test:443/base", "/forecast"]],
    ["encoded Route", "https://edge.test/base/caf%C3%A9", "/café", "", ["https://edge.test/base", "/caf%C3%A9"]],
    ["earlier repeated Route text", "https://edge.test/forecast/base/forecast", "/forecast", "", ["https://edge.test/forecast/base", "/forecast"]],
    ["forwarded subpath", "https://edge.test/base/v2/forecast/hourly?unit=c", "/v2/forecast", "/hourly", ["https://edge.test/base", "/v2/forecast"]],
    ["encoded path after earlier duplicate", "https://edge.test/caf%C3%A9/base/caf%C3%A9/today?keep=1", "/café", "/today", ["https://edge.test/caf%C3%A9/base", "/caf%C3%A9"]],
  ] as const)("segments the complete Endpoint base for %s", (_name, text, routePath, subpath, expected) => {
    const value = buildEndpointSegmentedURL(text, "primary", routePath, subpath);

    expect(value.segments.map((segment) => value.text.slice(segment.start, segment.end))).toEqual(expected);
  });

  it("falls back to one truthful Endpoint segment when the Route suffix is absent", () => {
    const value = buildEndpointSegmentedURL("https://edge.test/base/unrelated", "primary", "/forecast");

    expect(value.segments).toEqual([{ start: 0, end: value.text.length, kind: "endpoint", label: "primary" }]);
  });

  it("does not emit an empty Route segment when only a forwarded subpath exists", () => {
    const value = buildEndpointSegmentedURL("https://edge.test/base/hourly", "primary", "", "/hourly");

    expect(value.segments).toEqual([{ start: 0, end: value.text.length, kind: "endpoint", label: "primary" }]);
  });

});
