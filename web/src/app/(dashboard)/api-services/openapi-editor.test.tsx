import { useState } from "react";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import {
  buildOpenAPIUpdate,
  addOperation,
  addParameter,
  exportedPath,
  groupPathsByRoute,
  parseJSONField,
  parseJSONObjectField,
  removeParameter,
  removeOperation,
  renameOperation,
  renamePath,
  updateParameter,
  upsertOperation,
  type OpenAPIDocumentSnapshot,
} from "./_components/openapi-editor/openapi-editor-state";
import { OpenAPIDocumentEditor } from "./_components/openapi-editor/openapi-document-editor";
import { OpenAPIJSONField } from "./_components/openapi-editor/openapi-json-field";

const openAPIHooks = vi.hoisted(() => ({ get: vi.fn(), update: vi.fn() }));
const router = vi.hoisted(() => ({ push: vi.fn() }));
const pageNavigation = vi.hoisted(() => ({ search: "" }));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string, values?: Record<string, unknown>) => values ? `${key}:${Object.values(values).join(":")}` : key }));
vi.mock("next/navigation", () => ({ useRouter: () => router, useSearchParams: () => new URLSearchParams(pageNavigation.search) }));
vi.mock("@/lib/api/api-services", () => ({ useGetOpenAPIDocument: openAPIHooks.get, useUpdateOpenAPIDocument: openAPIHooks.update }));
vi.mock("./_components/form-entry", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./_components/form-entry")>()),
  APIServiceFormEntryGuard: ({ permission, children }: { permission: unknown; children: React.ReactNode }) => <div data-testid="openapi-entry-guard" data-permission={JSON.stringify(permission)}>{children}</div>,
  InvalidFormEntry: ({ subjectKey }: { subjectKey: string }) => <div data-testid="invalid-openapi-entry">{subjectKey}</div>,
}));

import { ApiError } from "@/lib/api/client";
import OpenAPIEditorPage, { OpenAPIEditorWorkspace } from "./openapi/page";

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false;
  Element.prototype.setPointerCapture ??= () => {};
  Element.prototype.releasePointerCapture ??= () => {};
  Element.prototype.scrollIntoView ??= () => {};
});

beforeEach(() => {
  openAPIHooks.get.mockReset();
  openAPIHooks.update.mockReset();
  router.push.mockReset();
  pageNavigation.search = "";
});

const snapshot: OpenAPIDocumentSnapshot = {
  service: {
    id: 7,
    slug: "weather",
    name: "Weather",
    description: "Forecast",
    updated_at: 11,
    document: { openapi: "3.1.0", info: { version: "1" }, tags: [{ name: "forecast" }], components: { schemas: { Weather: { type: "object" } } } },
  },
  routes: [{
    id: 9,
    slug: "forecast",
    upstream_path: "/forecast",
    updated_at: 12,
    paths: { "/forecast": { summary: "Forecast", parameters: [{ name: "city", in: "query", required: true, schema: { type: "string" } }], operations: { GET: { summary: "Get forecast", responses: { 200: { description: "OK" } } } } } },
  }],
};

