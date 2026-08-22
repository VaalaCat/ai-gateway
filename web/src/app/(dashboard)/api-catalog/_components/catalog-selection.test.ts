import { describe, expect, it } from "vitest";

import type { APICatalogRoute, APICatalogService } from "@/lib/api/api-access";

import {
  normalizeProtocol,
  normalizeRouteID,
  normalizeServiceID,
  parseCatalogSelection,
  pickCatalogID,
} from "./catalog-selection";

const services: APICatalogService[] = [
  { id: 3, slug: "weather", name: "Weather", description: "" },
  { id: 4, slug: "maps", name: "Maps", description: "" },
];

const emptyExample = { method: "", subpath: "", query: "", headers: {}, body: "" };
const httpOnlyRoute: APICatalogRoute = {
  id: 8,
  api_service_id: 3,
  slug: "forecast",
  protocols: ["http"],
  allowed_methods: [],
  websocket_subprotocols: [],
  example_request: emptyExample,
};
const websocketRoute: APICatalogRoute = {
  ...httpOnlyRoute,
  id: 9,
  slug: "radar",
  protocols: ["websocket"],
};

describe("parseCatalogSelection", () => {
  it("parses only positive safe IDs, supported protocols, and an OpenAPI operation without a Token ID", () => {
    expect(parseCatalogSelection(new URLSearchParams("service_id=7&route_id=9&path=%2Fusers%2F%7Bid%7D&method=get&protocol=websocket&token_id=19"))).toEqual({
      serviceID: 7,
      routeID: 9,
      path: "/users/{id}",
      method: "get",
      protocol: "websocket",
    });
  });

  it.each([
    ["service_id=0&route_id=-1&protocol=sse", {}],
    [`service_id=${Number.MAX_SAFE_INTEGER + 1}&route_id=3.5&protocol=HTTP`, {}],
    ["service_id=%20&route_id=1e2&protocol=http", { protocol: "http" }],
  ])("rejects malformed or unsupported values from %s", (search, expected) => {
    expect(parseCatalogSelection(new URLSearchParams(search))).toEqual(expected);
  });

  it("rejects an empty path or non-HTTP operation method", () => {
    expect(parseCatalogSelection(new URLSearchParams("path=&method=BREW"))).toEqual({});
  });
});

describe("catalog selection normalization", () => {
  it("keeps an unknown requested ID pending until pagination is exhausted", () => {
    expect(pickCatalogID(99, services, false)).toEqual({ pending: true });
    expect(pickCatalogID(99, services, true)).toEqual({ id: 3, pending: false });
  });

  it("selects a requested ID as soon as a later page contains it", () => {
    expect(pickCatalogID(4, services, false)).toEqual({ id: 4, pending: false });
  });

  it("keeps IDs present in the current result set", () => {
    expect(normalizeServiceID(4, services)).toBe(4);
    expect(normalizeRouteID(9, [httpOnlyRoute, websocketRoute])).toBe(9);
  });

  it("falls back to the first service and route", () => {
    expect(normalizeServiceID(undefined, services)).toBe(3);
    expect(normalizeRouteID(99, [httpOnlyRoute])).toBe(8);
  });

  it("replaces a Route from another Service with the first current Route", () => {
    const routeFromAnotherService = { ...websocketRoute, id: 11, api_service_id: 4 };

    expect(normalizeRouteID(routeFromAnotherService.id, [httpOnlyRoute])).toBe(8);
  });

  it("returns undefined for empty result sets", () => {
    expect(normalizeServiceID(3, [])).toBeUndefined();
    expect(normalizeRouteID(8, [])).toBeUndefined();
  });

  it("never keeps a protocol unsupported by the selected route", () => {
    expect(normalizeProtocol("websocket", httpOnlyRoute)).toBe("http");
    expect(normalizeProtocol("http", websocketRoute)).toBe("websocket");
    expect(normalizeProtocol(undefined, undefined)).toBeUndefined();
  });
});
