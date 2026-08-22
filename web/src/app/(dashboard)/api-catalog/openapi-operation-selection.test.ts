import { describe, expect, it } from "vitest";

import type { APICatalogRoute } from "@/lib/api/api-access";

import {
  buildOpenAPIInvocationURL,
  findOpenAPIOperation,
  listOpenAPIOperations,
  type OpenAPIInvocationValues,
} from "./_components/openapi-operation-selection";

const routes: APICatalogRoute[] = [
  {
    id: 9,
    api_service_id: 7,
    slug: "users",
    protocols: ["http"],
    allowed_methods: ["GET", "POST"],
    websocket_subprotocols: [],
    example_request: { method: "GET", subpath: "", query: "", headers: {}, body: "" },
  },
  {
    id: 10,
    api_service_id: 7,
    slug: "",
    protocols: ["http"],
    allowed_methods: ["GET"],
    websocket_subprotocols: [],
    example_request: { method: "GET", subpath: "", query: "", headers: {}, body: "" },
  },
];

const document = {
  paths: {
    "/users": {
      "x-ai-gateway-route-slug": "users",
      get: { summary: "List users" },
      post: { summary: "Create user" },
    },
    "/users/{id}": {
      "x-ai-gateway-route-slug": "users",
      get: { summary: "Get user" },
    },
    "/{tenant}/health": {
      "x-ai-gateway-route-slug": "",
      get: { summary: "Health" },
    },
  },
};

describe("OpenAPI operation selection", () => {
  it("uses route, path, and case-insensitive method identity, then falls back to the first visible operation", () => {
    const operations = listOpenAPIOperations(document, routes);

    expect(findOpenAPIOperation(operations, { routeID: 9, path: "/users/{id}", method: "get" }))
      .toMatchObject({ routeID: 9, path: "/users/{id}", method: "GET" });
    expect(findOpenAPIOperation(operations, { routeID: 9, path: "/missing", method: "get" }))
      .toMatchObject({ routeID: 9, path: "/users", method: "GET" });
  });

  it("does not retain an operation identity when its Token scope no longer exposes that Route", () => {
    const scopedOperations = listOpenAPIOperations(document, [routes[1]!]);

    expect(findOpenAPIOperation(scopedOperations, { routeID: 9, path: "/users", method: "GET" }))
      .toMatchObject({ routeID: 10, path: "/{tenant}/health", method: "GET" });
  });

  it("does not fall back to the first operation while Route identities are incomplete", () => {
    const operations = listOpenAPIOperations(document, [routes[1]!]);

    expect(findOpenAPIOperation(
      operations,
      { routeID: 9, path: "/users/{id}", method: "GET" },
      { identitiesComplete: false },
    )).toBeUndefined();
  });

  it("selects operations and parameters from a local ref-only Path Item", () => {
    const operations = listOpenAPIOperations({
      paths: {
        "/users": {
          $ref: "#/components/pathItems/SharedUsers",
          "x-ai-gateway-route-slug": "users",
        },
      },
      components: {
        pathItems: {
          SharedUsers: {
            parameters: [{ name: "tenant", in: "header", schema: { type: "string" } }],
            get: { summary: "List referenced users" },
          },
        },
      },
    }, routes);

    expect(operations).toEqual([
      expect.objectContaining({
        routeID: 9,
        routeSlug: "users",
        path: "/users",
        method: "GET",
        operation: { summary: "List referenced users" },
        pathItem: expect.objectContaining({
          parameters: [{ name: "tenant", in: "header", schema: { type: "string" } }],
        }),
      }),
    ]);
  });

  it.each([
    "#/components/pathItems/Shared%20Users",
    "#%2Fcomponents%2FpathItems%2FShared%20Users",
  ])("percent-decodes local Path Item reference %s before JSON Pointer decoding", (reference) => {
    const operations = listOpenAPIOperations({
      paths: {
        "/users": {
          $ref: reference,
          "x-ai-gateway-route-slug": "users",
        },
      },
      components: {
        pathItems: {
          "Shared Users": { get: { summary: "Percent-decoded users" } },
        },
      },
    }, routes);

    expect(operations).toEqual([
      expect.objectContaining({ path: "/users", method: "GET", operation: { summary: "Percent-decoded users" } }),
    ]);
  });

  const unsafePathItemReferences: Array<[
    string,
    string,
    Record<string, unknown>,
    Record<string, unknown>,
  ]> = [
    ["missing", "#/components/pathItems/Missing", {}, {}],
    ["external", "https://schemas.example/SharedUsers.json", {}, {}],
    ["cyclic", "#/components/pathItems/First", {
      First: { $ref: "#/components/pathItems/Second" },
      Second: { $ref: "#/components/pathItems/First" },
    }, {}],
    ["ref sibling", "#/components/pathItems/SharedUsers", {
      SharedUsers: { get: { summary: "Referenced GET" } },
    }, { post: { summary: "Untrusted sibling POST" } }],
    ["invalid", "#/components/pathItems/SharedUsers/get", {
      SharedUsers: { get: { summary: "Referenced GET" } },
    }, {}],
    ["malformed percent encoding", "#/components/pathItems/Shared%ZZUsers", {
      "Shared Users": { get: { summary: "Referenced GET" } },
    }, {}],
    ["invalid JSON Pointer escape", "#/components/pathItems/Shared~2Users", {
      "Shared~2Users": { get: { summary: "Referenced GET" } },
    }, {}],
  ];

  it.each(unsafePathItemReferences)("fails closed for a %s Path Item reference", (_name, reference, pathItems, sibling) => {
    const operations = listOpenAPIOperations({
      paths: {
        "/users": {
          $ref: reference,
          "x-ai-gateway-route-slug": "users",
          ...sibling,
        },
      },
      components: { pathItems },
    }, routes);

    expect(operations).toEqual([]);
  });
});

