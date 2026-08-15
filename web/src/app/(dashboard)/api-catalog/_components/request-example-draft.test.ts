import { describe, expect, it } from "vitest";

import type { APICatalogRoute } from "@/lib/api/api-access";
import { buildInvocationCommands } from "../../api-services/_components/invocation-command";

import {
  createRequestExampleDraft,
  requestExampleFromDraft,
} from "./request-example-draft";

const routeWithExample: APICatalogRoute = {
  id: 9,
  api_service_id: 7,
  slug: "forecast",
  protocols: ["http"],
  allowed_methods: ["POST"],
  websocket_subprotocols: ["weather.v1"],
  example_request: {
    method: "POST",
    subpath: "/cities/Paris",
    query: "unit=c&unit=f",
    headers: { "Content-Type": "application/json" },
    body: "{\"days\":3}",
  },
};

describe("createRequestExampleDraft", () => {
  it("hydrates every editable field from the Route example", () => {
    expect(createRequestExampleDraft(routeWithExample)).toEqual({
      method: "POST",
      subpath: "/cities/Paris",
      query: "unit=c&unit=f",
      headers: { "Content-Type": "application/json" },
      body: "{\"days\":3}",
    });
  });

  it("falls back without mutating the catalog DTO", () => {
    const route = {
      ...routeWithExample,
      protocols: ["http"] as const,
      allowed_methods: [],
      websocket_subprotocols: [],
      example_request: undefined,
    } as unknown as APICatalogRoute;
    const before = structuredClone(route);

    const draft = createRequestExampleDraft(route);
    draft.headers["X-Edited"] = "local";

    expect(draft).toEqual({ method: "GET", subpath: "", query: "", headers: { "X-Edited": "local" }, body: "" });
    expect(route).toEqual(before);
  });

  it("uses the first allowed method when the example method is empty", () => {
    const route: APICatalogRoute = {
      ...routeWithExample,
      protocols: ["http"],
      allowed_methods: ["PATCH", "GET"],
      websocket_subprotocols: [],
      example_request: { ...routeWithExample.example_request, method: "" },
    };

    expect(createRequestExampleDraft(route).method).toBe("PATCH");
  });

  it("defaults a WebSocket subprotocol without replacing an example header", () => {
    const withoutHeader: APICatalogRoute = {
      ...routeWithExample,
      protocols: ["websocket"],
      example_request: { ...routeWithExample.example_request, headers: {} },
    };
    const withHeader: APICatalogRoute = {
      ...withoutHeader,
      example_request: {
        ...withoutHeader.example_request,
        headers: { "sec-websocket-protocol": "example.v2" },
      },
    };

    expect(createRequestExampleDraft(withoutHeader).headers).toEqual({
      "Sec-WebSocket-Protocol": "weather.v1",
    });
    expect(createRequestExampleDraft(withHeader).headers).toEqual({
      "sec-websocket-protocol": "example.v2",
    });
  });

  it("does not add a WebSocket subprotocol to an HTTP-only Route", () => {
    const route: APICatalogRoute = {
      ...routeWithExample,
      protocols: ["http"],
      example_request: { ...routeWithExample.example_request, headers: {} },
    };

    expect(createRequestExampleDraft(route).headers).toEqual({});
  });
});

describe("requestExampleFromDraft", () => {
  it("keeps a dual-protocol Route's WebSocket metadata out of HTTP while preserving websocat protocol", () => {
    const route: APICatalogRoute = {
      ...routeWithExample,
      protocols: ["http", "websocket"],
      websocket_subprotocols: ["weather.v1"],
      example_request: { ...routeWithExample.example_request, headers: {} },
    };
    const draft = createRequestExampleDraft(route);
    const commandFor = (protocol: "http" | "websocket") => buildInvocationCommands({
      origin: "https://gateway.example",
      serviceSlug: "weather",
      routeSlug: route.slug,
      protocols: route.protocols,
      example: requestExampleFromDraft(draft, protocol),
      token: "token-value",
    }).find((command) => command.kind === (protocol === "http" ? "curl" : "websocat"))?.command;

    expect(commandFor("http")).not.toContain("Sec-WebSocket-Protocol");
    expect(commandFor("websocket")).toContain("--protocol 'weather.v1'");
  });

  it("drops body and Authorization for WebSocket command input", () => {
    expect(requestExampleFromDraft({
      method: "POST",
      subpath: "",
      query: "",
      headers: { authorization: "internal", "X-Test": "1" },
      body: "payload",
    }, "websocket")).toEqual({
      method: "POST",
      subpath: "",
      query: "",
      headers: { "X-Test": "1" },
      body: "",
    });
  });

  it("keeps an HTTP body and custom headers while removing every Authorization casing", () => {
    const draft = {
      method: "POST",
      subpath: "/events",
      query: "kind=a&kind=b",
      headers: { Authorization: "one", AUTHORIZATION: "two", "X-Test": "1" },
      body: "payload",
    };
    const before = structuredClone(draft);

    expect(requestExampleFromDraft(draft, "http")).toEqual({
      method: "POST",
      subpath: "/events",
      query: "kind=a&kind=b",
      headers: { "X-Test": "1" },
      body: "payload",
    });
    expect(draft).toEqual(before);
  });

  it("returns a fresh empty header record", () => {
    const draft = { method: "GET", subpath: "", query: "", headers: {}, body: "" };
    const example = requestExampleFromDraft(draft, "http");

    expect(example.headers).toEqual({});
    expect(example.headers).not.toBe(draft.headers);
  });
});
