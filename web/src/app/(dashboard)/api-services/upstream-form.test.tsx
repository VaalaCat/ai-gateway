import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { APIUpstreamForm } from "./_components/upstream-form";

beforeAll(() => { Element.prototype.hasPointerCapture ??= () => false; Element.prototype.setPointerCapture ??= () => {}; Element.prototype.releasePointerCapture ??= () => {}; Element.prototype.scrollIntoView ??= () => {}; });
const navigation = vi.hoisted(() => ({ push: vi.fn() }));
const mutations = vi.hoisted(() => ({ create: vi.fn(), update: vi.fn() }));
const state = vi.hoisted(() => ({ query: {} as Record<string, unknown>, backend: {} as Record<string, unknown>, routes: {} as Record<string, unknown> }));
type PreviewState = { data?: { endpoints: Array<Record<string, unknown>>; diagnostics: string[] }; isLoading: boolean; error: unknown; refetch: ReturnType<typeof vi.fn> };
const preview = vi.hoisted(() => ({
  drafts: [] as Array<Record<string, unknown> | undefined>,
  requests: [] as Array<Record<string, unknown>>,
  state: { data: { endpoints: [], diagnostics: [] }, isLoading: false, error: null, refetch: vi.fn() } as PreviewState,
  revisions: new Map<string, Record<string, unknown>>(),
}));
const notifications = vi.hoisted(() => ({ success: vi.fn() }));
const targetRoutes = [
  { id: 8, api_service_id: 7, backend_id: 12, slug: "forecast", protocols: ["http"], allowed_methods: ["GET"], upstream_path: "legacy-path", forward_subpath: false, example_request: { method: "", subpath: "", query: "", headers: {}, body: "" }, status: 1 },
  { id: 9, api_service_id: 7, backend_id: 12, slug: "radar", protocols: ["http"], allowed_methods: ["GET"], upstream_path: "legacy-radar", forward_subpath: false, example_request: { method: "", subpath: "", query: "", headers: {}, body: "" }, status: 1 },
];
vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({ useRouter: () => navigation }));
vi.mock("sonner", () => ({ toast: notifications }));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: ({ id, value, onChange, disabled }: { id?: string; value: string; onChange: (value: string) => void; disabled?: boolean }) => (
    <select id={id} value={value} disabled={disabled} onChange={(event) => onChange(event.target.value)}>
      <option value="">none</option>
      <option value="12">primary backend</option>
      <option value="13">secondary backend</option>
    </select>
  ),
}));
vi.mock("@/lib/api/api-services", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/api-services")>()),
  useAPIUpstream: () => state.query,
  useAPIBackend: () => state.backend,
  useAllAPIRoutes: () => state.routes,
  useAPIRoutePreview: (draft: Record<string, unknown> | undefined) => {
    preview.drafts.push(draft);
    if (!draft) return preview.state;
    const key = String(draft.slug);
    if (preview.revisions.get(key) !== draft) {
      preview.revisions.set(key, draft);
      preview.requests.push(draft);
    }
    if (preview.state.isLoading || preview.state.error || preview.state.data?.endpoints.length === 0) return preview.state;
    const target = draft.target as { first_upstream: { base_url: string; name: string } };
    return { ...preview.state, data: { endpoints: [{ upstream_id: 0, upstream_name: target.first_upstream.name, status: 1, priority: 0, weight: 1, final_url: `${target.first_upstream.base_url}/server-preview/${draft.slug}` }], diagnostics: [] } };
  },
  useCreateAPIUpstream: () => ({ mutateAsync: mutations.create, isPending: false }),
  useUpdateAPIUpstream: () => ({ mutateAsync: mutations.update, isPending: false }),
}));

