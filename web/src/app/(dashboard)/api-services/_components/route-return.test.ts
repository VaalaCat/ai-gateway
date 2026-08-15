import { describe, expect, it } from "vitest";

import {
  routeReturnContext,
  routeReturnQuery,
  serviceDetailReturnPath,
} from "./route-return";
import { isRouteSlug } from "./route-slug";

describe("Route return context", () => {
  it.each([
    "a",
    "9",
    "forecast-v2",
    "forecast.v2",
    "forecast_v2",
    "forecast~v2",
    `${"a".repeat(63)}-`,
  ])("accepts the real Route slug contract: %s", (slug) => {
    expect(isRouteSlug(slug)).toBe(true);
    const query = routeReturnQuery({ id: 9, slug });
    expect(routeReturnContext(new URLSearchParams(query))).toEqual({ id: 9, slug });
    expect(serviceDetailReturnPath(7, routeReturnContext(new URLSearchParams(query)))).toBe(
      `/api-services/detail?id=7&route_search=${encodeURIComponent(slug)}&route=9`,
    );
  });

  it.each([
    "",
    " Forecast",
    "forecast ",
    "forecast/v2",
    "daily report",
    "Forecast",
    "东京",
    "a".repeat(65),
    "-forecast",
  ])("rejects an invalid Route slug: %j", (slug) => {
    expect(isRouteSlug(slug)).toBe(false);
    expect(routeReturnContext(new URLSearchParams(`route_id=9&route_slug=${encodeURIComponent(slug)}`))).toBeUndefined();
  });

  it.each([
    "route_id=9",
    "route_slug=forecast-v2",
    "route_id=0&route_slug=forecast-v2",
    "route_id=2.5&route_slug=forecast-v2",
    `route_id=${Number.MAX_SAFE_INTEGER + 1}&route_slug=forecast-v2`,
  ])("fails closed for incomplete or invalid identity: %s", (query) => {
    expect(routeReturnContext(new URLSearchParams(query))).toBeUndefined();
  });

  it("fails closed when a malformed parameter reader throws", () => {
    expect(routeReturnContext({ get: () => { throw new URIError("malformed escape"); } })).toBeUndefined();
  });

  it("fails closed for the malformed percent value preserved by URLSearchParams", () => {
    const params = new URLSearchParams("route_id=9&route_slug=%ZZ");
    expect(params.get("route_slug")).toBe("%ZZ");
    expect(routeReturnContext(params)).toBeUndefined();
  });

  it("omits invalid producer context and keeps parameter order stable", () => {
    expect(routeReturnQuery()).toBe("");
    expect(routeReturnQuery({ id: 9, slug: "Forecast" })).toBe("");
    expect(routeReturnQuery({ id: 9, slug: "forecast-v2" })).toBe("route_id=9&route_slug=forecast-v2");
  });

  it.each([
    ["forecast.v2", "route_id=9&route_slug=forecast.v2"],
    ["forecast_v2", "route_id=9&route_slug=forecast_v2"],
    ["forecast~v2", "route_id=9&route_slug=forecast%7Ev2"],
  ])("roundtrips the URLSearchParams encoding for %s", (slug, query) => {
    expect(routeReturnQuery({ id: 9, slug })).toBe(query);
    const route = routeReturnContext(new URLSearchParams(query));
    expect(route).toEqual({ id: 9, slug });
    expect(serviceDetailReturnPath(7, route)).toBe(`/api-services/detail?id=7&route_search=${encodeURIComponent(slug)}&route=9`);
  });
});
