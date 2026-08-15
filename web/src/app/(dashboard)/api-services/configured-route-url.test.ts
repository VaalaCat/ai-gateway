import { describe, expect, it } from "vitest";

import { buildConfiguredEndpointURL, buildConfiguredPublicRouteURL } from "./_components/route-table/configured-route-url";

describe("configured route URLs", () => {
  it("builds a full selectable public URL with the subpath ellipsis", () => {
    expect(buildConfiguredPublicRouteURL({
      origin: "https://gateway.test",
      serviceSlug: "weather",
      routeSlug: "forecast",
      forwardSubpath: true,
    })).toEqual(expect.objectContaining({
      text: "https://gateway.test/v1/api/weather/forecast/…",
    }));
  });

  it("omits the ellipsis when subpath forwarding is disabled", () => {
    expect(buildConfiguredPublicRouteURL({
      origin: "https://gateway.test",
      serviceSlug: "weather",
      routeSlug: "radar",
      forwardSubpath: false,
    }).text).toBe("https://gateway.test/v1/api/weather/radar");
  });

  it("falls back to a relative URL when origin is invalid", () => {
    expect(buildConfiguredPublicRouteURL({
      origin: "not an origin",
      serviceSlug: "weather",
      routeSlug: "radar",
      forwardSubpath: false,
    }).text).toBe("/v1/api/weather/radar");
  });

  it("keeps encoded and unencoded path segments canonical", () => {
    expect(buildConfiguredPublicRouteURL({
      origin: "https://gateway.test",
      serviceSlug: "weather%20data",
      routeSlug: "daily report",
      forwardSubpath: false,
    }).text).toBe("https://gateway.test/v1/api/weather%20data/daily%20report");
  });

  it("replaces a concrete preview subpath with one configuration ellipsis", () => {
    expect(buildConfiguredEndpointURL({
      finalURL: "https://api.weather.test/v2/forecast",
      endpointName: "primary",
      routePath: "forecast",
      forwardSubpath: true,
    }).text).toBe("https://api.weather.test/v2/forecast/…");
  });

  it("removes the concrete preview subpath and query before appending a configuration ellipsis", () => {
    const value = buildConfiguredEndpointURL({
      finalURL: "https://api.weather.test/base/v2/forecast/hourly?unit=c",
      endpointName: "primary",
      routePath: "/v2/forecast",
      forwardSubpath: true,
    });

    expect(value.text).toBe("https://api.weather.test/base/v2/forecast/…");
    expect(value.text).not.toContain("hourly");
    expect(value.text).not.toContain("unit=c");
    const route = value.segments.find((segment) => segment.kind === "route");
    expect(value.text.slice(route!.start, route!.end)).toBe("/v2/forecast");
  });

  it("drops a query before building a fixed configured endpoint URL", () => {
    expect(buildConfiguredEndpointURL({
      finalURL: "https://api.weather.test/radar?redirect=/preview/",
      endpointName: "primary",
      routePath: "radar",
      forwardSubpath: false,
    }).text).toBe("https://api.weather.test/radar");
  });

  it("drops a hash before building a configured endpoint URL", () => {
    expect(buildConfiguredEndpointURL({
      finalURL: "https://api.weather.test/radar#preview/",
      endpointName: "primary",
      routePath: "radar",
      forwardSubpath: true,
    }).text).toBe("https://api.weather.test/radar/…");
  });

  it("does not append an ellipsis to a fixed route", () => {
    expect(buildConfiguredEndpointURL({
      finalURL: "https://api.weather.test/radar",
      endpointName: "primary",
      routePath: "radar",
      forwardSubpath: false,
    }).text).toBe("https://api.weather.test/radar");
  });

  it("preserves endpoint-only segmentation when the route suffix is not detectable", () => {
    const value = buildConfiguredEndpointURL({
      finalURL: "https://api.weather.test/rewritten",
      endpointName: "primary",
      routePath: "forecast",
      forwardSubpath: true,
    });

    expect(value.text).toBe("https://api.weather.test/rewritten/…");
    expect(value.segments[0]).toEqual(expect.objectContaining({ kind: "endpoint" }));
  });
});