describe("OpenAPI editor state", () => {
  it("renames a path immutably while preserving its operations and metadata", () => {
    const renamed = renamePath(snapshot, 9, "/forecast", "/forecast/v2");
    expect(renamed.routes[0]?.paths["/forecast/v2"]).toEqual(snapshot.routes[0]?.paths["/forecast"]);
    expect(snapshot.routes[0]?.paths["/forecast"]).toBeDefined();
  });

  it.each(["forecast", "", "  "])("rejects a path without a leading slash: %j", (path) => {
    expect(() => renamePath(snapshot, 9, "/forecast", path)).toThrow("must start with /");
  });

  it("rejects a duplicate final path and normalizes operation method keys", () => {
    const draft = { ...snapshot, routes: [{ ...snapshot.routes[0]!, paths: { "/forecast": {}, "/other": {} } }] };
    expect(() => renamePath(draft, 9, "/forecast", "/other")).toThrow("openAPIStoredPathDuplicate");
    expect(upsertOperation(snapshot, 9, "/forecast", "post", { summary: "Create" }).routes[0]?.paths["/forecast"]?.operations?.POST).toEqual({ summary: "Create" });
  });

  it("upserts then removes an operation without changing unrelated path data", () => {
    const changed = upsertOperation(snapshot, 9, "/forecast", "PATCH", { parameters: [{ name: "dry", in: "query" }] });
    expect(changed.routes[0]?.paths["/forecast"]?.parameters).toEqual(snapshot.routes[0]?.paths["/forecast"]?.parameters);
    expect(removeOperation(changed, 9, "/forecast", "patch").routes[0]?.paths["/forecast"]?.operations?.PATCH).toBeUndefined();
  });

  it("parses complex JSON only on success and leaves the previous typed value usable on errors", () => {
    expect(parseJSONField('{"type":"object","properties":{"id":{"type":"integer"}}}')).toMatchObject({ ok: true, value: { type: "object" } });
    expect(parseJSONField('{')).toMatchObject({ ok: false });
  });

  it("groups every path beneath its owning runtime route", () => {
    const grouped = groupPathsByRoute(snapshot);
    expect(grouped).toEqual([{ routeID: 9, slug: "forecast", upstreamPath: "/forecast", paths: ["/forecast"] }]);
  });

  it("builds the dedicated update shape with only service and route versioned document facts", () => {
    expect(buildOpenAPIUpdate(snapshot)).toEqual({
      service: { id: 7, updated_at: 11, document: snapshot.service.document },
      routes: [{ id: 9, updated_at: 12, paths: snapshot.routes[0]?.paths }],
    });
  });

  it("handles empty components and routes without inventing values", () => {
    expect(buildOpenAPIUpdate({ ...snapshot, service: { ...snapshot.service, document: {} }, routes: [] })).toEqual({
      service: { id: 7, updated_at: 11, document: {} }, routes: [],
    });
  });

  it("matches the backend exportedPath contract for custom, root, prefixed and invalid stored paths", () => {
    expect(exportedPath({ slug: "accounts", upstream_path: "/users" }, "/users/{id}")).toBe("/accounts/{id}");
    expect(exportedPath({ slug: "", upstream_path: "/users" }, "/users/{id}")).toBe("/users/{id}");
    expect(exportedPath({ slug: "accounts", upstream_path: "" }, "/health")).toBe("/accounts/health");
    expect(exportedPath({ slug: "accounts", upstream_path: "" }, "/accounts/health")).toBe("/accounts/health");
    expect(() => exportedPath({ slug: "accounts", upstream_path: "/users" }, "/orders")).toThrow("openAPIPathOutsideRoute");
  });

  it("rejects a rename that creates the same exported path in a different Route", () => {
    const twoRoutes: OpenAPIDocumentSnapshot = { ...snapshot, routes: [
      { ...snapshot.routes[0]!, slug: "accounts", upstream_path: "/users", paths: { "/users/{id}": {} } },
      { id: 10, slug: "accounts", upstream_path: "/members", updated_at: 13, paths: { "/members/list": {} } },
    ] };
    expect(() => renamePath(twoRoutes, 10, "/members/list", "/members/{id}")).toThrow("openAPIPublicPathDuplicate");
  });

  it("rejects one stored path mapping to different public paths across Routes", () => {
    const twoRoutes: OpenAPIDocumentSnapshot = { ...snapshot, routes: [
      { ...snapshot.routes[0]!, slug: "accounts", upstream_path: "/users", paths: { "/users": {} } },
      { id: 10, slug: "orders", upstream_path: "/users", updated_at: 13, paths: { "/users/list": {} } },
    ] };
    expect(() => renamePath(twoRoutes, 10, "/users/list", "/users")).toThrow("openAPIStoredPathDuplicate");
  });

  it("uses a stable i18n code for duplicate stored paths within one Route", () => {
    const draft = { ...snapshot, routes: [{ ...snapshot.routes[0]!, paths: { "/forecast": {}, "/forecast/other": {} } }] };
    expect(() => renamePath(draft, 9, "/forecast", "/forecast/other")).toThrow("openAPIStoredPathDuplicate");
  });

  it("adds only an unused method and atomically renames or removes operations", () => {
    expect(() => addOperation(snapshot, 9, "/forecast", "GET")).toThrow("openAPIMethodDuplicate");
    const withPost = addOperation(snapshot, 9, "/forecast", "post");
    expect(withPost.routes[0]?.paths["/forecast"]?.operations?.POST).toEqual({ responses: { 200: { description: "OK" } } });
    const renamed = renameOperation(withPost, 9, "/forecast", "POST", "PATCH");
    expect(renamed.routes[0]?.paths["/forecast"]?.operations?.POST).toBeUndefined();
    expect(renamed.routes[0]?.paths["/forecast"]?.operations?.PATCH).toBeDefined();
    expect(() => renameOperation(renamed, 9, "/forecast", "PATCH", "GET")).toThrow("openAPIMethodDuplicate");
    expect(removeOperation(renamed, 9, "/forecast", "PATCH").routes[0]?.paths["/forecast"]?.operations?.PATCH).toBeUndefined();
  });

  it("adds, edits and removes path and operation parameters including the final item", () => {
    const pathAdded = addParameter(snapshot, 9, "/forecast");
    const pathEdited = updateParameter(pathAdded, 9, "/forecast", undefined, 1, { name: "country", in: "query", required: false, schema: { type: "string" } });
    expect(pathEdited.routes[0]?.paths["/forecast"]?.parameters?.[1]).toMatchObject({ name: "country", required: false });
    const pathRemoved = removeParameter(pathEdited, 9, "/forecast", undefined, 1);
    expect(pathRemoved.routes[0]?.paths["/forecast"]?.parameters).toHaveLength(1);
    const operationAdded = addParameter(snapshot, 9, "/forecast", "GET");
    expect(operationAdded.routes[0]?.paths["/forecast"]?.operations?.GET.parameters).toHaveLength(1);
    expect(removeParameter(operationAdded, 9, "/forecast", "GET", 0).routes[0]?.paths["/forecast"]?.operations?.GET.parameters).toEqual([]);
  });

  it("accepts only JSON objects for complex object fields", () => {
    expect(parseJSONObjectField('{"type":"object"}')).toMatchObject({ ok: true });
    for (const raw of ["null", "[]", "1", '"x"']) expect(parseJSONObjectField(raw)).toEqual({ ok: false, error: "openAPIJSONObjectRequired" });
    expect(parseJSONObjectField("{")).toEqual({ ok: false, error: "openAPIJSONInvalid" });
  });
});

