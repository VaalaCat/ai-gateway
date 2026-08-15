import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import type { APIRoute, APIRoutePreview } from "@/lib/api/api-services";
import { APIRouteForm } from "./_components/route-form";
import {
  emptyRouteFormValues,
  headerRowsObject,
  hydrateRouteFormValues,
  requestExampleByteLength,
  routeFieldsForSubmit,
  routeFormReducer,
  validateRouteFormValues,
} from "./_components/route-form/route-form-state";

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false;
  Element.prototype.setPointerCapture ??= () => {};
  Element.prototype.releasePointerCapture ??= () => {};
  Element.prototype.scrollIntoView ??= () => {};
});

const navigation = vi.hoisted(() => ({ push: vi.fn() }));
const mutations = vi.hoisted(() => ({ create: vi.fn(), update: vi.fn() }));
const previewQuery = vi.hoisted(() => ({ data: { endpoints: [], diagnostics: [] } as APIRoutePreview | undefined, isLoading: false, error: null as Error | null, refetch: vi.fn() }));
const previewInput = vi.hoisted(() => ({ current: undefined as import("@/lib/api/api-services").APIRoutePreviewInput | undefined }));
const routeQuery = vi.hoisted(() => ({ current: {} as Record<string, unknown> }));
const backendQuery = vi.hoisted(() => ({ current: {} as Record<string, unknown> }));
const backendLookup = vi.hoisted(() => ({ ids: [] as number[] }));
const notifications = vi.hoisted(() => ({ success: vi.fn() }));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({ useRouter: () => navigation }));
vi.mock("sonner", () => ({ toast: notifications }));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: ({ id, value, onChange }: { id?: string; value: string; onChange: (value: string) => void }) => (
    <select id={id} value={value} onChange={(event) => onChange(event.target.value)}>
      <option value="">none</option>
      <option value="12">{(backendQuery.current.data as { name?: string } | undefined)?.name ?? "primary target"}</option>
      <option value="13">secondary target</option>
    </select>
  ),
}));
vi.mock("@/lib/api/api-services", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/api-services")>()),
  useAPIRoute: () => routeQuery.current,
  useAPIBackend: (id: number) => { backendLookup.ids.push(id); return backendQuery.current; },
  useCreateAPIRoute: () => ({ mutateAsync: mutations.create, isPending: false }),
  useUpdateAPIRoute: () => ({ mutateAsync: mutations.update, isPending: false }),
  useAPIRoutePreview: (draft: import("@/lib/api/api-services").APIRoutePreviewInput | undefined) => { if (draft) previewInput.current = draft; return previewQuery; },
}));

const createMode = () => ({ kind: "create" as const, serviceId: 7, serviceSlug: "weather" });
const editMode = (id = 9) => ({ kind: "edit" as const, id, serviceId: 7, serviceSlug: "weather" });
const exampleRequest = { method: "POST", subpath: "/today", query: "unit=c", headers: { "X-Trace": "one" }, body: "{}" };
const route = (overrides: Partial<APIRoute> = {}): APIRoute => ({
  id: 9,
  api_service_id: 7,
  backend_id: 12,
  slug: "forecast",
  protocols: ["http"],
  allowed_methods: [],
  upstream_path: "forecast",
  forward_subpath: false,
  example_request: exampleRequest,
  websocket_subprotocols: [],
  status: 1,
  ...overrides,
});

async function selectExistingTarget(user: ReturnType<typeof userEvent.setup>) {
  await user.selectOptions(screen.getByLabelText("backend"), "12");
}

async function waitForSaveReady() {
  await waitFor(() => expect(screen.getByRole("button", { name: "save" })).toBeEnabled());
}

