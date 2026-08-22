import { describe, expect, it } from "vitest";

import type { APICatalogRoute } from "@/lib/api/api-access";

import {
  createOpenAPIInvocationDraft,
  isOpenAPIMethodAllowed,
  listOpenAPIRouteSlugs,
  normalizeOpenAPIRequestBody,
  normalizeOpenAPIParameters,
  resolveOpenAPIObject,
  unresolvedOpenAPIRouteSlugs,
} from "./_components/openapi-operation-contract";
import type { OpenAPIOperation } from "./_components/openapi-operation-selection";

const route = (id: number, slug: string): APICatalogRoute => ({
  id,
  api_service_id: 7,
  slug,
  protocols: ["http"],
  allowed_methods: [],
  websocket_subprotocols: [],
  example_request: { method: "GET", subpath: "", query: "", headers: {}, body: "" },
});

describe("OpenAPI Route requirements", () => {
  const document = {
    paths: {
      "/first": { "x-ai-gateway-route-slug": "first", get: {} },
      "/second": { "x-ai-gateway-route-slug": "second", post: {} },
      "/duplicate": { "x-ai-gateway-route-slug": "first", get: {} },
      "/unrouted": { get: {} },
    },
  };

  it("extracts every distinct Route slug required by the document", () => {
    expect(listOpenAPIRouteSlugs(document)).toEqual(["first", "second"]);
  });

  it("reports Route slugs not present in the currently loaded identity pages", () => {
    expect(unresolvedOpenAPIRouteSlugs(document, [route(1, "first")])).toEqual(["second"]);
  });

  it("treats an explicitly empty root Route slug as a real identity", () => {
    expect(listOpenAPIRouteSlugs({ paths: { "/health": { "x-ai-gateway-route-slug": "", get: {} } } }))
      .toEqual([""]);
    expect(unresolvedOpenAPIRouteSlugs(
      { paths: { "/health": { "x-ai-gateway-route-slug": "", get: {} } } },
      [route(2, "")],
    )).toEqual([]);
  });
});

describe("resolveOpenAPIObject", () => {
  const componentKinds = [
    "parameters",
    "requestBodies",
    "responses",
    "headers",
    "schemas",
    "examples",
    "pathItems",
  ] as const;
  const components = Object.fromEntries(componentKinds.map((kind) => [
    kind,
    { "tenant/name~id": { kind, value: `${kind}-value` } },
  ]));

  it.each(componentKinds)("resolves one local %s reference with JSON Pointer decoding", (kind) => {
    expect(resolveOpenAPIObject({ $ref: `#/components/${kind}/tenant~1name~0id` }, components)).toEqual({
      value: { kind, value: `${kind}-value` },
      reference: `#/components/${kind}/tenant~1name~0id`,
      resolved: true,
      recursive: false,
    });
  });

  it("keeps external and missing references visible instead of manufacturing an object", () => {
    const external = { $ref: "https://schemas.example/User.json" };
    const missing = { $ref: "#/components/schemas/Missing" };
    expect(resolveOpenAPIObject(external, components)).toEqual({
      value: external, reference: external.$ref, resolved: false, recursive: false,
    });
    expect(resolveOpenAPIObject(missing, components)).toEqual({
      value: missing, reference: missing.$ref, resolved: false, recursive: false,
    });
  });

  it("expands at most one level and marks a direct recursive reference", () => {
    const nested = {
      schemas: {
        First: { $ref: "#/components/schemas/Second" },
        Loop: { $ref: "#/components/schemas/Loop" },
        Second: { type: "string" },
      },
    };
    expect(resolveOpenAPIObject({ $ref: "#/components/schemas/First" }, nested)).toMatchObject({
      value: { $ref: "#/components/schemas/Second" }, resolved: true, recursive: false,
    });
    expect(resolveOpenAPIObject({ $ref: "#/components/schemas/Loop" }, nested)).toMatchObject({
      value: { $ref: "#/components/schemas/Loop" }, resolved: true, recursive: true,
    });
  });
});