describe("OpenAPI document editor", () => {
  it("keeps service facts, tags/components, route selection, path and final public URL in normal page flow", () => {
    render(<OpenAPIDocumentEditor snapshot={snapshot} origin="https://gateway.example.test" disabled={false} onChange={vi.fn()} />);
    expect(screen.getByText("Weather")).toBeVisible();
    expect(screen.getAllByText("forecast").length).toBeGreaterThan(0);
    expect(screen.getByText("Weather")).toBeVisible();
    expect(screen.getByText("https://gateway.example.test/v1/api/weather/forecast")).toBeVisible();
    expect(screen.queryByText("Sheet")).not.toBeInTheDocument();
  });

  it("renders an empty route state and disables editing while save is pending", () => {
    render(<OpenAPIDocumentEditor snapshot={{ ...snapshot, routes: [] }} origin="" disabled onChange={vi.fn()} />);
    expect(screen.getByText("openAPIRoutesEmpty")).toBeVisible();
    expect(screen.getByLabelText("openAPIVersion")).toBeDisabled();
  });

  function EditorHarness({ initial = snapshot }: { initial?: OpenAPIDocumentSnapshot }) {
    const [draft, setDraft] = useState(initial);
    const [valid, setValid] = useState(true);
    return <><OpenAPIDocumentEditor snapshot={draft} origin="https://gateway.example.test" disabled={false} onChange={setDraft} onValidityChange={setValid} /><output data-testid="draft">{JSON.stringify(draft)}</output><output data-testid="valid">{String(valid)}</output></>;
  }

  it("shows backend-equivalent public URLs and switches Route ownership in the normal page", async () => {
    const twoRoutes: OpenAPIDocumentSnapshot = { ...snapshot, routes: [
      { ...snapshot.routes[0]!, slug: "accounts", upstream_path: "/users", paths: { "/users/{id}": snapshot.routes[0]!.paths["/forecast"]! } },
      { id: 10, slug: "", upstream_path: "", updated_at: 13, paths: { "/health": { operations: { GET: {} } } } },
    ] };
    render(<EditorHarness initial={twoRoutes} />);
    expect(screen.getByText("https://gateway.example.test/v1/api/weather/accounts/{id}")).toBeVisible();
    await userEvent.setup().click(screen.getByRole("combobox", { name: "openAPIRouteLabel" }));
    await userEvent.setup().click(screen.getByRole("option", { name: "rootRoute" }));
    expect(screen.getByText("https://gateway.example.test/v1/api/weather/health")).toBeVisible();
    expect(screen.queryByText("https://gateway.example.test/v1/api/weather/accounts/{id}")).not.toBeInTheDocument();
  });

  it("keeps invalid path input local, reports inline errors, and commits a valid path on blur", async () => {
    render(<EditorHarness />);
    const path = screen.getByRole("textbox", { name: "openAPIPathLabel" });
    fireEvent.change(path, { target: { value: "forecast-v2" } });
    expect(screen.getByText("openAPIPathMustStartWithSlash")).toBeVisible();
    expect(screen.getByTestId("draft")).toHaveTextContent('"/forecast"');
    fireEvent.change(path, { target: { value: "/forecast/v2" } });
    fireEvent.blur(path);
    expect(await screen.findByText("https://gateway.example.test/v1/api/weather/forecast/v2")).toBeVisible();
  });

  it("shows a localized inline error when blur would duplicate a stored path", () => {
    const duplicatePaths: OpenAPIDocumentSnapshot = { ...snapshot, routes: [{ ...snapshot.routes[0]!, paths: {
      "/forecast": snapshot.routes[0]!.paths["/forecast"]!,
      "/forecast/other": {},
    } }] };
    render(<EditorHarness initial={duplicatePaths} />);
    const sourceCard = screen.getByTestId("openapi-path-/forecast");
    const input = within(sourceCard).getByRole("textbox", { name: "openAPIPathLabel" });
    fireEvent.change(input, { target: { value: "/forecast/other" } });
    fireEvent.blur(input);
    expect(within(sourceCard).getByText("openAPIStoredPathDuplicate")).toBeVisible();
  });

  it("adds an unused method, renames it atomically, and deletes it without overwriting GET", async () => {
    render(<EditorHarness />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("combobox", { name: "openAPIAddMethodLabel" }));
    expect(screen.queryByRole("option", { name: "GET" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("option", { name: "POST" }));
    await user.click(screen.getByRole("button", { name: "openAPIAddOperation" }));
    expect(screen.getByRole("region", { name: "POST /forecast" })).toBeVisible();
    const post = screen.getByRole("region", { name: "POST /forecast" });
    await user.click(within(post).getByRole("combobox", { name: "openAPIMethodLabel" }));
    await user.click(screen.getByRole("option", { name: "PATCH" }));
    expect(screen.getByRole("region", { name: "PATCH /forecast" })).toBeVisible();
    await user.click(within(screen.getByRole("region", { name: "PATCH /forecast" })).getByRole("button", { name: "openAPIRemoveOperation" }));
    expect(screen.queryByRole("region", { name: "PATCH /forecast" })).not.toBeInTheDocument();
    expect(screen.getByRole("region", { name: "GET /forecast" })).toBeVisible();
  });

  it("edits both parameter levels with Checkbox required, object schema validation and final removal", async () => {
    render(<EditorHarness />);
    const user = userEvent.setup();
    const pathSection = screen.getByTestId("openapi-path-/forecast");
    await user.click(within(pathSection).getByRole("button", { name: "openAPIAddPathParameter" }));
    const pathName = within(pathSection).getByRole("textbox", { name: "openAPIParameterNameAt:2" });
    await user.type(pathName, "country");
    const pathRequired = within(pathSection).getByRole("checkbox", { name: "openAPIParameterRequiredAt:2" });
    await user.click(pathRequired);
    expect(pathRequired).toBeChecked();
    const operation = screen.getByRole("region", { name: "GET /forecast" });
    await user.click(within(operation).getByRole("button", { name: "openAPIAddOperationParameter" }));
    const schema = within(operation).getByRole("textbox", { name: "openAPIParameterSchemaAt:1" });
    fireEvent.change(schema, { target: { value: "[]" } });
    expect(screen.getByTestId("valid")).toHaveTextContent("false");
    expect(within(operation).getByText("openAPIJSONObjectRequired")).toBeVisible();
    await user.click(within(operation).getByRole("button", { name: "openAPIRemoveParameter:1" }));
    expect(within(operation).getByText("openAPIParametersEmpty")).toBeVisible();
  });

  it("keeps invalid complex JSON raw text and the prior typed value, then resets raw/error for a new server snapshot", () => {
    const onChange = vi.fn();
    const onValidityChange = vi.fn();
    const view = render(<OpenAPIJSONField fieldKey="components" snapshotKey="11" id="components" label="components" value={{ schemas: { Old: {} } }} objectOnly disabled={false} onChange={onChange} onValidityChange={onValidityChange} />);
    const textarea = screen.getByRole("textbox", { name: "components" });
    fireEvent.change(textarea, { target: { value: "[" } });
    expect(textarea).toHaveValue("[");
    expect(onChange).not.toHaveBeenCalled();
    expect(onValidityChange).toHaveBeenLastCalledWith("components", false);
    view.rerender(<OpenAPIJSONField fieldKey="components" snapshotKey="12" id="components" label="components" value={{ schemas: { New: {} } }} objectOnly disabled={false} onChange={onChange} onValidityChange={onValidityChange} />);
    expect(screen.getByRole("textbox", { name: "components" })).toHaveValue(JSON.stringify({ schemas: { New: {} } }, null, 2));
    expect(screen.queryByText("openAPIJSONInvalid")).not.toBeInTheDocument();
  });

  it("keeps invalid operation JSON and document validity across temporary Route switches", async () => {
    const twoRoutes: OpenAPIDocumentSnapshot = { ...snapshot, routes: [
      snapshot.routes[0]!,
      { id: 10, slug: "radar", upstream_path: "/radar", updated_at: 13, paths: { "/radar": { operations: { GET: { responses: { 200: { description: "OK" } } } } } } },
    ] };
    render(<EditorHarness initial={twoRoutes} />);
    const responses = screen.getByRole("textbox", { name: "openAPIResponses" });
    fireEvent.change(responses, { target: { value: "{" } });
    expect(screen.getByTestId("valid")).toHaveTextContent("false");

    const user = userEvent.setup();
    await user.click(screen.getByRole("combobox", { name: "openAPIRouteLabel" }));
    await user.click(screen.getByRole("option", { name: "radar" }));
    expect(screen.getByTestId("valid")).toHaveTextContent("false");
    await user.click(screen.getByRole("combobox", { name: "openAPIRouteLabel" }));
    await user.click(screen.getByRole("option", { name: "forecast" }));
    expect(screen.getByRole("textbox", { name: "openAPIResponses" })).toHaveValue("{");
    expect(screen.getByText("openAPIJSONInvalid")).toBeVisible();
  });

  it.each(["path", "operation"] as const)("keeps the second %s parameter identity after deleting an invalid first row", async (scope) => {
    const parameters = [
      { name: "first", in: "query", required: false, schema: { type: "string", title: "First" } },
      { name: "second", in: "header", required: true, schema: { type: "integer", title: "Second" } },
    ];
    const parameterSnapshot: OpenAPIDocumentSnapshot = { ...snapshot, routes: [{ ...snapshot.routes[0]!, paths: { "/forecast": {
      ...snapshot.routes[0]!.paths["/forecast"]!,
      parameters: scope === "path" ? parameters : [],
      operations: { GET: { ...snapshot.routes[0]!.paths["/forecast"]!.operations!.GET, parameters: scope === "operation" ? parameters : [] } },
    } } }] };
    render(<EditorHarness initial={parameterSnapshot} />);
    const container = scope === "path" ? screen.getByTestId("openapi-path-/forecast") : screen.getByRole("region", { name: "GET /forecast" });
    const firstSchema = within(container).getByRole("textbox", { name: "openAPIParameterSchemaAt:1" });
    fireEvent.change(firstSchema, { target: { value: "[]" } });
    expect(screen.getByTestId("valid")).toHaveTextContent("false");

    await userEvent.setup().click(within(container).getByRole("button", { name: "openAPIRemoveParameter:1" }));

    expect(within(container).getByRole("textbox", { name: "openAPIParameterNameAt:1" })).toHaveValue("second");
    const remainingSchema = within(container).getByRole("textbox", { name: "openAPIParameterSchemaAt:1" });
    expect(remainingSchema).toHaveValue(JSON.stringify({ type: "integer", title: "Second" }, null, 2));
    expect(screen.getByTestId("valid")).toHaveTextContent("true");
    fireEvent.change(remainingSchema, { target: { value: JSON.stringify({ type: "number", title: "Second updated" }) } });
    expect(screen.getByTestId("draft")).toHaveTextContent('"name":"second"');
    expect(screen.getByTestId("draft")).toHaveTextContent('"title":"Second updated"');
    expect(screen.getByTestId("draft")).not.toHaveTextContent('"title":"First"');
  });
});

function queryState(data?: OpenAPIDocumentSnapshot, overrides: Record<string, unknown> = {}) {
  return { data, error: null, isLoading: false, refetch: vi.fn().mockResolvedValue({ data }), ...overrides };
}

function updateState(overrides: Record<string, unknown> = {}) {
  return { isPending: false, mutateAsync: vi.fn(), ...overrides };
}

function snapshotFor(serviceID: number, version: string, updatedAt = serviceID) {
  return {
    ...snapshot,
    service: {
      ...snapshot.service,
      id: serviceID,
      name: `Service ${serviceID}`,
      updated_at: updatedAt,
      document: { ...snapshot.service.document, openapi: version },
    },
  } satisfies OpenAPIDocumentSnapshot;
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((onResolve, onReject) => { resolve = onResolve; reject = onReject; });
  return { promise, resolve, reject };
}

describe("OpenAPI editor workspace", () => {
  it("renders loading, retryable error, and empty Route states from the real workspace", async () => {
    const refetch = vi.fn();
    openAPIHooks.update.mockReturnValue(updateState());
    openAPIHooks.get.mockReturnValueOnce(queryState(undefined, { isLoading: true }));
    const view = render(<OpenAPIEditorWorkspace serviceID={7} />);
    expect(screen.queryByRole("textbox", { name: "openAPIVersion" })).not.toBeInTheDocument();

    openAPIHooks.get.mockReturnValueOnce(queryState(undefined, { error: new Error("offline"), refetch }));
    view.rerender(<OpenAPIEditorWorkspace key="error" serviceID={7} />);
    expect(screen.getByText("loadFailed")).toBeVisible();
    await userEvent.setup().click(screen.getByRole("button", { name: "retry" }));
    expect(refetch).toHaveBeenCalledOnce();

    openAPIHooks.get.mockReturnValueOnce(queryState({ ...snapshot, routes: [] }));
    view.rerender(<OpenAPIEditorWorkspace key="empty" serviceID={7} />);
    expect(screen.getByText("openAPIRoutesEmpty")).toBeVisible();
  });

  it("keeps Save disabled and restores invalid JSON raw after switching Routes", async () => {
    const twoRoutes: OpenAPIDocumentSnapshot = { ...snapshot, routes: [
      snapshot.routes[0]!,
      { id: 10, slug: "radar", upstream_path: "/radar", updated_at: 13, paths: { "/radar": { operations: { GET: { responses: {} } } } } },
    ] };
    openAPIHooks.get.mockReturnValue(queryState(twoRoutes));
    openAPIHooks.update.mockReturnValue(updateState());
    render(<OpenAPIEditorWorkspace serviceID={7} />);
    fireEvent.change(screen.getByRole("textbox", { name: "openAPIResponses" }), { target: { value: "{" } });
    expect(screen.getByRole("button", { name: "saveOpenAPIDocument" })).toBeDisabled();

    const user = userEvent.setup();
    await user.click(screen.getByRole("combobox", { name: "openAPIRouteLabel" }));
    await user.click(screen.getByRole("option", { name: "radar" }));
    expect(screen.getByRole("button", { name: "saveOpenAPIDocument" })).toBeDisabled();
    await user.click(screen.getByRole("combobox", { name: "openAPIRouteLabel" }));
    await user.click(screen.getByRole("option", { name: "forecast" }));
    expect(screen.getByRole("textbox", { name: "openAPIResponses" })).toHaveValue("{");
    expect(screen.getByText("openAPIJSONInvalid")).toBeVisible();
  });

  it("saves the edited document and replaces it with the fresh refetched server snapshot", async () => {
    const initial = snapshotFor(7, "3.1.0", 11);
    const saved = snapshotFor(7, "3.1.1", 12);
    const refreshed = snapshotFor(7, "server-3.2.0", 13);
    const refetch = vi.fn().mockResolvedValue({ data: refreshed });
    const mutateAsync = vi.fn().mockResolvedValue(saved);
    openAPIHooks.get.mockReturnValue(queryState(initial, { refetch }));
    openAPIHooks.update.mockReturnValue(updateState({ mutateAsync }));
    render(<OpenAPIEditorWorkspace serviceID={7} />);

    fireEvent.change(screen.getByRole("textbox", { name: "openAPIVersion" }), { target: { value: "3.1.1" } });
    await userEvent.setup().click(screen.getByRole("button", { name: "saveOpenAPIDocument" }));

    await waitFor(() => expect(mutateAsync).toHaveBeenCalledWith(expect.objectContaining({ service: expect.objectContaining({ id: 7, updated_at: 11, document: expect.objectContaining({ openapi: "3.1.1" }) }) })));
    await waitFor(() => expect(screen.getByRole("textbox", { name: "openAPIVersion" })).toHaveValue("server-3.2.0"));
  });

  it("keeps the successful PUT snapshot and shows a non-destructive refresh warning when refetch fails with stale data", async () => {
    const initial = snapshotFor(7, "3.1.0", 11);
    const saved = snapshotFor(7, "saved-3.2.0", 12);
    const savedAgain = snapshotFor(7, "saved-3.3.0", 13);
    const refetch = vi.fn();
    let query = queryState(initial, { refetch });
    refetch.mockImplementation(async () => {
      query = queryState(initial, { error: new Error("refresh offline"), refetch });
      return { data: initial, error: query.error };
    });
    const mutateAsync = vi.fn().mockResolvedValueOnce(saved).mockResolvedValueOnce(savedAgain);
    openAPIHooks.get.mockImplementation(() => query);
    openAPIHooks.update.mockReturnValue(updateState({ mutateAsync }));
    const view = render(<OpenAPIEditorWorkspace serviceID={7} />);

    fireEvent.change(screen.getByRole("textbox", { name: "openAPIVersion" }), { target: { value: "saved-3.2.0" } });
    await userEvent.setup().click(screen.getByRole("button", { name: "saveOpenAPIDocument" }));
    await waitFor(() => expect(refetch).toHaveBeenCalledOnce());
    view.rerender(<OpenAPIEditorWorkspace serviceID={7} />);

    await waitFor(() => expect(screen.getByRole("textbox", { name: "openAPIVersion" })).toHaveValue("saved-3.2.0"));
    expect(screen.getByText("openAPISavedRefreshFailed")).toBeVisible();
    expect(screen.getByText("openAPISavedRefreshFailedDescription")).toBeVisible();
    expect(screen.queryByText("openAPIUpdateFailed")).not.toBeInTheDocument();

    fireEvent.change(screen.getByRole("textbox", { name: "openAPIVersion" }), { target: { value: "saved-3.3.0" } });
    await userEvent.setup().click(screen.getByRole("button", { name: "saveOpenAPIDocument" }));
    await waitFor(() => expect(mutateAsync).toHaveBeenNthCalledWith(2, expect.objectContaining({
      service: expect.objectContaining({ id: 7, updated_at: 12, document: expect.objectContaining({ openapi: "saved-3.3.0" }) }),
    })));
  });

  it("keeps a 409 draft, requires reload confirmation, and resets JSON raw text from the server", async () => {
    const initial = snapshotFor(7, "3.1.0", 11);
    const refreshed = snapshotFor(7, "3.2.0", 12);
    refreshed.service.document.components = { schemas: { Server: {} } };
    const refetch = vi.fn().mockResolvedValue({ data: refreshed });
    const mutateAsync = vi.fn().mockRejectedValue(new ApiError(409, "conflict"));
    openAPIHooks.get.mockReturnValue(queryState(initial, { refetch }));
    openAPIHooks.update.mockReturnValue(updateState({ mutateAsync }));
    render(<OpenAPIEditorWorkspace serviceID={7} />);

    fireEvent.change(screen.getByRole("textbox", { name: "openAPIVersion" }), { target: { value: "my-draft" } });
    const components = screen.getByRole("textbox", { name: "openAPIComponents" });
    fireEvent.change(components, { target: { value: "{" } });
    expect(screen.getByRole("button", { name: "saveOpenAPIDocument" })).toBeDisabled();
    fireEvent.change(components, { target: { value: "{}" } });
    await userEvent.setup().click(screen.getByRole("button", { name: "saveOpenAPIDocument" }));

    expect(await screen.findByText("openAPIUpdateConflict")).toBeVisible();
    expect(screen.getByRole("textbox", { name: "openAPIVersion" })).toHaveValue("my-draft");
    await userEvent.setup().click(screen.getByRole("button", { name: "reloadOpenAPIDocument" }));
    expect(screen.getByRole("button", { name: "confirmReloadOpenAPIDocument" })).toBeVisible();
    await userEvent.setup().click(screen.getByRole("button", { name: "confirmReloadOpenAPIDocument" }));

    await waitFor(() => expect(screen.getByRole("textbox", { name: "openAPIVersion" })).toHaveValue("3.2.0"));
    expect(screen.getByRole("textbox", { name: "openAPIComponents" })).toHaveValue(JSON.stringify({ schemas: { Server: {} } }, null, 2));
  });

  it("keeps the draft and shows an ordinary PUT failure in the page flow", async () => {
    const mutateAsync = vi.fn().mockRejectedValue(new Error("save exploded"));
    openAPIHooks.get.mockReturnValue(queryState(snapshotFor(7, "3.1.0")));
    openAPIHooks.update.mockReturnValue(updateState({ mutateAsync }));
    render(<OpenAPIEditorWorkspace serviceID={7} />);

    fireEvent.change(screen.getByRole("textbox", { name: "openAPIVersion" }), { target: { value: "my-draft" } });
    await userEvent.setup().click(screen.getByRole("button", { name: "saveOpenAPIDocument" }));

    expect(await screen.findByText("save exploded")).toBeVisible();
    expect(screen.getByRole("textbox", { name: "openAPIVersion" })).toHaveValue("my-draft");
  });

  it("resets all local state when the service ID changes", () => {
    const byID = { 7: queryState(snapshotFor(7, "service-7")), 8: queryState(snapshotFor(8, "service-8")) };
    openAPIHooks.get.mockImplementation((id: 7 | 8) => byID[id]);
    openAPIHooks.update.mockReturnValue(updateState());
    const view = render(<OpenAPIEditorWorkspace serviceID={7} />);
    fireEvent.change(screen.getByRole("textbox", { name: "openAPIVersion" }), { target: { value: "dirty-7" } });

    view.rerender(<OpenAPIEditorWorkspace serviceID={8} />);

    expect(screen.getByText("Service 8")).toBeVisible();
    expect(screen.getByRole("textbox", { name: "openAPIVersion" })).toHaveValue("service-8");
  });

  it("ignores a save completion from the previous service after switching IDs", async () => {
    const pending = deferred<OpenAPIDocumentSnapshot>();
    const byID = { 7: queryState(snapshotFor(7, "service-7")), 8: queryState(snapshotFor(8, "service-8")) };
    const updates = { 7: updateState({ mutateAsync: vi.fn().mockReturnValue(pending.promise) }), 8: updateState() };
    openAPIHooks.get.mockImplementation((id: 7 | 8) => byID[id]);
    openAPIHooks.update.mockImplementation((id: 7 | 8) => updates[id]);
    const view = render(<OpenAPIEditorWorkspace serviceID={7} />);
    await userEvent.setup().click(screen.getByRole("button", { name: "saveOpenAPIDocument" }));

    view.rerender(<OpenAPIEditorWorkspace serviceID={8} />);
    await act(async () => pending.resolve(snapshotFor(7, "late-service-7")));

    expect(screen.getByText("Service 8")).toBeVisible();
    expect(screen.getByRole("textbox", { name: "openAPIVersion" })).toHaveValue("service-8");
  });
});

describe("OpenAPI editor page entry", () => {
  it("enters the real guard and workspace for a valid search-param service ID", () => {
    pageNavigation.search = "id=7";
    openAPIHooks.get.mockReturnValue(queryState(snapshotFor(7, "3.1.0")));
    openAPIHooks.update.mockReturnValue(updateState());
    render(<OpenAPIEditorPage />);
    expect(screen.getByTestId("openapi-entry-guard")).toHaveAttribute("data-permission", JSON.stringify({ kind: "manage", serviceId: 7 }));
    expect(screen.getByRole("textbox", { name: "openAPIVersion" })).toHaveValue("3.1.0");
  });

  it.each(["", "id=0", "id=invalid"])("rejects a missing or invalid search-param service ID: %s", (search) => {
    pageNavigation.search = search;
    render(<OpenAPIEditorPage />);
    expect(screen.getByTestId("invalid-openapi-entry")).toHaveTextContent("serviceNotFound");
    expect(screen.queryByTestId("openapi-entry-guard")).not.toBeInTheDocument();
  });
});