describe("APIRouteForm state", () => {
  // behavior change: the control plane intentionally enforces slug === upstream_path before launch.
  it("normalizes one path into both persisted fields", () => {
    expect(routeFieldsForSubmit({ ...emptyRouteFormValues(), path: "/forecast" })).toEqual(expect.objectContaining({ slug: "forecast", upstream_path: "forecast" }));
  });

  it("hydrates edit state from route.slug and ignores a divergent upstream_path", () => {
    const values = hydrateRouteFormValues(route({ slug: "billing", upstream_path: "legacy" }));
    expect(values.path).toBe("billing");
    expect(values).not.toHaveProperty("upstreamPath");
  });

  it.each(["forecast.v2", "forecast_v2", "forecast~v2", "a".repeat(64)])("accepts valid Route path %s", (path) => {
    expect(validateRouteFormValues({ ...emptyRouteFormValues(), path })).not.toContain("invalidRouteSlug");
  });

  it.each(["", "UPPER", "a/b", "a b", "东京", "a?b", "a".repeat(65)])("rejects invalid path %s", (path) => {
    expect(validateRouteFormValues({ ...emptyRouteFormValues(), path })).toContain("invalidRouteSlug");
  });

  it("clears selected methods when all methods is selected", () => {
    const selected = routeFormReducer(emptyRouteFormValues(), { type: "methods", mode: "selected", methods: ["GET"] });
    expect(routeFormReducer(selected, { type: "methods", mode: "all", methods: ["GET"] }).allowedMethods).toEqual([]);
  });

  it("requires the first Endpoint name, URL, and complete credentials for atomic creation", () => {
    const target = { mode: "create" as const, backend: { name: "edge" }, first_upstream: { name: "", base_url: "https://edge.test", priority: 0, weight: 1, status: 1, auth_type: "bearer" as const, credential: {} } };
    const values = { ...emptyRouteFormValues(), path: "forecast", target };
    expect(validateRouteFormValues(values)).toEqual(expect.arrayContaining(["endpointNameRequired", "credentialRequired"]));
  });

  it("keeps override rows lossless while rejecting canonical duplicates", () => {
    const rows = [{ id: "a", name: "X-Trace", value: "first" }, { id: "b", name: "x-trace", value: "second" }];
    const target = { mode: "create" as const, backend: { name: "edge" }, first_upstream: { name: "primary", base_url: "https://edge.test", priority: 0, weight: 1, status: 1, auth_type: "none" as const } };
    expect(headerRowsObject(rows)).toEqual({ "X-Trace": "first", "x-trace": "second" });
    expect(validateRouteFormValues({ ...emptyRouteFormValues(), path: "forecast", target }, rows)).toContain("invalidHeaderOverride");
  });

  it("rejects unsafe enabled request examples without constraining disabled drafts", () => {
    const invalidExample = {
      method: "DELETE",
      subpath: "../private",
      query: "",
      headers: { Authorization: "secret" },
      body: "x".repeat(64 * 1024),
    };
    const values = { ...emptyRouteFormValues(), path: "create-item", target: { mode: "existing" as const, backend_id: 12 }, allowedMethods: ["POST"], exampleEnabled: true, exampleRequest: invalidExample };
    expect(validateRouteFormValues(values)).toEqual(expect.arrayContaining(["invalidExampleMethod", "invalidExamplePath", "invalidExampleHeader", "bodyTooLarge"]));
    expect(validateRouteFormValues({ ...values, exampleEnabled: false })).toEqual([]);
  });

  it("accepts TRACE examples and reports unsafe query input separately", () => {
    const values = {
      ...emptyRouteFormValues(),
      path: "trace-request",
      target: { mode: "existing" as const, backend_id: 12 },
      exampleEnabled: true,
      exampleRequest: { method: "TRACE", subpath: "", query: "debug=#fragment", headers: {}, body: "" },
    };

    expect(validateRouteFormValues(values)).not.toContain("invalidExampleMethod");
    expect(validateRouteFormValues(values)).toContain("invalidExampleQuery");
    expect(validateRouteFormValues(values)).not.toContain("invalidExamplePath");
  });

  it("accepts an example at 64 KiB and rejects the next byte", () => {
    const example = { method: "POST", subpath: "", query: "", headers: {}, body: "" };
    const bodyBytes = 64 * 1024 - requestExampleByteLength(example);
    const values = {
      ...emptyRouteFormValues(),
      path: "bounded-request",
      target: { mode: "existing" as const, backend_id: 12 },
      exampleEnabled: true,
      exampleRequest: { ...example, body: "x".repeat(bodyBytes) },
    };

    expect(requestExampleByteLength(values.exampleRequest)).toBe(64 * 1024);
    expect(validateRouteFormValues(values)).not.toContain("bodyTooLarge");
    expect(validateRouteFormValues({ ...values, exampleRequest: { ...values.exampleRequest, body: `${values.exampleRequest.body}x` } })).toContain("bodyTooLarge");
  });
});

