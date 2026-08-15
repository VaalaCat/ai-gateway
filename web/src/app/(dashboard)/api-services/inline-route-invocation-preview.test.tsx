import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { APIBackend, APIRoute } from "@/lib/api/api-services";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";

import { InlineRouteInvocationPreview } from "./_components/route-table/inline-route-invocation-preview";
import { STANDARD_ROUTE_HTTP_METHODS } from "./_components/route-editor/standard-http-methods";

const hooks = vi.hoisted(() => ({
  preview: { data: { endpoints: [], diagnostics: [] } as unknown, isLoading: false, error: null as unknown, refetch: vi.fn() },
  usePreview: vi.fn(),
}));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => ({
  method: "方法", subpath: "子路径", query: "Query", headers: "Header", body: "Body",
  requestDetails: "请求详情", retryPreview: "重试", copyCommand: "复制命令", clientRequest: "客户端请求",
  invocationProtocol: "协议", websocketProtocol: "WebSocket", websocketSubprotocols: "WebSocket 子协议",
  httpProtocol: "HTTP", routingPreviewDiagnostics: "路由诊断",
  routingPreviewFailed: "预览失败", routingPreviewFailedDescription: "预览失败后可以重试",
  endpointSummary: "转发结果", noEndpointCandidates: "没有 Endpoint", routingPreviewEmptyDescription: "没有候选 Endpoint",
  endpointDisabled: "Endpoint 已停用", endpointUnavailable503: "当前 Route 会返回 503",
  routingPreviewStaticOnlyDisabled: "Endpoint 都已停用", requestHeadersJSONInvalid: "Header JSON 无效",
  routingPreviewLoading: "正在加载预览", publicUrlCopied: "已复制客户端 URL", finalUrlCopied: "已复制 Endpoint URL",
  copyClientRequest: "复制客户端 URL", copyEndpointURL: "复制 Endpoint URL", copyCommandFailed: "复制命令失败",
}[key] ?? key) }));
vi.mock("@/lib/api/api-services", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/api-services")>()),
  useAPIRoutePreview: hooks.usePreview,
}));
vi.mock("@/lib/utils/clipboard", () => ({ copyTextWithFeedback: vi.fn().mockResolvedValue(true) }));

const target: APIBackend = {
  id: 17, api_service_id: 7, name: "Primary Endpoint", route_count: 1, upstream_count: 1, enabled_upstream_count: 1, endpoint_hosts: ["weather.test"],
};

function route(overrides: Partial<APIRoute> = {}): APIRoute {
  return {
    id: 9, api_service_id: 7, backend_id: 17, slug: "forecast", protocols: ["http"], allowed_methods: ["GET"], upstream_path: "/forecast", forward_subpath: true,
    example_request: { method: "GET", subpath: "today", query: "unit=c", headers: {}, body: "" }, status: 1,
    ...overrides,
  };
}

function websocketRoute(overrides: Partial<APIRoute> = {}) {
  return route({ protocols: ["websocket"], allowed_methods: [], ...overrides });
}

function renderPreview(overrides: Partial<React.ComponentProps<typeof InlineRouteInvocationPreview>> = {}) {
  return render(<InlineRouteInvocationPreview origin="https://gateway.test" serviceSlug="weather" route={route()} target={target} dependencyRevision="test-revision" {...overrides} />);
}