describe("APIUpstreamForm", () => {
  beforeEach(() => { state.query = {}; state.backend = { data: { id: 12, api_service_id: 7, name: "Primary target" }, isLoading: false }; state.routes = { data: [], isLoading: false, error: null }; preview.drafts = []; preview.requests = []; preview.revisions = new Map(); preview.state = { data: { endpoints: [{ upstream_id: 0 }], diagnostics: [] }, isLoading: false, error: null, refetch: vi.fn() }; navigation.push.mockReset(); mutations.create.mockReset(); mutations.update.mockReset(); notifications.success.mockReset(); });
  afterEach(() => { vi.useRealTimers(); });

  it("creates with weight one and accepts the 2048-character Base URL boundary", async () => {
    mutations.create.mockResolvedValue({ id: 3 });
    const user = userEvent.setup();
    const baseURL = `https://example.test/${"a".repeat(2027)}`;
    expect(baseURL).toHaveLength(2048);
    render(<APIUpstreamForm mode={{ kind: "create", serviceId: 7 }} />);
    const save = screen.getByRole("button", { name: "save" });
    expect(save).toHaveAttribute("form", "api-upstream-form");
    expect(save.closest("[data-slot=page-layout-footer]")).toBeInTheDocument();
    await user.type(screen.getByLabelText("name"), "primary");
    await user.selectOptions(screen.getByLabelText("backend"), "12");
    fireEvent.change(screen.getByLabelText("baseUrl"), { target: { value: baseURL } });
    await user.click(save);
    expect(mutations.create).toHaveBeenCalledWith(expect.objectContaining({ backend_id: 12, base_url: baseURL, weight: 1, auth_type: "none" }));
    expect(mutations.create.mock.calls[0]?.[0]).not.toHaveProperty("api_service_id");
    expect(notifications.success).toHaveBeenCalledWith("success");
    expect(navigation.push).toHaveBeenCalledWith("/api-services/detail?id=7");
  });

  it("rejects create when no Backend is selected", async () => {
    const user = userEvent.setup();
    render(<APIUpstreamForm mode={{ kind: "create", serviceId: 7 }} />);
    await user.type(screen.getByLabelText("name"), "primary");
    await user.type(screen.getByLabelText("baseUrl"), "https://example.test");
    await user.click(screen.getByRole("button", { name: "save" }));

    expect(mutations.create).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent("backendRequired");
  });

  it("shows a Target from the create URL as read-only context instead of a picker", async () => {
    mutations.create.mockResolvedValue({ id: 3 });
    const user = userEvent.setup();
    render(<APIUpstreamForm mode={{ kind: "create", serviceId: 7, backendId: 12, returnRoute: { id: 9, slug: "daily-report-9" } }} />);

    expect(screen.queryByRole("combobox", { name: "backend" })).not.toBeInTheDocument();
    expect(screen.getByText("Primary target")).toBeInTheDocument();
    expect(screen.getByText("backendLocked")).toBeInTheDocument();
    await user.type(screen.getByLabelText("name"), "primary");
    await user.type(screen.getByLabelText("baseUrl"), "https://example.test");
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.create).toHaveBeenCalledWith(expect.objectContaining({ backend_id: 12 }));
    expect(navigation.push).toHaveBeenCalledWith("/api-services/detail?id=7&route_search=daily-report-9&route=9");
  });

  it("shows each Target Route using the server Preview final URL without another Target input", async () => {
    vi.useFakeTimers();
    state.routes = { data: targetRoutes, isLoading: false, error: null };
    render(<APIUpstreamForm mode={{ kind: "create", serviceId: 7, backendId: 12 }} />);

    fireEvent.change(screen.getByLabelText("name"), { target: { value: "primary" } });
    fireEvent.change(screen.getByLabelText("baseUrl"), { target: { value: "https://edge.test/a-very-long-base" } });
    expect(preview.requests).toHaveLength(0);
    expect(screen.getAllByText("endpointPreviewPreparing")).toHaveLength(2);
    expect(screen.queryByText("endpointPreviewRequired")).not.toBeInTheDocument();
    act(() => { vi.advanceTimersByTime(300); });

    expect(screen.getAllByText("/forecast").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("/radar").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByTestId("segmented-url-text").map((element) => element.textContent)).toEqual(expect.arrayContaining([
      "https://edge.test/a-very-long-base/server-preview/forecast",
      "https://edge.test/a-very-long-base/server-preview/radar",
    ]));
    expect(screen.queryByRole("combobox", { name: "backend" })).not.toBeInTheDocument();
    expect(preview.drafts.some((draft) => draft?.upstream_path === "forecast")).toBe(true);
    expect(preview.drafts.some((draft) => draft?.upstream_path === "radar")).toBe(true);
    expect(preview.requests).toHaveLength(2);
    expect(preview.requests.every((draft) => (draft.target as { first_upstream: { name: string } }).first_upstream.name === "endpoint-route-preview")).toBe(true);

    fireEvent.change(screen.getByLabelText("name"), { target: { value: "renamed endpoint" } });
    act(() => { vi.advanceTimersByTime(300); });
    expect(preview.requests).toHaveLength(2);
  });

  it.each(["edit", "copy"] as const)("publishes the initial prefilled %s Preview immediately", (kind) => {
    vi.useFakeTimers();
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: "https://prefilled.test", weight: 1, priority: 0, auth_type: "none", status: 1, credential_configured: false, proxy_url_configured: false }, isLoading: false };
    state.routes = { data: [targetRoutes[0]], isLoading: false, error: null };

    render(<APIUpstreamForm mode={{ kind, id: 3, serviceId: 7 }} />);

    expect(preview.requests).toHaveLength(1);
    expect(screen.getByTestId("segmented-url-text")).toHaveTextContent("https://prefilled.test/server-preview/forecast");
    expect(screen.queryByText("endpointPreviewRequired")).not.toBeInTheDocument();
    expect(screen.queryByText("endpointPreviewPreparing")).not.toBeInTheDocument();
  });

  it("shows preparing while an edited prefilled Base URL waits for debounce", () => {
    vi.useFakeTimers();
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: "https://prefilled.test", weight: 1, priority: 0, auth_type: "none", status: 1, credential_configured: false, proxy_url_configured: false }, isLoading: false };
    state.routes = { data: [targetRoutes[0]], isLoading: false, error: null };
    render(<APIUpstreamForm mode={{ kind: "edit", id: 3, serviceId: 7 }} />);
    expect(preview.requests).toHaveLength(1);

    preview.requests = [];
    fireEvent.change(screen.getByLabelText("baseUrl"), { target: { value: "https://changed.test" } });
    expect(preview.requests).toHaveLength(0);
    expect(screen.getByText("endpointPreviewPreparing")).toBeInTheDocument();
    expect(screen.queryByText("endpointPreviewRequired")).not.toBeInTheDocument();
    expect(screen.queryByTestId("segmented-url-text")).not.toBeInTheDocument();
    act(() => { vi.advanceTimersByTime(299); });
    expect(preview.requests).toHaveLength(0);
    expect(screen.getByText("endpointPreviewPreparing")).toBeInTheDocument();

    act(() => { vi.advanceTimersByTime(1); });
    expect(preview.requests).toHaveLength(1);
    expect(screen.queryByText("endpointPreviewPreparing")).not.toBeInTheDocument();
    expect(screen.getByTestId("segmented-url-text")).toHaveTextContent("https://changed.test/server-preview/forecast");
  });

  it("cancels pending Route previews when the Route list changes", () => {
    vi.useFakeTimers();
    state.routes = { data: targetRoutes, isLoading: false, error: null };
    const { rerender } = render(<APIUpstreamForm mode={{ kind: "create", serviceId: 7, backendId: 12 }} />);

    fireEvent.change(screen.getByLabelText("baseUrl"), { target: { value: "https://edge.test/pending" } });
    state.routes = { data: [], isLoading: false, error: null };
    rerender(<APIUpstreamForm mode={{ kind: "create", serviceId: 7, backendId: 12 }} />);
    act(() => { vi.advanceTimersByTime(300); });

    expect(preview.requests).toHaveLength(0);
    expect(screen.getByText("endpointRouteResultsEmpty")).toBeInTheDocument();
  });

  it("debounces a newly mounted Route preview when the Route list changes", () => {
    vi.useFakeTimers();
    state.routes = { data: targetRoutes, isLoading: false, error: null };
    const { rerender } = render(<APIUpstreamForm mode={{ kind: "create", serviceId: 7, backendId: 12 }} />);
    fireEvent.change(screen.getByLabelText("baseUrl"), { target: { value: "https://edge.test/stable" } });
    act(() => { vi.advanceTimersByTime(300); });
    expect(preview.requests).toHaveLength(2);

    preview.requests = [];
    state.routes = { data: [{ ...targetRoutes[0], id: 10, slug: "alerts" }], isLoading: false, error: null };
    rerender(<APIUpstreamForm mode={{ kind: "create", serviceId: 7, backendId: 12 }} />);
    expect(preview.requests).toHaveLength(0);
    act(() => { vi.advanceTimersByTime(299); });
    expect(preview.requests).toHaveLength(0);
    act(() => { vi.advanceTimersByTime(1); });
    expect(preview.requests).toHaveLength(1);
  });

  it("shows an idle hint instead of loading when Base URL is not ready", () => {
    state.routes = { data: [targetRoutes[0]], isLoading: false, error: null };

    render(<APIUpstreamForm mode={{ kind: "create", serviceId: 7, backendId: 12 }} />);

    expect(screen.getByText("endpointPreviewRequired")).toBeInTheDocument();
    expect(screen.getByLabelText("baseUrl")).toHaveAttribute("aria-invalid", "false");
    expect(screen.queryByText("endpointPreviewInvalid")).not.toBeInTheDocument();
    expect(screen.queryByRole("status", { name: "endpointPreviewLoading" })).not.toBeInTheDocument();
    expect(preview.requests).toHaveLength(0);
  });

  it.each(["relative/path", "ftp://edge.test", "https://user:secret@edge.test", "https://edge.test/#fragment"])('keeps invalid Base URL %j in the invalid state', (baseURL) => {
    vi.useFakeTimers();
    state.routes = { data: [targetRoutes[0]], isLoading: false, error: null };
    render(<APIUpstreamForm mode={{ kind: "create", serviceId: 7, backendId: 12 }} />);

    fireEvent.change(screen.getByLabelText("baseUrl"), { target: { value: baseURL } });
    act(() => { vi.advanceTimersByTime(300); });

    expect(screen.getAllByText("endpointPreviewInvalid")).toHaveLength(2);
    expect(screen.getByLabelText("baseUrl")).toHaveAttribute("aria-invalid", "true");
    expect(screen.queryByText("endpointPreviewRequired")).not.toBeInTheDocument();
    expect(screen.queryByText("endpointPreviewPreparing")).not.toBeInTheDocument();
    expect(preview.requests).toHaveLength(0);
  });

  it.each([
    ["invalid percent escape", "https://edge.test/path%zz"],
    ["invalid query percent escape", "https://edge.test/path?query=%zz"],
    ["leading space", " https://edge.test/path"],
    ["trailing space", "https://edge.test/path "],
    ["newline", "https://edge.test/path\nnext"],
    ["ASCII control", "https://edge.test/path\u0001next"],
    ["opaque HTTP URL normalized by WHATWG", "http:edge.test/path"],
    ["empty authority with three slashes", "https:///edge.test/path"],
    ["empty authority with four slashes", "https:////edge.test/path"],
    ["empty userinfo", "https://@edge.test/path"],
    ["authority backslash normalized by WHATWG", "https://edge.test\\hidden"],
    ["IPv6 zone without a closing bracket", "https://[fe80::1%25eth0/path"],
    ["IPv6 zone with a nonnumeric port", "https://[fe80::1%25eth0]:named/path"],
    ["encoded slash", "https://edge.test/a%2Fb"],
    ["double encoded slash", "https://edge.test/a%252Fb"],
    ["encoded backslash", "https://edge.test/a%5Cb"],
    ["double encoded backslash", "https://edge.test/a%255Cb"],
    ["dot segment", "https://edge.test/a/../secret"],
    ["encoded dot segment", "https://edge.test/a/%2e%2e/secret"],
    ["double encoded dot segment", "https://edge.test/a/%252e%252e/secret"],
    ["encoded NUL", "https://edge.test/a%00b"],
  ])("keeps a server-rejected prefilled Base URL with %s out of Preview", (_name, baseURL) => {
    vi.useFakeTimers();
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: baseURL, weight: 1, priority: 0, auth_type: "none", status: 1, credential_configured: false, proxy_url_configured: false }, isLoading: false };
    state.routes = { data: [targetRoutes[0]], isLoading: false, error: null };

    render(<APIUpstreamForm mode={{ kind: "edit", id: 3, serviceId: 7 }} />);
    act(() => { vi.advanceTimersByTime(600); });

    expect(screen.getAllByText("endpointPreviewInvalid")).toHaveLength(2);
    expect(screen.getByLabelText("baseUrl")).toHaveAttribute("aria-invalid", "true");
    expect(screen.queryByText("endpointPreviewRequired")).not.toBeInTheDocument();
    expect(screen.queryByText("endpointPreviewPreparing")).not.toBeInTheDocument();
    expect(preview.requests).toHaveLength(0);
  });

  it.each([
    ["encoded path space", "https://edge.test/a%20b"],
    ["encoded path percent", "https://edge.test/discount%25off"],
    ["encoded query values", "https://edge.test/path?query=a%20b&slash=%2F"],
    ["uppercase scheme", "HTTPS://edge.test/path"],
    ["ordinary authority", "https://edge.test"],
    ["IPv6 authority", "https://[2001:db8::1]:8443/path"],
    ["scoped IPv6 authority", "https://[fe80::1%25eth0]:8443/path"],
    ["raw query backslash", "https://edge.test/path?marker=\\"],
  ])("previews a safe prefilled Base URL with %s", (_name, baseURL) => {
    vi.useFakeTimers();
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: baseURL, weight: 1, priority: 0, auth_type: "none", status: 1, credential_configured: false, proxy_url_configured: false }, isLoading: false };
    state.routes = { data: [targetRoutes[0]], isLoading: false, error: null };

    render(<APIUpstreamForm mode={{ kind: "edit", id: 3, serviceId: 7 }} />);

    expect(screen.getByLabelText("baseUrl")).toHaveAttribute("aria-invalid", "false");
    expect(screen.queryByText("endpointPreviewInvalid")).not.toBeInTheDocument();
    expect(preview.requests).toHaveLength(1);
  });

  it.each([
    ["percent-encoded zone", "https://[fe80::1%25%65%6e%301-._~]:8443/path"],
    ["encoded zone punctuation", "https://[fe80::1%25eth%2D0]:8443/path"],
    ["empty optional port", "https://[fe80::1%25eth0]:/path"],
    ["uncertain IPv6 literal", "https://[1::1::1%25eth0]:8443/path"],
    ["empty zone for server validation", "https://[fe80::1%25]/path"],
    ["zone ending in an encoded percent", "https://[fe80::1%25eth0%25]/path"],
  ])("defers a structurally complete bracketed authority with %s to Preview", (_name, baseURL) => {
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: baseURL, weight: 1, priority: 0, auth_type: "none", status: 1, credential_configured: false, proxy_url_configured: false }, isLoading: false };
    state.routes = { data: [targetRoutes[0]], isLoading: false, error: null };

    render(<APIUpstreamForm mode={{ kind: "edit", id: 3, serviceId: 7 }} />);

    expect(screen.getByLabelText("baseUrl")).toHaveAttribute("aria-invalid", "false");
    expect(screen.queryByText("endpointPreviewInvalid")).not.toBeInTheDocument();
    expect(preview.requests).toHaveLength(1);
    expect((preview.requests[0]?.target as { first_upstream: { base_url: string } }).first_upstream.base_url).toBe(baseURL);
  });

  it("defers an uncertain IPv4-mapped bracketed authority and shows the server Preview error", () => {
    const baseURL = "https://[::ffff:192.0.2.1%25eth0]:8443/path";
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: baseURL, weight: 1, priority: 0, auth_type: "none", status: 1, credential_configured: false, proxy_url_configured: false }, isLoading: false };
    state.routes = { data: [targetRoutes[0]], isLoading: false, error: null };
    preview.state = { data: undefined, isLoading: false, error: new Error("server rejected authority"), refetch: vi.fn() };

    render(<APIUpstreamForm mode={{ kind: "edit", id: 3, serviceId: 7 }} />);

    expect(screen.getByLabelText("baseUrl")).toHaveAttribute("aria-invalid", "false");
    expect(preview.requests).toHaveLength(1);
    expect(screen.getByRole("alert")).toHaveTextContent("endpointPreviewFailed");
  });

  it("shows a local loading placeholder while an Endpoint Route Preview is pending", () => {
    vi.useFakeTimers();
    state.routes = { data: [targetRoutes[0]], isLoading: false, error: null };
    preview.state = { data: undefined, isLoading: true, error: null, refetch: vi.fn() };
    render(<APIUpstreamForm mode={{ kind: "create", serviceId: 7, backendId: 12 }} />);

    fireEvent.change(screen.getByLabelText("baseUrl"), { target: { value: "https://edge.test" } });
    act(() => { vi.advanceTimersByTime(300); });

    expect(screen.getByRole("status", { name: "endpointPreviewLoading" })).toBeInTheDocument();
    expect(screen.queryByText("endpointPreviewRequired")).not.toBeInTheDocument();
  });

  it("retries one failed Endpoint Route Preview without losing form drafts", () => {
    vi.useFakeTimers();
    const refetch = vi.fn();
    state.routes = { data: [targetRoutes[0]], isLoading: false, error: null };
    preview.state = { data: undefined, isLoading: false, error: new Error("preview rejected"), refetch };
    render(<APIUpstreamForm mode={{ kind: "create", serviceId: 7, backendId: 12 }} />);

    fireEvent.change(screen.getByLabelText("name"), { target: { value: "Primary draft" } });
    fireEvent.change(screen.getByLabelText("baseUrl"), { target: { value: "https://edge.test/draft" } });
    act(() => { vi.advanceTimersByTime(300); });
    fireEvent.click(screen.getByRole("button", { name: "retryEndpointPreview" }));

    expect(refetch).toHaveBeenCalledOnce();
    expect(screen.getByRole("alert")).toHaveTextContent("endpointPreviewFailed");
    expect(screen.getByLabelText("name")).toHaveValue("Primary draft");
    expect(screen.getByLabelText("baseUrl")).toHaveValue("https://edge.test/draft");
  });

  it("shows a distinct empty Endpoint Route Preview state", () => {
    vi.useFakeTimers();
    state.routes = { data: [targetRoutes[0]], isLoading: false, error: null };
    preview.state = { data: { endpoints: [], diagnostics: [] }, isLoading: false, error: null, refetch: vi.fn() };
    render(<APIUpstreamForm mode={{ kind: "create", serviceId: 7, backendId: 12 }} />);

    fireEvent.change(screen.getByLabelText("baseUrl"), { target: { value: "https://edge.test" } });
    act(() => { vi.advanceTimersByTime(300); });

    expect(screen.getByText("endpointPreviewEmpty")).toBeInTheDocument();
    expect(screen.queryByText("endpointPreviewRequired")).not.toBeInTheDocument();
  });

  it("gives the Endpoint result copy action a 44px touch target", () => {
    vi.useFakeTimers();
    state.routes = { data: [targetRoutes[0]], isLoading: false, error: null };
    render(<APIUpstreamForm mode={{ kind: "create", serviceId: 7, backendId: 12 }} />);

    fireEvent.change(screen.getByLabelText("name"), { target: { value: "Primary" } });
    fireEvent.change(screen.getByLabelText("baseUrl"), { target: { value: "https://edge.test" } });
    act(() => { vi.advanceTimersByTime(300); });

    screen.getByRole("button", { name: "copyEndpointURL" });
    expect(screen.getByTestId("endpoint-route-result")).toHaveClass("[&_[data-slot=button]]:size-11");
  });

  it("offers the Target picker only for context-free create", () => {
    const { rerender } = render(<APIUpstreamForm mode={{ kind: "create", serviceId: 7 }} />);
    expect(screen.getByRole("combobox", { name: "backend" })).toBeInTheDocument();

    rerender(<APIUpstreamForm mode={{ kind: "create", serviceId: 7, backendId: 12 }} />);
    expect(screen.queryByRole("combobox", { name: "backend" })).not.toBeInTheDocument();
    expect(screen.getAllByText("Primary target")).toHaveLength(1);
  });

  it("keeps an edited Endpoint's Backend immutable and offers a copy action", () => {
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: "https://example.test", weight: 1, priority: 0, auth_type: "none", status: 1, credential_configured: false, proxy_url_configured: false }, isLoading: false };
    render(<APIUpstreamForm mode={{ kind: "edit", id: 3, serviceId: 7 }} />);

    expect(screen.queryByRole("combobox", { name: "backend" })).not.toBeInTheDocument();
    expect(screen.getByText("backendImmutable")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "copyEndpoint" })).toHaveAttribute("href", "/api-services/upstreams/new?service_id=7&copy_id=3");
  });

  it("keeps Route context in Endpoint edit cancel and copy navigation", async () => {
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: "https://example.test", weight: 1, priority: 0, auth_type: "none", status: 1, credential_configured: false, proxy_url_configured: false }, isLoading: false };
    const mode = { kind: "edit" as const, id: 3, serviceId: 7, returnRoute: { id: 9, slug: "daily-report-9" } };
    render(<APIUpstreamForm mode={mode} />);
    expect(screen.getByRole("link", { name: "copyEndpoint" })).toHaveAttribute("href", "/api-services/upstreams/new?service_id=7&copy_id=3&route_id=9&route_slug=daily-report-9");
    await userEvent.setup().click(screen.getByRole("button", { name: "cancel" }));
    expect(navigation.push).toHaveBeenCalledWith("/api-services/detail?id=7&route_search=daily-report-9&route=9");
  });

  it("returns a saved contextual Endpoint edit to its Route", async () => {
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: "https://example.test", weight: 1, priority: 0, auth_type: "none", status: 1, credential_configured: false, proxy_url_configured: false }, isLoading: false };
    mutations.update.mockResolvedValue({ id: 3 });
    render(<APIUpstreamForm mode={{ kind: "edit", id: 3, serviceId: 7, returnRoute: { id: 9, slug: "forecast-v2" } }} />);

    await userEvent.setup().click(screen.getByRole("button", { name: "save" }));
    expect(mutations.update).toHaveBeenCalledWith(expect.objectContaining({ id: 3 }));
    expect(navigation.push).toHaveBeenCalledWith("/api-services/detail?id=7&route_search=forecast-v2&route=9");
  });

  it("returns a saved contextual Endpoint copy to its Route", async () => {
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: "https://example.test", weight: 1, priority: 0, auth_type: "none", status: 1, credential_configured: false, proxy_url_configured: false }, isLoading: false };
    mutations.create.mockResolvedValue({ id: 4 });
    const user = userEvent.setup();
    render(<APIUpstreamForm mode={{ kind: "copy", id: 3, serviceId: 7, returnRoute: { id: 9, slug: "forecast-v2" } }} />);
    await user.type(screen.getByLabelText("name"), "primary copy");
    await user.click(screen.getByRole("button", { name: "save" }));

    expect(mutations.create).toHaveBeenCalledWith(expect.objectContaining({ backend_id: 12, name: "primary copy" }));
    expect(navigation.push).toHaveBeenCalledWith("/api-services/detail?id=7&route_search=forecast-v2&route=9");
  });

  it("keeps a contextual Endpoint form in place when its mutation fails", async () => {
    mutations.create.mockRejectedValue(new Error("endpoint create rejected"));
    const user = userEvent.setup();
    render(<APIUpstreamForm mode={{ kind: "create", serviceId: 7, backendId: 12, returnRoute: { id: 9, slug: "forecast-v2" } }} />);
    await user.type(screen.getByLabelText("name"), "primary");
    await user.type(screen.getByLabelText("baseUrl"), "https://example.test");
    await user.click(screen.getByRole("button", { name: "save" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("endpoint create rejected");
    expect(navigation.push).not.toHaveBeenCalled();
  });

  it("copies an Endpoint by creating a new record in its read-only Target", async () => {
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: "https://example.test", weight: 1, priority: 0, auth_type: "none", status: 1, credential_configured: false, proxy_url_configured: false, header_override: { "X-Tenant": "west" } }, isLoading: false };
    state.backend = { data: { id: 12, api_service_id: 7 }, isLoading: false };
    mutations.create.mockResolvedValue({ id: 4 });
    const user = userEvent.setup();
    render(<APIUpstreamForm mode={{ kind: "copy", id: 3, serviceId: 7 }} />);

    expect(screen.queryByRole("combobox", { name: "backend" })).not.toBeInTheDocument();
    expect(screen.getByLabelText("name")).toHaveValue("");
    await user.type(screen.getByLabelText("name"), "primary copy");
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.create).toHaveBeenCalledWith(expect.objectContaining({ backend_id: 12, name: "primary copy", base_url: "https://example.test" }));
    expect(mutations.create).toHaveBeenCalledWith(expect.objectContaining({ header_override: { "X-Tenant": "west" } }));
    expect(mutations.update).not.toHaveBeenCalled();
  });

  it("edits non-secret Header overrides through the shared row editor", async () => {
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: "https://example.test", weight: 1, priority: 0, auth_type: "none", status: 1, credential_configured: false, proxy_url_configured: false, header_override: { "X-Tenant": "west" } }, isLoading: false };
    mutations.update.mockResolvedValue({ id: 3 });
    const user = userEvent.setup();
    render(<APIUpstreamForm mode={{ kind: "edit", id: 3, serviceId: 7 }} />);
    await user.click(screen.getByRole("button", { name: "showAdvanced" }));

    expect(screen.getByLabelText("headerName")).toHaveValue("X-Tenant");
    await user.clear(screen.getByLabelText("headerValue"));
    await user.type(screen.getByLabelText("headerValue"), "east");
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.update).toHaveBeenCalledWith(expect.objectContaining({ header_override: { "X-Tenant": "east" } }));
  });

  it("requires a fresh credential when copying an authenticated Endpoint", async () => {
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: "https://example.test", weight: 1, priority: 0, auth_type: "bearer", status: 1, credential_configured: true, proxy_url_configured: true }, isLoading: false };
    state.backend = { data: { id: 12, api_service_id: 7 }, isLoading: false };
    const user = userEvent.setup();
    render(<APIUpstreamForm mode={{ kind: "copy", id: 3, serviceId: 7 }} />);

    expect(screen.queryByText("credentialConfigured")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "showAdvanced" }));
    expect(screen.getByText("proxyOptional")).toBeInTheDocument();
    await user.type(screen.getByLabelText("name"), "authenticated copy");
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.create).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent("credentialRequired");
  });

  it("fails closed when URL-selected Backend belongs to another Service", () => {
    state.backend = { data: { id: 12, api_service_id: 8 }, isLoading: false };
    render(<APIUpstreamForm mode={{ kind: "create", serviceId: 7, backendId: 12 }} />);
    expect(screen.getByText("upstreamNotFound")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "save" })).not.toBeInTheDocument();
  });

  it("asks for confirmation before disabling the last enabled Endpoint", async () => {
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: "https://example.test", weight: 1, priority: 0, auth_type: "none", status: 1, credential_configured: false, proxy_url_configured: false }, isLoading: false };
    state.backend = { data: { id: 12, api_service_id: 7, enabled_upstream_count: 1, route_count: 2 }, isLoading: false };
    mutations.update.mockResolvedValue({ id: 3 });
    const user = userEvent.setup();
    render(<APIUpstreamForm mode={{ kind: "edit", id: 3, serviceId: 7 }} />);

    await user.click(screen.getByRole("switch", { name: "enabled" }));
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.update).not.toHaveBeenCalled();
    expect(screen.getByRole("alertdialog")).toHaveTextContent("lastEndpointDisableDescription");
    expect(screen.getByRole("alertdialog")).toHaveTextContent("confirmDisableEndpointTitle");
    expect(screen.getByRole("button", { name: "confirmDisableEndpoint" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "confirmDisableEndpoint" }));
    await waitFor(() => expect(mutations.update).toHaveBeenCalledWith(expect.objectContaining({ id: 3, status: 0 })));
  });

  it("keeps the last Endpoint confirmation and draft open when disabling fails", async () => {
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: "https://example.test", weight: 1, priority: 0, auth_type: "none", status: 1, credential_configured: false, proxy_url_configured: false }, isLoading: false };
    state.backend = { data: { id: 12, api_service_id: 7, enabled_upstream_count: 1, route_count: 2 }, isLoading: false };
    mutations.update.mockRejectedValue(new Error("disable rejected"));
    const user = userEvent.setup();
    render(<APIUpstreamForm mode={{ kind: "edit", id: 3, serviceId: 7 }} />);

    await user.click(screen.getByRole("switch", { name: "enabled" }));
    await user.click(screen.getByRole("button", { name: "save" }));
    await user.click(screen.getByRole("button", { name: "confirmDisableEndpoint" }));

    expect(await screen.findByRole("alertdialog")).toHaveTextContent("disable rejected");
    expect(screen.getByRole("switch", { name: "enabled", hidden: true })).not.toBeChecked();
  });

  it("does not echo configured secrets and preserves untouched credential and proxy", async () => {
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: "https://example.test", weight: 2, priority: 0, auth_type: "bearer", status: 1, credential_configured: true, proxy_url_configured: true }, isLoading: false };
    state.backend = { data: { id: 12, api_service_id: 7 }, isLoading: false };
    mutations.update.mockResolvedValue({ id: 3 });
    const user = userEvent.setup();
    render(<APIUpstreamForm mode={{ kind: "edit", id: 3, serviceId: 7 }} />);
    expect(screen.queryByDisplayValue("stored-secret")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "save" }));
    const payload = mutations.update.mock.calls[0]?.[0];
    expect(payload).not.toHaveProperty("credential");
    expect(payload).not.toHaveProperty("proxy_url");
    expect(payload).not.toHaveProperty("api_service_id");
  });

  it("clears configured credential and proxy only through explicit switches", async () => {
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: "https://example.test", weight: 2, priority: 0, auth_type: "bearer", status: 1, credential_configured: true, proxy_url_configured: true }, isLoading: false };
    mutations.update.mockResolvedValue({ id: 3 });
    const user = userEvent.setup();
    render(<APIUpstreamForm mode={{ kind: "edit", id: 3, serviceId: 7 }} />);
    await user.click(screen.getByRole("switch", { name: "clearCredential" }));
    await user.click(screen.getByRole("button", { name: "showAdvanced" }));
    await user.click(screen.getByRole("switch", { name: "clearProxy" }));
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.update).toHaveBeenCalledWith(expect.objectContaining({ id: 3, auth_type: "none", credential: {}, proxy_url: "" }));
  });

  it("requires a matching credential for an unconfigured auth type and keeps input on mutation error", async () => {
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: "https://example.test", weight: 1, priority: 0, auth_type: "bearer", status: 1, credential_configured: false, proxy_url_configured: false }, isLoading: false };
    const user = userEvent.setup();
    render(<APIUpstreamForm mode={{ kind: "edit", id: 3, serviceId: 7 }} />);
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.update).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent("credentialRequired");
    await user.type(screen.getByLabelText("bearerToken"), "new-secret");
    mutations.update.mockRejectedValue(new Error("upstream rejected"));
    await user.click(screen.getByRole("button", { name: "save" }));
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("upstream rejected");
    await waitFor(() => expect(alert).toHaveFocus());
    expect(screen.getByLabelText("bearerToken")).toHaveValue("new-secret");
  });

  it("does not carry credential or proxy drafts across edit identities", async () => {
    state.query = { data: { id: 3, backend_id: 12, name: "upstream-a", base_url: "https://a.test", weight: 1, priority: 0, auth_type: "bearer", status: 1, credential_configured: true, proxy_url_configured: true }, isLoading: false };
    const user = userEvent.setup();
    const { rerender } = render(<APIUpstreamForm mode={{ kind: "edit", id: 3, serviceId: 7 }} />);
    await user.type(screen.getByLabelText("bearerToken"), "secret-a");
    await user.click(screen.getByRole("button", { name: "showAdvanced" }));
    await user.type(screen.getByLabelText("proxyUrl"), "https://proxy-a.test");
    state.query = { data: { id: 4, backend_id: 13, name: "upstream-b", base_url: "https://b.test", weight: 2, priority: 1, auth_type: "bearer", status: 1, credential_configured: true, proxy_url_configured: true }, isLoading: false };
    state.backend = { data: { id: 13, api_service_id: 8 }, isLoading: false };
    rerender(<APIUpstreamForm mode={{ kind: "edit", id: 4, serviceId: 8 }} />);
    expect(screen.getByLabelText("name")).toHaveValue("upstream-b");
    expect(screen.getByLabelText("bearerToken")).toHaveValue("");
    expect(screen.getByLabelText("proxyUrl")).toHaveValue("");
  });

  it("fails closed when the loaded upstream belongs to another service", () => {
    state.query = { data: { id: 3, backend_id: 12, name: "upstream-a", base_url: "https://a.test", weight: 1, priority: 0, auth_type: "none", status: 1, credential_configured: false, proxy_url_configured: false }, isLoading: false };
    state.backend = { data: { id: 12, api_service_id: 8 }, isLoading: false };
    render(<APIUpstreamForm mode={{ kind: "edit", id: 3, serviceId: 7 }} />);
    expect(screen.getByText("upstreamNotFound")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "save" })).not.toBeInTheDocument();
  });

  it("treats configured authentication changing to none as an explicit credential clear", async () => {
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: "https://example.test", weight: 1, priority: 0, auth_type: "bearer", status: 1, credential_configured: true, proxy_url_configured: false }, isLoading: false };
    mutations.update.mockResolvedValue({ id: 3 });
    const user = userEvent.setup();
    render(<APIUpstreamForm mode={{ kind: "edit", id: 3, serviceId: 7 }} />);
    await user.click(screen.getByRole("combobox", { name: "authType" }));
    await user.click(await screen.findByRole("option", { name: "none" }));
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.update).toHaveBeenCalledWith(expect.objectContaining({ id: 3, auth_type: "none", credential: {} }));
  });

  it("clears credentials when authentication changes to none even if none are configured", async () => {
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: "https://example.test", weight: 1, priority: 0, auth_type: "bearer", status: 1, credential_configured: false, proxy_url_configured: false }, isLoading: false };
    mutations.update.mockResolvedValue({ id: 3 });
    const user = userEvent.setup();
    render(<APIUpstreamForm mode={{ kind: "edit", id: 3, serviceId: 7 }} />);
    await user.click(screen.getByRole("combobox", { name: "authType" }));
    await user.click(await screen.findByRole("option", { name: "none" }));
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.update).toHaveBeenCalledWith(expect.objectContaining({ id: 3, auth_type: "none", credential: {} }));
  });

  it("sends explicit credential and proxy replacements", async () => {
    state.query = { data: { id: 3, backend_id: 12, name: "primary", base_url: "https://example.test", weight: 1, priority: 0, auth_type: "bearer", status: 1, credential_configured: true, proxy_url_configured: true }, isLoading: false };
    mutations.update.mockResolvedValue({ id: 3 });
    const user = userEvent.setup();
    render(<APIUpstreamForm mode={{ kind: "edit", id: 3, serviceId: 7 }} />);
    await user.type(screen.getByLabelText("bearerToken"), "replacement-token");
    await user.click(screen.getByRole("button", { name: "showAdvanced" }));
    await user.type(screen.getByLabelText("proxyUrl"), "https://replacement-proxy.test");
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.update).toHaveBeenCalledWith(expect.objectContaining({
      credential: { bearer_token: "replacement-token" },
      proxy_url: "https://replacement-proxy.test",
    }));
  });
});