describe("APIRouteForm page", () => {
  beforeEach(() => {
    routeQuery.current = {};
    backendQuery.current = { data: { id: 12, api_service_id: 7, name: "Primary Target" }, isLoading: false, error: null, refetch: vi.fn() };
    backendLookup.ids = [];
    navigation.push.mockReset();
    mutations.create.mockReset();
    mutations.update.mockReset();
    notifications.success.mockReset();
    previewQuery.data = { endpoints: [], diagnostics: [] };
    previewQuery.isLoading = false;
    previewQuery.error = null;
    previewQuery.refetch.mockReset();
    previewInput.current = undefined;
  });

  it("reveals an optional request example and persists its generic HTTP fields with the Route", async () => {
    mutations.create.mockResolvedValue({ id: 9 });
    const user = userEvent.setup();
    render(<APIRouteForm mode={createMode()} />);
    await selectExistingTarget(user);
    await user.type(screen.getByLabelText("path"), "create-item");

    expect(screen.queryByLabelText("exampleMethod")).not.toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "configureRequestExample" }));
    await user.click(screen.getByLabelText("exampleMethod"));
    await user.click(screen.getByRole("option", { name: "POST" }));
    await user.type(screen.getByLabelText("exampleQuery"), "dry_run=true");
    await user.type(screen.getByLabelText("exampleBody"), '{{"name":"example"}');
    await waitForSaveReady();
    await user.click(screen.getByRole("button", { name: "save" }));

    expect(mutations.create).toHaveBeenCalledWith(expect.objectContaining({
      example_request: {
        method: "POST",
        subpath: "",
        query: "dry_run=true",
        headers: {},
        body: '{"name":"example"}',
      },
    }));
  }, 10_000);

  it("shows an inline error for an unsafe example query", async () => {
    const user = userEvent.setup();
    render(<APIRouteForm mode={createMode()} />);
    await selectExistingTarget(user);
    await user.type(screen.getByLabelText("path"), "inspect-request");
    await user.click(screen.getByRole("checkbox", { name: "configureRequestExample" }));
    await user.type(screen.getByLabelText("exampleQuery"), "debug=#fragment");

    expect(screen.getByText("invalidExampleQuery")).toBeVisible();
    expect(screen.getByRole("button", { name: "save" })).toBeDisabled();
  });

  it("persists safe request headers and clears the saved example when configuration is unchecked", async () => {
    routeQuery.current = { data: route(), isLoading: false };
    mutations.update.mockResolvedValue({ status: "ok" });
    const user = userEvent.setup();
    render(<APIRouteForm mode={editMode()} />);

    expect(screen.getByRole("checkbox", { name: "configureRequestExample" })).toBeChecked();
    await user.click(screen.getByRole("button", { name: "addHeader" }));
    await user.clear(screen.getByLabelText("headerName 2"));
    await user.type(screen.getByLabelText("headerName 2"), "X-Request-Mode");
    await user.type(screen.getByLabelText("headerValue 2"), "preview");
    await user.click(screen.getByRole("checkbox", { name: "configureRequestExample" }));
    expect(screen.queryByLabelText("exampleMethod")).not.toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "configureRequestExample" }));
    expect(screen.getByLabelText("headerValue 2")).toHaveValue("preview");
    await user.click(screen.getByRole("checkbox", { name: "configureRequestExample" }));
    await waitForSaveReady();
    await user.click(screen.getByRole("button", { name: "save" }));

    expect(mutations.update).toHaveBeenCalledWith(expect.objectContaining({
      example_request: { method: "", subpath: "", query: "", headers: {}, body: "" },
    }));
  });

  it("shows one editable path and uses Preview final URLs without rebuilding Endpoint paths", async () => {
    previewQuery.data = { endpoints: [
      { upstream_id: 3, upstream_name: "base-query", status: 1, priority: 0, weight: 1, final_url: "https://edge.test/base//forecast?keep=a%2Fb" },
      { upstream_id: 4, upstream_name: "encoded", status: 1, priority: 1, weight: 1, final_url: "https://backup.test/root/encoded%2Fslash/forecast" },
    ], diagnostics: [] };
    const user = userEvent.setup();
    render(<APIRouteForm mode={createMode()} />);
    await selectExistingTarget(user);
    await user.type(screen.getByLabelText("path"), "forecast");

    await waitFor(() => expect(screen.getAllByTestId("segmented-url-text").map((node) => node.textContent)).toEqual([
      "http://localhost:3000/v1/api/weather/forecast",
      "https://edge.test/base//forecast?keep=a%2Fb",
      "https://backup.test/root/encoded%2Fslash/forecast",
    ]));
    expect(screen.getAllByRole("textbox", { name: "path" })).toHaveLength(1);
  });

  it("adds and removes the ellipsis on both sides with forwardSubpath", async () => {
    previewQuery.data = { endpoints: [
      { upstream_id: 3, upstream_name: "primary", status: 1, priority: 0, weight: 1, final_url: "https://edge.test/base/forecast?keep=1" },
      { upstream_id: 4, upstream_name: "backup", status: 1, priority: 1, weight: 1, final_url: "https://backup.test/forecast" },
    ], diagnostics: [] };
    const user = userEvent.setup();
    render(<APIRouteForm mode={createMode()} />);
    await selectExistingTarget(user);
    await user.type(screen.getByLabelText("path"), "forecast");
    const forward = screen.getByRole("checkbox", { name: "forwardSubpath" });
    await user.click(forward);
    await waitFor(() => expect(screen.getAllByTestId("segmented-url-text").map((node) => node.textContent)).toEqual([
      "http://localhost:3000/v1/api/weather/forecast/…",
      "https://edge.test/base/forecast/…?keep=1",
      "https://backup.test/forecast/…",
    ]));
    await user.click(forward);
    await waitFor(() => expect(screen.getAllByTestId("segmented-url-text").map((node) => node.textContent)).toEqual([
      "http://localhost:3000/v1/api/weather/forecast",
      "https://edge.test/base/forecast?keep=1",
      "https://backup.test/forecast",
    ]));
  });

  it("sends the configured sample subpath to an edit Preview", async () => {
    routeQuery.current = { data: route({ forward_subpath: true }), isLoading: false };
    previewQuery.data = { endpoints: [
      { upstream_id: 3, upstream_name: "primary", status: 1, priority: 0, weight: 1, final_url: "https://edge.test/forecast/base/forecast" },
    ], diagnostics: [] };
    render(<APIRouteForm mode={editMode()} />);

    let endpointURL: HTMLElement | undefined;
    await waitFor(() => {
      endpointURL = screen.getAllByTestId("segmented-url-text").find((node) => node.textContent?.startsWith("https://edge.test"));
      expect(endpointURL).toHaveTextContent("https://edge.test/forecast/base/forecast/…");
    });
    expect(endpointURL).toBeDefined();
    if (!endpointURL) throw new Error("missing Endpoint Preview URL");
    expect(previewInput.current?.sample).toEqual(exampleRequest);
    const segmentedParts = [...endpointURL.querySelectorAll("[aria-describedby]")].map((node) => node.textContent);
    expect(segmentedParts).toEqual(["https://edge.test/forecast/base/forecast"]);
  });

  it.each(["https://edge.test/base#fragment", "https://edge.test/%zz"])("shows a local Preview error and no fabricated final URL for unsafe Endpoint input %s", async (baseURL) => {
    const user = userEvent.setup();
    previewQuery.data = undefined;
    previewQuery.error = new Error("fragment or invalid percent rejected");
    render(<APIRouteForm mode={createMode()} />);
    await user.type(screen.getByLabelText("path"), "forecast");
    await user.click(screen.getByRole("radio", { name: "targetCreate" }));
    await user.type(screen.getByLabelText("targetName"), "edge");
    await user.type(screen.getByLabelText("endpointName"), "primary");
    await user.type(screen.getByLabelText("endpointUrl"), baseURL);

    await waitFor(() => expect(screen.getAllByRole("alert").some((alert) => alert.textContent?.includes("routingPreviewFailed") || alert.textContent?.includes("routingPreviewValidationFailed"))).toBe(true));
    expect(screen.queryByText(new RegExp(`${baseURL.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}/forecast`))).not.toBeInTheDocument();
    await user.click(screen.getAllByRole("button", { name: "retryPreview" })[0]!);
    expect(previewQuery.refetch).toHaveBeenCalledOnce();
  });

  it("submits an atomic new Target plus first Endpoint with path equality", async () => {
    mutations.create.mockResolvedValue({ id: 9 });
    const user = userEvent.setup();
    render(<APIRouteForm mode={createMode()} />);
    await user.type(screen.getByLabelText("path"), "forecast");
    await user.click(screen.getByRole("radio", { name: "targetCreate" }));
    await user.type(screen.getByLabelText("targetName"), "edge");
    await user.type(screen.getByLabelText("endpointName"), "primary");
    await user.type(screen.getByLabelText("endpointUrl"), "https://edge.test");
    await waitForSaveReady();
    expect(previewInput.current?.target).toEqual(expect.objectContaining({ mode: "create", backend: { name: "edge" }, first_upstream: expect.objectContaining({ name: "primary", base_url: "https://edge.test" }) }));
    await user.click(screen.getByRole("button", { name: "save" }));

    expect(mutations.create).toHaveBeenCalledWith(expect.objectContaining({
      api_service_id: 7,
      slug: "forecast",
      upstream_path: "forecast",
      example_request: { method: "", subpath: "", query: "", headers: {}, body: "" },
      target: expect.objectContaining({ mode: "create", backend: { name: "edge" }, first_upstream: expect.objectContaining({ name: "primary", base_url: "https://edge.test", priority: 0, weight: 1 }) }),
    }));
    expect(navigation.push).toHaveBeenCalledWith("/api-services/detail?id=7&route_search=forecast&route=9");
  });

  it("uses and preserves the configured request example while editing a Route", async () => {
    routeQuery.current = { data: route({ forward_subpath: true }), isLoading: false };
    backendQuery.current = { data: { id: 12, api_service_id: 7, name: "Primary Target" }, isLoading: false, error: null, refetch: vi.fn() };
    previewQuery.data = { endpoints: [
      { upstream_id: 3, upstream_name: "primary", status: 1, priority: 0, weight: 1, final_url: "https://edge.test/base/forecast" },
    ], diagnostics: [] };
    mutations.update.mockResolvedValue({ status: "ok" });
    const user = userEvent.setup();
    render(<APIRouteForm mode={editMode()} />);
    await waitForSaveReady();
    expect(previewInput.current?.sample).toEqual(exampleRequest);
    expect(screen.getAllByTestId("segmented-url-text").map((node) => node.textContent)).toEqual([
      "http://localhost:3000/v1/api/weather/forecast/…",
      "https://edge.test/base/forecast/…",
    ]);
    await user.click(screen.getByRole("button", { name: "save" }));

    expect(mutations.update).toHaveBeenCalledWith(expect.objectContaining({ id: 9, example_request: exampleRequest, slug: "forecast", upstream_path: "forecast", forward_subpath: true }));
    expect(navigation.push).toHaveBeenCalledWith("/api-services/detail?id=7&route_search=forecast&route=9");
  });

  it("returns a cancelled edit to its searched and expanded Route", async () => {
    routeQuery.current = { data: route(), isLoading: false };
    render(<APIRouteForm mode={editMode()} />);
    await userEvent.setup().click(screen.getByRole("button", { name: "cancel" }));
    expect(navigation.push).toHaveBeenCalledWith("/api-services/detail?id=7&route_search=forecast&route=9");
  });

  it("keeps a failed create in the form without navigation", async () => {
    mutations.create.mockRejectedValue(new Error("create rejected"));
    const user = userEvent.setup();
    render(<APIRouteForm mode={createMode()} />);
    await user.type(screen.getByLabelText("path"), "forecast");
    await selectExistingTarget(user);
    await waitForSaveReady();
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(await screen.findByText("create rejected")).toBeVisible();
    expect(navigation.push).not.toHaveBeenCalled();
  });

  it("fails closed without leaking an id when the existing Target no longer exists", () => {
    routeQuery.current = { data: route(), isLoading: false };
    backendQuery.current = { data: undefined, isLoading: false, error: { status: 404 }, refetch: vi.fn() };

    render(<APIRouteForm mode={editMode()} />);

    expect(screen.getByText("targetNotFound")).toBeInTheDocument();
    expect(screen.queryByText("#12")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "save" })).toBeDisabled();
  });

  it("retries an existing Target lookup error and recovers its trusted name", async () => {
    const refetch = vi.fn();
    routeQuery.current = { data: route(), isLoading: false };
    backendQuery.current = { data: undefined, isLoading: false, error: new Error("target lookup rejected"), refetch };
    const user = userEvent.setup();
    const view = render(<APIRouteForm mode={editMode()} />);

    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("targetLoadFailed");
    await user.click(within(alert).getByRole("button", { name: "retry" }));
    expect(refetch).toHaveBeenCalledOnce();

    backendQuery.current = { data: { id: 12, api_service_id: 7, name: "Recovered Target" }, isLoading: false, error: null, refetch };
    view.rerender(<APIRouteForm mode={editMode()} />);

    expect(screen.getByText("Recovered Target")).toBeInTheDocument();
    await waitForSaveReady();
  });

  it("fails closed when the selected Target belongs to another API Service", () => {
    routeQuery.current = { data: route(), isLoading: false };
    backendQuery.current = { data: { id: 12, api_service_id: 8, name: "Foreign Target" }, isLoading: false, error: null, refetch: vi.fn() };

    render(<APIRouteForm mode={editMode()} />);

    expect(screen.getByText("targetServiceMismatch")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "save" })).toBeDisabled();
  });

  it("fails closed when a same-Service Target lookup returns the wrong identity", () => {
    routeQuery.current = { data: route(), isLoading: false };
    backendQuery.current = { data: { id: 13, api_service_id: 7, name: "Wrong Target" }, isLoading: false, error: null, refetch: vi.fn() };

    render(<APIRouteForm mode={editMode()} />);

    expect(screen.getByText("targetNotFound")).toBeInTheDocument();
    expect(screen.queryByText("#12")).not.toBeInTheDocument();
    expect(previewInput.current).toBeUndefined();
    expect(screen.getByRole("button", { name: "save" })).toBeDisabled();
  });

  it("does not trust a late previous Target result after the selection changes", async () => {
    routeQuery.current = { data: route(), isLoading: false };
    backendQuery.current = { data: { id: 12, api_service_id: 7, name: "Primary Target" }, isLoading: false, error: null, refetch: vi.fn() };
    const user = userEvent.setup();
    const view = render(<APIRouteForm mode={editMode()} />);
    await waitForSaveReady();

    await user.selectOptions(screen.getByLabelText("backend"), "13");
    view.rerender(<APIRouteForm mode={editMode()} />);

    expect(backendLookup.ids.at(-1)).toBe(13);
    expect(screen.getByText("targetNotFound")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "save" })).toBeDisabled();
  });

  it("submits deduplicated WebSocket subprotocols on Enter without requiring blur", async () => {
    mutations.create.mockResolvedValue({ id: 9 });
    const user = userEvent.setup();
    render(<APIRouteForm mode={createMode()} />);
    await selectExistingTarget(user);
    await user.type(screen.getByLabelText("path"), "forecast");
    await user.click(screen.getByRole("checkbox", { name: "websocket" }));
    await user.type(screen.getByLabelText("websocketSubprotocols"), "weather.v1, weather.v1, weather.v2");
    await waitForSaveReady();
    await user.keyboard("{Enter}");

    await waitFor(() => expect(mutations.create).toHaveBeenCalledOnce());
    expect(mutations.create).toHaveBeenCalledWith(expect.objectContaining({ websocket_subprotocols: ["weather.v1", "weather.v2"] }));
  });

  it("places every create Target validation error under its owning field", async () => {
    const user = userEvent.setup();
    render(<APIRouteForm mode={createMode()} />);
    await user.click(screen.getByRole("radio", { name: "targetCreate" }));

    expect(screen.getByLabelText("targetName").closest("[data-slot=field]")).toHaveTextContent("invalidTargetName");
    expect(screen.getByLabelText("endpointName").closest("[data-slot=field]")).toHaveTextContent("endpointNameRequired");
    expect(screen.getByLabelText("endpointUrl").closest("[data-slot=field]")).toHaveTextContent("endpointUrlRequired");
    expect(screen.queryByText("targetRequired")).not.toBeInTheDocument();
  });

  it("opens advanced Target settings and locates an invalid proxy URL at its input", async () => {
    const user = userEvent.setup();
    render(<APIRouteForm mode={createMode()} />);
    await user.click(screen.getByRole("radio", { name: "targetCreate" }));
    await user.click(screen.getByRole("button", { name: "advancedTarget" }));
    await user.type(screen.getByLabelText("proxyUrl"), "https://user:secret@proxy.test/#fragment");
    await user.click(screen.getByRole("button", { name: "advancedTarget" }));

    expect(screen.getByLabelText("proxyUrl")).toBeVisible();
    expect(screen.getByLabelText("proxyUrl").closest("[data-slot=field]")).toHaveTextContent("invalidProxyUrl");
  });

  it("locates credential and Header override errors at their create Target controls", async () => {
    const user = userEvent.setup();
    render(<APIRouteForm mode={createMode()} />);
    await user.click(screen.getByRole("radio", { name: "targetCreate" }));
    await user.click(screen.getByRole("combobox", { name: "authType" }));
    await user.click(await screen.findByRole("option", { name: "bearer" }));
    expect(screen.getByLabelText("bearerToken").closest("[data-slot=field]")).toHaveTextContent("credentialRequired");

    await user.click(screen.getByRole("button", { name: "advancedTarget" }));
    await user.click(screen.getByRole("button", { name: "addHeader" }));
    await user.type(screen.getByLabelText("headerName"), "Connection");
    await user.click(screen.getByRole("button", { name: "advancedTarget" }));
    expect(screen.getByLabelText("headerName")).toBeVisible();
    expect(screen.getByLabelText("headerName").closest("[data-slot=field]")).toHaveTextContent("invalidHeaderOverride");
  });

  it("places Target and request policy validation errors next to their controls", async () => {
    const user = userEvent.setup();
    render(<APIRouteForm mode={createMode()} />);
    expect(screen.getByLabelText("backend").closest("[data-slot=field]")).toHaveTextContent("backendRequired");
    await user.click(screen.getByRole("checkbox", { name: "http" }));
    expect(screen.getByRole("group", { name: "protocols" })).toHaveTextContent("protocolRequired");
    await user.click(screen.getByRole("radio", { name: "selectedMethodsShort" }));
    expect(screen.getByRole("group", { name: "allowedMethods" })).toHaveTextContent("allowedMethodsRequired");
  });

  it("keeps the sticky footer without stage navigation", () => {
    render(<APIRouteForm mode={createMode()} />);
    expect(screen.getByTestId("page-layout-footer")).toContainElement(screen.getByRole("button", { name: "save" }));
    expect(screen.queryByRole("button", { name: "next" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "previous" })).not.toBeInTheDocument();
  });

  it("shows field validation and keeps the path draft after a failed save", async () => {
    mutations.create.mockRejectedValue(new Error("route rejected"));
    const user = userEvent.setup();
    render(<APIRouteForm mode={createMode()} />);
    await selectExistingTarget(user);
    await user.type(screen.getByLabelText("path"), "UPPER");
    expect(screen.getByText("invalidRouteSlug")).toBeInTheDocument();
    await user.clear(screen.getByLabelText("path"));
    await user.type(screen.getByLabelText("path"), "forecast");
    await waitForSaveReady();
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(await screen.findByText("route rejected")).toBeInTheDocument();
    expect(screen.getByLabelText("path")).toHaveValue("forecast");
  });

  it("keeps CONNECT in explanatory copy rather than method controls", () => {
    render(<APIRouteForm mode={createMode()} />);
    expect(screen.getByText("allMethodsDescription")).toBeInTheDocument();
    expect(screen.queryByRole("checkbox", { name: "CONNECT" })).not.toBeInTheDocument();
  });

  it("shows WebSocket subprotocols only when WebSocket is selected", async () => {
    const user = userEvent.setup();
    render(<APIRouteForm mode={createMode()} />);
    expect(screen.queryByLabelText("websocketSubprotocols")).not.toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "websocket" }));
    expect(screen.getByLabelText("websocketSubprotocols")).toBeInTheDocument();
  });

  it("does not carry a route draft across edit identities", async () => {
    routeQuery.current = { data: route({ id: 9, slug: "route-a" }), isLoading: false };
    const user = userEvent.setup();
    const view = render(<APIRouteForm mode={editMode(9)} />);
    await user.clear(screen.getByLabelText("path"));
    await user.type(screen.getByLabelText("path"), "draft-a");
    routeQuery.current = { data: route({ id: 10, slug: "route-b" }), isLoading: false };
    view.rerender(<APIRouteForm mode={editMode(10)} />);
    expect(screen.getByLabelText("path")).toHaveValue("route-b");
  });

  it("fails closed when the loaded route belongs to another service", () => {
    routeQuery.current = { data: route({ api_service_id: 8 }), isLoading: false };
    render(<APIRouteForm mode={editMode()} />);
    expect(screen.getByText("routeNotFound")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "save" })).not.toBeInTheDocument();
  });
});