describe("normalizeOpenAPIParameters", () => {
  const operation: OpenAPIOperation = {
    routeID: 9,
    routeSlug: "users",
    path: "/users/{id}",
    method: "GET",
    pathItem: {
      parameters: [
        { name: "id", in: "path", required: true, schema: { type: "string" }, example: "u-1" },
        { name: "filter", in: "query", schema: { type: "string" }, example: "path-value" },
        { name: "Authorization", in: "header", schema: { type: "string" } },
      ],
    },
    operation: {
      parameters: [
        { name: "filter", in: "query", required: true, schema: { type: "string" }, example: "operation-value", allowReserved: true },
        { name: "tags", in: "query", schema: { type: "array" }, style: "form", explode: true },
        { name: "matrix", in: "path", schema: { type: "string" }, style: "matrix" },
        { name: "payload", in: "query", content: { "application/json": { schema: { type: "object" } } } },
      ],
    },
  };

  it("merges path and operation parameters by location and name with operation precedence", () => {
    const normalized = normalizeOpenAPIParameters(operation, {});
    expect(normalized.filter((parameter) => parameter.name === "filter")).toHaveLength(1);
    expect(normalized.find((parameter) => parameter.name === "filter")).toMatchObject({
      in: "query",
      required: true,
      value: "operation-value",
      allowReserved: true,
      schema: { type: "string" },
    });
    expect(normalized.some((parameter) => parameter.name === "Authorization")).toBe(false);
  });

  it("preserves serialization metadata and marks unsupported parameter shapes", () => {
    const byName = Object.fromEntries(normalizeOpenAPIParameters(operation, {}).map((parameter) => [parameter.name, parameter]));
    expect(byName.id).toMatchObject({ style: "simple", explode: false, supported: true });
    expect(byName.filter).toMatchObject({ style: "form", explode: true, allowReserved: true, supported: false, unsupportedReason: "serialization" });
    expect(byName.tags).toMatchObject({ schema: { type: "array" }, supported: false, unsupportedReason: "shape" });
    expect(byName.matrix).toMatchObject({ style: "matrix", supported: false, unsupportedReason: "serialization" });
    expect(byName.payload).toMatchObject({ content: { "application/json": expect.any(Object) }, supported: false, unsupportedReason: "content" });
  });

  it("resolves referenced parameters and keeps missing references visible but non-invokable", () => {
    const referenced: OpenAPIOperation = {
      ...operation,
      pathItem: { parameters: [{ $ref: "#/components/parameters/Tenant" }, { $ref: "#/components/parameters/Missing" }] },
      operation: { parameters: [] },
    };
    const normalized = normalizeOpenAPIParameters(referenced, {
      parameters: { Tenant: { name: "tenant", in: "header", required: true, schema: { type: "string" }, example: "north" } },
    });
    expect(normalized).toEqual([
      expect.objectContaining({ name: "tenant", in: "header", value: "north", supported: true, reference: "#/components/parameters/Tenant" }),
      expect.objectContaining({ name: "#/components/parameters/Missing", supported: false, unsupportedReason: "reference" }),
    ]);
  });

  it.each([
    ["missing", "#/components/schemas/Missing"],
    ["external", "https://schemas.example/Scalar.json"],
  ])("fails closed for a %s parameter schema reference whose scalar shape cannot be established", (_kind, reference) => {
    const referencedSchema: OpenAPIOperation = {
      ...operation,
      pathItem: {},
      operation: { parameters: [{ name: "value", in: "query", schema: { $ref: reference } }] },
    };

    expect(normalizeOpenAPIParameters(referencedSchema, {})).toEqual([
      expect.objectContaining({
        name: "value",
        schema: { $ref: reference },
        supported: false,
        unsupportedReason: "reference",
      }),
    ]);
  });

  it("allows a local schema ref only when one-level resolution proves a scalar type", () => {
    const referencedSchema: OpenAPIOperation = {
      ...operation,
      pathItem: {},
      operation: {
        parameters: [
          { name: "scalar", in: "query", schema: { $ref: "#/components/schemas/Scalar" } },
          { name: "unknown", in: "query", schema: { $ref: "#/components/schemas/Unknown" } },
        ],
      },
    };

    const byName = Object.fromEntries(normalizeOpenAPIParameters(referencedSchema, {
      schemas: { Scalar: { type: "string" }, Unknown: { description: "type omitted" } },
    }).map((parameter) => [parameter.name, parameter]));
    expect(byName.scalar).toMatchObject({ supported: true, schema: { type: "string" } });
    expect(byName.unknown).toMatchObject({ supported: false, unsupportedReason: "reference" });
  });
});