describe("buildOpenAPIInvocationURL", () => {
  it("does not add an empty segment for a root Route and encodes path values", () => {
    expect(buildOpenAPIInvocationURL("https://gateway.example", "user-api", "", "/{tenant}/health", { tenant: "a/b" }))
      .toBe("https://gateway.example/v1/api/user-api/a%2Fb/health");
  });

  it("removes the documented grouping prefix before appending a normal Route", () => {
    expect(buildOpenAPIInvocationURL("https://gateway.example", "user-api", "users", "/users/{id}", { id: "a/b" }))
      .toBe("https://gateway.example/v1/api/user-api/users/a%2Fb");
  });

  it("encodes path and query values without treating document security headers as gateway credentials", () => {
    const values: OpenAPIInvocationValues = {
      id: "a b",
      query: { "display name": "a&b", tag: ["first", "second"] },
      headers: { "X-Request-ID": "a/b", Authorization: "upstream-secret" },
    };

    expect(buildOpenAPIInvocationURL("https://gateway.example/", "user-api", "users", "/users/{id}", values))
      .toBe("https://gateway.example/v1/api/user-api/users/a%20b?display+name=a%26b&tag=first&tag=second");
    expect(values.headers?.Authorization).toBe("upstream-secret");
  });

  it("keeps the server-rendered browser-origin placeholder empty until hydration supplies an origin", () => {
    expect(buildOpenAPIInvocationURL("", "user-api", "users", "/users", {})).toBe("");
  });

  it.each([
    ["/reports/%20", "https://gateway.example/v1/api/report-api/reports/%20"],
    ["/reports/%", "https://gateway.example/v1/api/report-api/reports/%25"],
    ["/reports/.", "https://gateway.example/v1/api/report-api/reports/%2E"],
    ["/reports/..", "https://gateway.example/v1/api/report-api/reports/%2E%2E"],
    ["/reports/%2E%2E", "https://gateway.example/v1/api/report-api/reports/%2E%2E"],
    ["/reports//daily/", "https://gateway.example/v1/api/report-api/reports//daily/"],
  ])("keeps documented path %s canonical without URL-parser normalization", (path, expected) => {
    expect(buildOpenAPIInvocationURL("https://gateway.example", "report-api", "reports", path, {}))
      .toBe(expected);
  });

  it("applies the same decode-once segment rules to service, Route, and substituted path values", () => {
    expect(buildOpenAPIInvocationURL(
      "https://gateway.example",
      "report%20api",
      "daily%20reports",
      "/daily%20reports/{name}",
      { name: "%2E%2E" },
    )).toBe("https://gateway.example/v1/api/report%20api/daily%20reports/%2E%2E");
  });
});