describe("InlineRouteInvocationPreview", () => {
  beforeEach(() => {
    hooks.usePreview.mockReset();
    hooks.preview = { data: { endpoints: [], diagnostics: [] }, isLoading: false, error: null, refetch: vi.fn() };
    hooks.usePreview.mockImplementation(() => hooks.preview);
  });

  it("shows method subpath and query before advanced request details", () => {
    renderPreview({ route: route({ forward_subpath: true }) });
    expect(screen.getByLabelText("方法")).toBeVisible();
    expect(screen.getByLabelText("子路径")).toBeVisible();
    expect(screen.getByLabelText("Query")).toBeVisible();
    expect(screen.queryByLabelText("Header")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Body")).not.toBeInTheDocument();
  });

  it("hides subpath and body fields when the route contract forbids them", async () => {
    const user = userEvent.setup();
    renderPreview({ route: websocketRoute({ forward_subpath: false }) });
    expect(screen.queryByLabelText("子路径")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "请求详情" }));
    expect(screen.queryByLabelText("Body")).not.toBeInTheDocument();
  });

  it("keeps the draft and exposes retry after preview failure", async () => {
    const user = userEvent.setup();
    hooks.preview.error = new Error("offline");
    renderPreview({ route: route({ example_request: { method: "GET", subpath: "today", query: "", headers: {}, body: "" } }) });
    await user.type(screen.getByLabelText("Query"), "unit=c");
    const retry = screen.getByRole("button", { name: "重试" });
    expect(retry).toHaveClass("min-h-11", "min-w-11");
    await user.click(retry);
    expect(screen.getByLabelText("Query")).toHaveValue("unit=c");
    expect(hooks.preview.refetch).toHaveBeenCalled();
  });

  it("initializes visible fields from example_request and exposes concrete client and endpoint URLs", () => {
    hooks.preview.data = { endpoints: [{ upstream_id: 31, upstream_name: "Primary", status: 1, priority: 0, weight: 1, final_url: "https://weather.test/forecast/today?unit=c" }], diagnostics: [] };
    renderPreview();

    expect(screen.getByLabelText("方法")).toHaveTextContent("GET");
    expect(screen.getByLabelText("子路径")).toHaveValue("today");
    expect(screen.getByLabelText("Query")).toHaveValue("unit=c");
    expect(screen.getAllByTestId("segmented-url-text").map((item) => item.textContent)).toEqual([
      "https://gateway.test/v1/api/weather/forecast/today?unit=c",
      "https://weather.test/forecast/today?unit=c",
    ]);
    expect(screen.getByText(/curl --request 'GET'/)).toBeVisible();
  });

  it("uses the WebSocket command and omits an HTTP body editor", () => {
    renderPreview({ route: websocketRoute({ example_request: { method: "GET", subpath: "", query: "", headers: { "Sec-WebSocket-Protocol": "chat" }, body: "ignored" } }) });

    expect(screen.getByText(/websocat/)).toBeVisible();
    expect(screen.getByText("协议: WebSocket")).toBeVisible();
    expect(screen.queryByLabelText("Body")).not.toBeInTheDocument();
  });

  it("keeps HTTP preview URLs and its curl command on HTTP(S)", () => {
    hooks.preview.data = { endpoints: [{ upstream_id: 31, upstream_name: "Primary", status: 1, priority: 0, weight: 1, final_url: "https://weather.test/forecast/today?unit=c" }], diagnostics: [] };
    renderPreview();

    expect(screen.getAllByTestId("segmented-url-text").map((item) => item.textContent)).toEqual([
      "https://gateway.test/v1/api/weather/forecast/today?unit=c",
      "https://weather.test/forecast/today?unit=c",
    ]);
    expect(screen.getByText(/curl --request 'GET'/)).toBeVisible();
  });

  it("maps only HTTP(S) endpoint schemes for a WebSocket-only preview", () => {
    hooks.preview.data = { endpoints: [
      { upstream_id: 31, upstream_name: "Secure", status: 1, priority: 0, weight: 1, final_url: "https://weather.test/forecast/today?unit=c#fragment" },
      { upstream_id: 32, upstream_name: "Custom", status: 1, priority: 0, weight: 1, final_url: "unix:///var/run/weather.sock" },
    ], diagnostics: [] };
    renderPreview({ route: websocketRoute() });

    expect(screen.getAllByTestId("segmented-url-text").map((item) => item.textContent)).toEqual([
      "wss://gateway.test/v1/api/weather/forecast/today?unit=c",
      "wss://weather.test/forecast/today?unit=c#fragment",
      "unix:///var/run/weather.sock",
    ]);
    expect(screen.getByText(/websocat/)).toBeVisible();
  });

  it("switches the client URL, endpoint URL, and copied command between HTTP and WebSocket together", async () => {
    const user = userEvent.setup();
    hooks.preview.data = { endpoints: [{ upstream_id: 31, upstream_name: "Primary", status: 1, priority: 0, weight: 1, final_url: "http://weather.test/forecast/today?unit=c" }], diagnostics: [] };
    renderPreview({ route: route({ protocols: ["http", "websocket"] }) });

    expect(screen.getAllByTestId("segmented-url-text").map((item) => item.textContent)).toEqual([
      "https://gateway.test/v1/api/weather/forecast/today?unit=c",
      "http://weather.test/forecast/today?unit=c",
    ]);
    expect(screen.getByText(/curl --request 'GET'/)).toBeVisible();
    await user.click(screen.getByRole("button", { name: "复制命令" }));
    expect(copyTextWithFeedback).toHaveBeenLastCalledWith(expect.stringContaining("curl --request 'GET'"), expect.any(Object));

    await user.click(screen.getByRole("radio", { name: "WebSocket" }));
    expect(screen.getAllByTestId("segmented-url-text").map((item) => item.textContent)).toEqual([
      "wss://gateway.test/v1/api/weather/forecast/today?unit=c",
      "ws://weather.test/forecast/today?unit=c",
    ]);
    expect(screen.getByText(/websocat/)).toBeVisible();
    await user.click(screen.getByRole("button", { name: "复制命令" }));
    expect(copyTextWithFeedback).toHaveBeenLastCalledWith(expect.stringContaining("websocat"), expect.any(Object));
  });

  it("does not issue a preview or retain a command for invalid Header JSON", async () => {
    const user = userEvent.setup();
    renderPreview();
    await user.click(screen.getByRole("button", { name: "请求详情" }));
    expect(screen.getByRole("button", { name: "请求详情" })).toHaveClass("min-h-11", "min-w-11");
    fireEvent.change(screen.getByLabelText("Header"), { target: { value: "{" } });

    expect(hooks.usePreview).toHaveBeenLastCalledWith(undefined, { cacheKey: undefined });
    expect(screen.getByText("Header JSON 无效")).toBeVisible();
    expect(screen.queryByRole("button", { name: "复制命令" })).not.toBeInTheDocument();
  });

  it("resets the draft when a different Route replaces this workspace", async () => {
    const user = userEvent.setup();
    const view = renderPreview();
    await user.clear(screen.getByLabelText("Query"));
    await user.type(screen.getByLabelText("Query"), "unit=f");

    view.rerender(<InlineRouteInvocationPreview origin="https://gateway.test" serviceSlug="weather" target={target} dependencyRevision="test-revision" route={route({ id: 10, slug: "history", example_request: { method: "POST", subpath: "week", query: "days=7", headers: {}, body: "" } })} />);
    expect(screen.getByLabelText("子路径")).toHaveValue("week");
    expect(screen.getByLabelText("Query")).toHaveValue("days=7");
  });

  it("uses one effective all-method example for the visible method, preview, URL, and command", () => {
    renderPreview({ route: route({ allowed_methods: [], example_request: { method: "", subpath: "", query: "", headers: {}, body: "" } }) });

    expect(screen.getByLabelText("方法")).toHaveTextContent("GET");
    expect(hooks.usePreview).toHaveBeenLastCalledWith(expect.objectContaining({ sample: expect.objectContaining({ method: "GET" }) }), { cacheKey: "9:invocation:http:test-revision" });
    expect(screen.getByTestId("segmented-url-text")).toHaveTextContent("https://gateway.test/v1/api/weather/forecast");
    expect(screen.getByText(/curl --request 'GET'/)).toBeVisible();
  });

  it("uses the first allowed method when a saved example has no method", () => {
    renderPreview({ route: route({ allowed_methods: ["POST"], example_request: { method: "", subpath: "", query: "", headers: {}, body: "" } }) });

    expect(screen.getByLabelText("方法")).toHaveTextContent("POST");
    expect(hooks.usePreview).toHaveBeenLastCalledWith(expect.objectContaining({ sample: expect.objectContaining({ method: "POST" }) }), { cacheKey: "9:invocation:http:test-revision" });
    expect(screen.getByText(/curl --request 'POST'/)).toBeVisible();
  });

  it("removes a saved subpath from every invocation output when forwarding is disabled", () => {
    renderPreview({ route: route({ forward_subpath: false, example_request: { method: "GET", subpath: "legacy", query: "unit=c", headers: {}, body: "" } }) });

    expect(screen.queryByLabelText("子路径")).not.toBeInTheDocument();
    expect(hooks.usePreview).toHaveBeenLastCalledWith(expect.objectContaining({ sample: expect.objectContaining({ subpath: "" }) }), { cacheKey: "9:invocation:http:test-revision" });
    expect(screen.getByTestId("segmented-url-text")).toHaveTextContent("https://gateway.test/v1/api/weather/forecast?unit=c");
    expect(screen.getByText(/--url 'https:\/\/gateway.test\/v1\/api\/weather\/forecast\?unit=c'/)).toBeVisible();
  });

  it("selects HTTP or WebSocket explicitly for a dual-protocol Route", async () => {
    const user = userEvent.setup();
    renderPreview({ route: route({ protocols: ["http", "websocket"], websocket_subprotocols: ["chat"], example_request: { method: "POST", subpath: "today", query: "", headers: {}, body: "payload" } }) });

    expect(screen.getByRole("radio", { name: "HTTP" })).toHaveAttribute("data-state", "on");
    await user.click(screen.getByRole("button", { name: "请求详情" }));
    expect(screen.getByLabelText("Body")).toBeVisible();
    expect(screen.getByText(/curl --request 'POST'/)).toBeVisible();
    expect(screen.getByTestId("segmented-url-text")).toHaveTextContent("https://gateway.test/v1/api/weather/forecast/today");
    await user.click(screen.getByRole("radio", { name: "WebSocket" }));
    expect(screen.getByRole("radio", { name: "WebSocket" })).toHaveAttribute("data-state", "on");
    expect(screen.queryByLabelText("Body")).not.toBeInTheDocument();
    expect(screen.getByText("协议: WebSocket")).toBeVisible();
    expect(screen.getByText("WebSocket 子协议: chat")).toBeVisible();
    expect(screen.getByText(/websocat/)).toBeVisible();
    expect(screen.getByTestId("segmented-url-text")).toHaveTextContent("wss://gateway.test/v1/api/weather/forecast/today");
  });

  it("injects a Route default subprotocol only into the selected WebSocket invocation", async () => {
    const user = userEvent.setup();
    renderPreview({ route: route({ protocols: ["http", "websocket"], websocket_subprotocols: ["chat"], example_request: { method: "GET", subpath: "", query: "", headers: {}, body: "" } }) });

    await user.click(screen.getByRole("button", { name: "请求详情" }));
    expect(screen.getByLabelText("Header")).toHaveValue("{}");
    expect(hooks.usePreview).toHaveBeenLastCalledWith(expect.objectContaining({ sample: expect.objectContaining({ headers: {} }) }), { cacheKey: "9:invocation:http:test-revision" });
    expect(screen.getByText(/curl/)).not.toHaveTextContent("Sec-WebSocket-Protocol");

    await user.click(screen.getByRole("radio", { name: "WebSocket" }));
    expect(hooks.usePreview).toHaveBeenLastCalledWith(expect.objectContaining({ sample: expect.objectContaining({ headers: { "Sec-WebSocket-Protocol": "chat" } }) }), { cacheKey: "9:invocation:websocket:test-revision" });
    expect(screen.getByText(/websocat/)).toHaveTextContent("--protocol 'chat'");
    expect(screen.getByLabelText("Header")).toHaveValue("{}");
  });

  it("preserves an explicitly saved WebSocket header for HTTP instead of rewriting it", async () => {
    const user = userEvent.setup();
    renderPreview({ route: route({ protocols: ["http", "websocket"], websocket_subprotocols: ["chat"], example_request: { method: "GET", subpath: "", query: "", headers: { "Sec-WebSocket-Protocol": "saved" }, body: "" } }) });

    await user.click(screen.getByRole("button", { name: "请求详情" }));
    expect(screen.getByLabelText("Header")).toHaveValue('{\n  "Sec-WebSocket-Protocol": "saved"\n}');
    expect(hooks.usePreview).toHaveBeenLastCalledWith(expect.objectContaining({ sample: expect.objectContaining({ headers: { "Sec-WebSocket-Protocol": "saved" } }) }), { cacheKey: "9:invocation:http:test-revision" });
  });

  it("resets the selected protocol when the same Route contract changes", () => {
    const view = renderPreview({ route: route({ protocols: ["http", "websocket"] }) });
    expect(screen.getByRole("radio", { name: "HTTP" })).toHaveAttribute("data-state", "on");

    view.rerender(<InlineRouteInvocationPreview origin="https://gateway.test" serviceSlug="weather" target={target} dependencyRevision="test-revision" route={route({ protocols: ["websocket"] })} />);
    expect(screen.queryByRole("radio", { name: "HTTP" })).not.toBeInTheDocument();
    expect(screen.getByText("协议: WebSocket")).toBeVisible();
    expect(screen.getByText(/websocat/)).toBeVisible();
  });

  it("gives method, subpath, query, and protocol controls 44px minimum touch targets", () => {
    renderPreview({ route: route({ protocols: ["http", "websocket"] }) });

    expect(screen.getByLabelText("方法")).toHaveClass("min-h-11");
    expect(screen.getByLabelText("子路径")).toHaveClass("min-h-11");
    expect(screen.getByLabelText("Query")).toHaveClass("min-h-11");
    expect(screen.getByRole("radio", { name: "HTTP" })).toHaveClass("min-h-11", "min-w-11");
    expect(screen.getByRole("radio", { name: "WebSocket" })).toHaveClass("min-h-11", "min-w-11");
  });

  it("keeps the inline preview single-column and bounds long request content locally", () => {
    const { container } = renderPreview();

    const preview = screen.getByRole("region", { name: "previewInvocation" });
    expect(preview).toHaveClass("grid", "grid-cols-1", "min-w-0", "max-w-full");
    expect(screen.getByTestId("segmented-url-text").parentElement?.parentElement).toHaveClass(
      "min-w-0",
      "overflow-x-auto",
    );
    expect(container.innerHTML).not.toMatch(/(?:bg|text|border)-(?:gray|slate|zinc|neutral|stone)-/);
  });

  it("shares every standard Route method with the Route Editor including TRACE", () => {
    renderPreview({ route: route({ allowed_methods: [], example_request: { method: "TRACE", subpath: "", query: "", headers: {}, body: "" } }) });

    expect(STANDARD_ROUTE_HTTP_METHODS).toContain("TRACE");
    expect(screen.getByLabelText("方法")).toHaveTextContent("TRACE");
  });

  it("shows diagnostics alongside endpoint URLs and announces loading", () => {
    hooks.preview = { data: { endpoints: [
      { upstream_id: 31, upstream_name: "Primary", status: 1, priority: 0, weight: 1, final_url: "https://weather.test/forecast/today?unit=c" },
      { upstream_id: 32, upstream_name: "Backup", status: 0, priority: 1, weight: 1, final_url: "https://backup.test/forecast/today?unit=c" },
    ], diagnostics: ["no_available_upstream"] }, isLoading: false, error: null, refetch: vi.fn() };
    renderPreview();
    expect(screen.getByRole("region", { name: "Primary" })).toBeVisible();
    expect(screen.getByRole("region", { name: "Backup" })).toBeVisible();
    expect(screen.getAllByTestId("segmented-url-text").map((item) => item.textContent)).toEqual([
      "https://gateway.test/v1/api/weather/forecast/today?unit=c",
      "https://weather.test/forecast/today?unit=c",
      "https://backup.test/forecast/today?unit=c",
    ]);
    expect(screen.getByText("路由诊断")).toBeVisible();

    hooks.preview = { data: undefined, isLoading: true, error: null, refetch: vi.fn() };
    const view = renderPreview({ route: route({ id: 10 }) });
    expect(screen.getAllByRole("status", { name: "正在加载预览" })).toHaveLength(1);
    view.unmount();
  });

  it("warns only when every preview endpoint is disabled", () => {
    hooks.preview = { data: { endpoints: [
      { upstream_id: 31, upstream_name: "Primary", status: 1, priority: 0, weight: 1, final_url: "https://weather.test/forecast" },
      { upstream_id: 32, upstream_name: "Backup", status: 0, priority: 1, weight: 1, final_url: "https://backup.test/forecast" },
    ], diagnostics: [] }, isLoading: false, error: null, refetch: vi.fn() };
    const view = renderPreview();
    expect(screen.queryByText("当前 Route 会返回 503")).not.toBeInTheDocument();

    hooks.preview = { data: { endpoints: [{ upstream_id: 32, upstream_name: "Backup", status: 0, priority: 1, weight: 1, final_url: "https://backup.test/forecast" }], diagnostics: ["no_available_upstream"] }, isLoading: false, error: null, refetch: vi.fn() };
    view.rerender(<InlineRouteInvocationPreview origin="https://gateway.test" serviceSlug="weather" route={route({ id: 10 })} target={target} dependencyRevision="test-revision" />);
    expect(screen.getByText("当前 Route 会返回 503")).toBeVisible();
  });

  it("shows a zero-endpoint diagnostic once in the empty state", () => {
    hooks.preview = { data: { endpoints: [], diagnostics: ["no_available_upstream"] }, isLoading: false, error: null, refetch: vi.fn() };
    renderPreview();

    expect(screen.getAllByText("noAvailableUpstream")).toHaveLength(1);
    expect(screen.queryByText("路由诊断")).not.toBeInTheDocument();
  });
});
