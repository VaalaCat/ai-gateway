import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { OpenAPIOperationDocument } from "./_components/openapi-operation-document";
import type { OpenAPIOperation } from "./_components/openapi-operation-selection";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));

const operation: OpenAPIOperation = {
  routeID: 9,
  routeSlug: "users",
  path: "/users/{id}",
  method: "GET",
  pathItem: {
    parameters: [{ name: "id", in: "path", required: true, schema: { type: "string" }, example: "u-1" }],
  },
  operation: {
    description: "Returns the current user.",
    parameters: [{ name: "include", in: "query", schema: { type: "string" }, example: "teams" }],
    responses: {
      "200": {
        description: "User response",
        content: { "application/json": { schema: { $ref: "#/components/schemas/User" }, example: { id: "u-1" } } },
      },
    },
  },
};

describe("OpenAPIOperationDocument", () => {
  it("keeps the complete URL, parameters, schema, response, and example in the page flow", () => {
    render(<OpenAPIOperationDocument origin="https://gateway.example" serviceSlug="user-api" operation={operation} components={{ schemas: { User: { type: "object", properties: { id: { type: "string" } } } } }} />);

    expect(screen.getByText("https://gateway.example/v1/api/user-api/users/{id}")).toBeInTheDocument();
    expect(screen.getByText("Returns the current user.")).toBeInTheDocument();
    expect(screen.getByText("include")).toBeInTheDocument();
    expect(screen.getByText("#/components/schemas/User")).toBeInTheDocument();
    expect(screen.getByText(/"u-1"/)).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("renders recursive references as readable references without recursively expanding them", () => {
    render(<OpenAPIOperationDocument origin="https://gateway.example" serviceSlug="user-api" operation={operation} components={{ schemas: { User: { $ref: "#/components/schemas/User" } } }} />);

    expect(screen.getAllByText("#/components/schemas/User")).not.toHaveLength(0);
    expect(screen.getByText("openAPIReferenceRecursive")).toBeInTheDocument();
  });

  it("renders an empty description boundary without inventing upstream security credentials", () => {
    render(<OpenAPIOperationDocument origin="https://gateway.example" serviceSlug="user-api" operation={{ ...operation, operation: { security: [{ upstreamKey: [] }], responses: {} } }} components={{}} />);

    expect(screen.getByText("noDescription")).toBeInTheDocument();
    expect(document.body).not.toHaveTextContent("upstreamKey");
  });

  it("merges an operation parameter over the matching path parameter without duplicate rows", () => {
    render(<OpenAPIOperationDocument
      origin="https://gateway.example"
      serviceSlug="user-api"
      operation={{
        ...operation,
        pathItem: { parameters: [{ name: "include", in: "query", schema: { type: "string" }, description: "path version" }] },
        operation: { parameters: [{ name: "include", in: "query", required: true, schema: { type: "boolean" } }], responses: {} },
      }}
      components={{}}
    />);

    expect(screen.getAllByText("include")).toHaveLength(1);
    expect(screen.getByText(/"type": "boolean"/)).toBeInTheDocument();
  });

  it("uses the shared one-level view for parameter, requestBody, response, header, schema, and example refs", () => {
    render(<OpenAPIOperationDocument
      origin="https://gateway.example"
      serviceSlug="user-api"
      operation={{
        ...operation,
        pathItem: { parameters: [{ $ref: "#/components/parameters/Tenant~1ID" }] },
        operation: {
          requestBody: { $ref: "#/components/requestBodies/Create" },
          responses: { "200": { $ref: "#/components/responses/Created" }, "404": { $ref: "#/components/responses/Missing" } },
        },
      }}
      components={{
        parameters: { "Tenant/ID": { name: "tenant", in: "header", schema: { type: "string" } } },
        requestBodies: { Create: { description: "Create payload", content: { "application/json": { examples: { sample: { $ref: "#/components/examples/CreateSample" } } } } } },
        responses: { Created: { description: "Created response", headers: { "X-Trace": { $ref: "#/components/headers/Trace" } }, content: { "application/json": { schema: { $ref: "#/components/schemas/User" } } } } },
        headers: { Trace: { description: "Trace header", schema: { type: "string" } } },
        schemas: { User: { type: "object", title: "Resolved user" } },
        examples: { CreateSample: { value: { name: "Ada" } } },
      }}
    />);

    expect(screen.getByText("tenant")).toBeInTheDocument();
    expect(screen.getByText("Create payload")).toBeInTheDocument();
    expect(screen.getByText("sample")).toBeInTheDocument();
    expect(screen.getByText(/"name": "Ada"/)).toBeInTheDocument();
    expect(screen.getByText("Created response")).toBeInTheDocument();
    expect(screen.getByText("X-Trace")).toBeInTheDocument();
    expect(screen.getByText("Trace header")).toBeInTheDocument();
    expect(screen.getByText(/"title": "Resolved user"/)).toBeInTheDocument();
    expect(screen.getByText("#/components/responses/Missing")).toBeInTheDocument();
  });

  it("keeps external refs visible when they cannot be expanded locally", () => {
    render(<OpenAPIOperationDocument
      origin="https://gateway.example"
      serviceSlug="user-api"
      operation={{ ...operation, pathItem: {}, operation: { requestBody: { $ref: "https://schemas.example/body.json" }, responses: {} } }}
      components={{}}
    />);

    expect(screen.getByText("https://schemas.example/body.json")).toBeInTheDocument();
  });
});