describe("normalizeOpenAPIRequestBody", () => {
  const bodyOperation: OpenAPIOperation = {
    routeID: 9,
    routeSlug: "users",
    path: "/users",
    method: "POST",
    pathItem: {},
    operation: {
      requestBody: { $ref: "#/components/requestBodies/CreateUser" },
    },
  };
  const components = {
    requestBodies: {
      CreateUser: {
        required: true,
        content: {
          "text/plain": {
            schema: { default: "schema-default" },
            examples: {
              second: { value: "named-second" },
              first: { $ref: "#/components/examples/First" },
            },
          },
          "application/json": {
            example: { source: "inline" },
            schema: { example: { source: "schema" } },
          },
          "application/xml": {
            schema: { $ref: "#/components/schemas/XMLExample" },
          },
        },
      },
    },
    examples: { First: { value: "named-first" } },
    schemas: { XMLExample: { type: "string", example: "schema-example" } },
  };

  it("sorts media types deterministically and keeps the requestBody required contract", () => {
    const body = normalizeOpenAPIRequestBody(bodyOperation, components);
    expect(body).toMatchObject({
      required: true,
      reference: "#/components/requestBodies/CreateUser",
      supported: true,
    });
    expect(body.mediaTypes.map((media) => media.contentType)).toEqual([
      "application/json",
      "application/xml",
      "text/plain",
    ]);
  });

  it.each([
    ["missing", "#/components/requestBodies/Missing", {}],
    ["external", "https://schemas.example/CreateUser.json", {}],
    ["recursive", "#/components/requestBodies/Loop", {
      requestBodies: { Loop: { $ref: "#/components/requestBodies/Loop" } },
    }],
  ])("fails closed for a %s requestBody reference", (_kind, reference, referenceComponents) => {
    const normalized = normalizeOpenAPIRequestBody({
      ...bodyOperation,
      operation: { requestBody: { $ref: reference } },
    }, referenceComponents);

    expect(normalized).toMatchObject({
      reference,
      supported: false,
      unsupportedReason: "reference",
    });
  });

  it("supports only JSON including +json and text media types", () => {
    const normalized = normalizeOpenAPIRequestBody({
      ...bodyOperation,
      operation: {
        requestBody: {
          content: Object.fromEntries([
            "application/json",
            "application/problem+json",
            "application/json; charset=utf-8",
            "text/plain",
            "text/plain; charset=utf-8",
            "application/x-www-form-urlencoded",
            "multipart/form-data",
            "application/octet-stream",
            "application/xml",
          ].map((contentType) => [contentType, { example: "body" }])),
        },
      },
    }, {});

    expect(Object.fromEntries(normalized.mediaTypes.map((media) => [media.contentType, media.supported]))).toEqual({
      "application/json": true,
      "application/json; charset=utf-8": true,
      "application/octet-stream": false,
      "application/problem+json": true,
      "application/x-www-form-urlencoded": false,
      "application/xml": false,
      "multipart/form-data": false,
      "text/plain": true,
      "text/plain; charset=utf-8": true,
    });
    expect(normalized.mediaTypes.filter((media) => !media.supported))
      .toEqual(expect.arrayContaining([
        expect.objectContaining({ contentType: "multipart/form-data", unsupportedReason: "mediaType" }),
        expect.objectContaining({ contentType: "application/octet-stream", unsupportedReason: "mediaType" }),
      ]));
  });

  it("uses inline, named, schema example, then schema default precedence and unwraps Example Object values", () => {
    const body = normalizeOpenAPIRequestBody(bodyOperation, components);
    expect(body.mediaTypes.map((media) => [media.contentType, media.body])).toEqual([
      ["application/json", JSON.stringify({ source: "inline" }, null, 2)],
      ["application/xml", "schema-example"],
      ["text/plain", "named-first"],
    ]);
    expect(body.mediaTypes.find((media) => media.contentType === "text/plain")?.examples).toEqual([
      { name: "first", value: "named-first" },
      { name: "second", value: "named-second" },
    ]);
  });

  it("creates one draft whose selected content type and normalized parameter values can drive URL, fetch, and curl", () => {
    const operationWithParameters: OpenAPIOperation = {
      ...bodyOperation,
      path: "/users/{id}",
      pathItem: { parameters: [{ name: "id", in: "path", required: true, example: "u-1" }] },
      operation: {
        ...bodyOperation.operation,
        parameters: [{ name: "q", in: "query", example: "active" }],
      },
    };
    expect(createOpenAPIInvocationDraft(operationWithParameters, components)).toEqual({
      path: { id: "u-1" },
      query: { q: "active" },
      headers: {},
      body: JSON.stringify({ source: "inline" }, null, 2),
      contentType: "application/json",
    });
  });
});

describe("isOpenAPIMethodAllowed", () => {
  it("treats an empty allowed_methods policy as all methods", () => {
    expect(isOpenAPIMethodAllowed([], "DELETE")).toBe(true);
  });

  it("matches an explicitly allowed method case-insensitively", () => {
    expect(isOpenAPIMethodAllowed(["get", "POST"], "post")).toBe(true);
  });

  it("rejects a documented method that drifted outside Route policy", () => {
    expect(isOpenAPIMethodAllowed(["GET"], "POST")).toBe(false);
  });
});
