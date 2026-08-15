import { useState } from "react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { APIBackend, APIRoute, APIRoutePreview, APIService, APIUpstream } from "@/lib/api/api-services";
import { routePreviewDependencyRevision } from "@/lib/route-preview-dependency-revision";

import { RouteDataTable } from "./_components/route-table/route-data-table";
import { previewDraftForConfiguredRoute, RouteExpandedWorkspace } from "./_components/route-table/route-expanded-workspace";

const hooks = vi.hoisted(() => ({
  backend: vi.fn(),
  endpoints: vi.fn(),
  preview: vi.fn(),
}));

const translations: Record<string, string> = {
  addEndpoint: "添加 Endpoint",
  configuredEndpointPreviewFailed: "无法加载配置 URL",
  configuredEndpointPreviewFailedDescription: "Endpoint 实体仍可管理；这里只重试只读 Preview。",
  configuredEndpointURLLoading: "正在加载 Endpoint 配置 URL",
  copyEndpointConfig: "复制配置",
  cancel: "取消",
  confirmDelete: "删除",
  confirmDeleteDescription: "此操作会永久删除 Endpoint。",
  confirmDeleteEndpoint: "删除 Endpoint",
  confirmDeleteLastEndpointTitle: "删除最后一个启用的 Endpoint？",
  confirmDeleteTitle: "确认删除？",
  deleteUpstream: "删除 Endpoint",
  deleteTarget: "删除 Target",
  editRoute: "编辑 Route",
  editTarget: "编辑 Target",
  editUpstream: "编辑 Endpoint",
  endpointsLoadFailed: "Endpoint 加载失败",
  endpointsLoadFailedDescription: "Target 与 Route 仍可管理；这里只重试 Endpoint 列表。",
  endpointsLoading: "正在加载 Endpoint",
  noEndpoints: "还没有 Endpoint",
  noEndpointsDescription: "添加 Endpoint 后，此 Route 才能转发请求。",
  retry: "重试",
  retryPreview: "重试 Preview",
  previewInvocation: "预览调用",
  collapseInvocationPreview: "收起预览调用",
  method: "方法",
  subpath: "子路径",
  query: "Query",
  headers: "Header",
  body: "Body",
  requestDetails: "请求详情",
  invocationProtocol: "协议",
  websocketProtocol: "WebSocket",
  clientRequest: "客户端请求",
  endpointSummary: "转发结果",
  endpointSection: "Endpoint",
  routingPreviewLoading: "正在加载预览",
  routingPreviewFailed: "预览失败",
  routingPreviewFailedDescription: "预览失败后可以重试",
  noEndpointCandidates: "没有 Endpoint",
  routingPreviewEmptyDescription: "没有候选 Endpoint",
  requestHeadersJSONInvalid: "Header JSON 无效",
  copyClientRequest: "复制客户端 URL",
  publicUrlCopied: "已复制客户端 URL",
  templateCommandCopied: "已复制命令",
  copyCommandFailed: "复制命令失败",
  routeTargetMissing: "Route 引用的 Target 不存在",
  routeTargetMissingDescription: "仍可编辑 Route 并重新选择 Target。",
  targetLoadFailed: "Target 加载失败",
  targetLoadFailedDescription: "Route 仍可管理；这里只重试 Target。",
  targetLoading: "正在加载 Target",
  targetReturns503: "当前 Route 会返回 503",
  targetReturns503Description: "没有已启用的 Endpoint 可以接收请求。",
  mutationFailed: "保存失败",
  lastEndpointDeleteDescription: "1 条 Route 将返回 503，直到启用其他 Endpoint。",
};

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, unknown>) => {
    if (key === "endpointCount") return `${String(values?.count)} 个 Endpoint`;
    if (key === "confirmDeleteDescription") return `此操作会永久删除 ${String(values?.subject)}。`;
    if (key === "lastEndpointDeleteDescription") return `${String(values?.count)} 条 Route 将返回 503，直到启用其他 Endpoint。`;
    return translations[key] ?? key;
  },
}));

vi.mock("@/lib/api/api-services", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/api-services")>()),
  useAPIBackend: hooks.backend,
  useAPIUpstreams: hooks.endpoints,
  useAllAPIUpstreams: hooks.endpoints,
  useAPIRoutePreview: hooks.preview,
}));

const service: APIService = {
  id: 7,
  slug: "weather",
  name: "Weather API",
  description: "Weather data",
  price_per_call: 1,
  status: 1,
};

const route: APIRoute = {
  id: 9,
  api_service_id: 7,
  backend_id: 17,
  slug: "forecast",
  protocols: ["http"],
  allowed_methods: ["GET"],
  upstream_path: "/forecast",
  forward_subpath: true,
  example_request: {
    method: "POST",
    subpath: "/saved-example",
    query: "secret=sample",
    headers: { "X-Sample": "saved" },
    body: "saved body",
  },
  status: 1,
};

const target: APIBackend = {
  id: 17,
  api_service_id: 7,
  name: "Primary Target",
  route_count: 1,
  upstream_count: 2,
  enabled_upstream_count: 1,
  endpoint_hosts: ["api.weather.test", "backup.weather.test"],
};

function endpoint(overrides: Partial<APIUpstream> = {}): APIUpstream {
  return {
    id: 31,
    backend_id: 17,
    name: "Primary Endpoint",
    base_url: "https://api.weather.test",
    weight: 1,
    priority: 0,
    auth_type: "none",
    status: 1,
    credential_configured: false,
    proxy_url_configured: false,
    ...overrides,
  };
}

function defaultEndpoints() {
  return [
    endpoint(),
    endpoint({ id: 32, name: "Backup Endpoint", base_url: "https://backup.weather.test", status: 0, priority: 1 }),
  ];
}

function configuredCacheKey(currentTarget = target, currentEndpoints = defaultEndpoints()) {
  return `${route.id}:configured:${routePreviewDependencyRevision(route, currentTarget, currentEndpoints)}`;
}

function previewFor(endpoints: APIRoutePreview["endpoints"]): APIRoutePreview {
  return { endpoints, diagnostics: [] };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((success, failure) => { resolve = success; reject = failure; });
  return { promise, resolve, reject };
}

function renderExpanded(overrides: { canManage?: boolean; onDeleteEndpoint?: (endpoint: APIUpstream) => Promise<void> | void } = {}) {
  return render(
    <RouteExpandedWorkspace
      service={service}
      origin="https://gateway.test"
      route={route}
      canManage={overrides.canManage ?? true}
      onDeleteTarget={vi.fn()}
      onDeleteEndpoint={overrides.onDeleteEndpoint ?? vi.fn()}
    />,
  );
}

function endpointTableRow(id: number) {
  return screen.getByTestId(`endpoint-row-${id}`).closest("tr")!;
}

describe("RouteExpandedWorkspace", () => {
  beforeEach(() => {
    hooks.backend.mockReset();
    hooks.endpoints.mockReset();
    hooks.preview.mockReset();
    hooks.backend.mockReturnValue({ data: target, isLoading: false, error: null, refetch: vi.fn() });
    hooks.endpoints.mockReturnValue({ data: defaultEndpoints(), isLoading: false, error: null, refetch: vi.fn() });
    hooks.preview.mockReturnValue({
      data: previewFor([
        { upstream_id: 31, upstream_name: "Primary Endpoint", status: 1, priority: 0, weight: 1, final_url: "https://api.weather.test/forecast" },
        { upstream_id: 32, upstream_name: "Backup Endpoint", status: 0, priority: 1, weight: 1, final_url: "https://backup.weather.test/forecast" },
      ]),
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
  });

  it("falls back to GET while keeping every concrete request field empty", () => {
    expect(previewDraftForConfiguredRoute({
      ...route,
      example_request: { method: "", subpath: "/saved", query: "saved=1", headers: { Saved: "yes" }, body: "saved" },
    }).sample).toEqual({ method: "GET", subpath: "", query: "", headers: {}, body: "" });
  });

  it("opens the invocation preview inline and returns focus to its trigger after collapsing", async () => {
    const user = userEvent.setup();
    renderExpanded();

    const trigger = screen.getByRole("button", { name: "预览调用" });
    expect(trigger).toHaveClass("h-8", "max-sm:min-h-11");
    await user.click(trigger);
    expect(screen.getByLabelText("方法")).toBeVisible();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    const collapse = screen.getByRole("button", { name: "收起预览调用" });
    expect(collapse).toHaveClass("h-8", "max-sm:min-h-11");
    await user.click(collapse);
    expect(screen.getByRole("button", { name: "预览调用" })).toHaveFocus();
  });

  it("keeps one single-column workspace with a dedicated public URL and one Endpoint table scroller", () => {
    renderExpanded();

    const workspaces = screen.getAllByTestId("route-expanded-workspace-9");
    expect(workspaces).toHaveLength(1);
    expect(workspaces[0]).toHaveClass("grid", "grid-cols-1", "min-w-0", "max-w-full");

    expect(screen.getAllByTestId("segmented-url-text")).toHaveLength(3);
    expect(workspaces[0]!.querySelectorAll('[data-slot="table-container"]')).toHaveLength(1);
    for (const copy of screen.getAllByRole("button", { name: "copyEndpointURL" })) {
      expect(copy).toHaveClass("size-8", "max-sm:min-h-11", "max-sm:min-w-11");
    }
  });

  it("shows the Target and every configured Endpoint URL without saved example data", async () => {
    renderExpanded();

    expect(await screen.findByText("Primary Target")).toBeVisible();
    expect(screen.getAllByTestId("segmented-url-text").map((item) => item.textContent)).toEqual([
      "https://gateway.test/v1/api/weather/forecast/…",
      "https://api.weather.test/forecast/…",
      "https://backup.weather.test/forecast/…",
    ]);
    expect(hooks.endpoints).toHaveBeenCalledWith(
      17,
      { enabled: true },
    );
    expect(hooks.preview).toHaveBeenCalledWith(expect.objectContaining({
      sample: { method: "POST", subpath: "", query: "", headers: {}, body: "" },
      target: { mode: "existing", backend_id: 17 },
    }), { enabled: true, cacheKey: configuredCacheKey() });
    expect(JSON.stringify(hooks.preview.mock.calls[0]?.[0])).not.toContain("saved-example");
    expect(JSON.stringify(hooks.preview.mock.calls[0]?.[0])).not.toContain("secret=sample");
  });

  it("keeps Route management available and disables child queries when the Target is missing", () => {
    hooks.backend.mockReturnValue({ data: undefined, isLoading: false, error: { status: 404 }, refetch: vi.fn() });
    renderExpanded();

    expect(screen.getByText("Route 引用的 Target 不存在")).toBeVisible();
    expect(screen.getByRole("link", { name: "编辑 Route" })).toHaveAttribute(
      "href",
      "/api-services/routes/edit?id=9&service_id=7",
    );
    expect(hooks.endpoints).toHaveBeenCalledWith(
      0,
      { enabled: false },
    );
    expect(hooks.preview).toHaveBeenCalledWith(expect.any(Object), { enabled: false, cacheKey: `${route.id}:configured:unavailable` });
  });

  it("retries only the failed Endpoint query and keeps Target management", async () => {
    const refetch = vi.fn();
    hooks.endpoints.mockReturnValue({ data: undefined, isLoading: false, error: new Error("offline"), refetch });
    const user = userEvent.setup();
    renderExpanded();

    expect(screen.getByText("Endpoint 加载失败")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "重试" }));
    expect(refetch).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "targetActions" })).toBeVisible();
  });

  it("shows an empty Endpoint state and the single add action at the list end", () => {
    hooks.endpoints.mockReturnValue({ data: [], isLoading: false, error: null, refetch: vi.fn() });
    hooks.preview.mockReturnValue({ data: previewFor([]), isLoading: false, error: null, refetch: vi.fn() });
    renderExpanded();

    expect(screen.getByText("还没有 Endpoint")).toBeVisible();
    expect(screen.getByText("当前 Route 会返回 503")).toBeVisible();
    const links = screen.getAllByRole("link", { name: "添加 Endpoint" });
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveAttribute(
      "href",
      "/api-services/upstreams/new?service_id=7&backend_id=17&route_id=9&route_slug=forecast",
    );
  });

  it("warns when no enabled Endpoint can receive the request", () => {
    hooks.endpoints.mockReturnValue({
      data: [endpoint({ id: 32, name: "Backup Endpoint", base_url: "https://backup.weather.test", status: 0 })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
    renderExpanded();

    expect(screen.getByText("当前 Route 会返回 503")).toBeVisible();
    expect(screen.getByRole("link", { name: "添加 Endpoint" })).toBeVisible();
  });

  it("loads all 101 Endpoints and does not warn when only the final Endpoint is enabled", () => {
    const endpoints = Array.from({ length: 101 }, (_, index) => endpoint({
      id: index + 1,
      name: `Endpoint ${index + 1}`,
      base_url: `https://edge-${index + 1}.test`,
      status: index === 100 ? 1 : 0,
    }));
    hooks.endpoints.mockReturnValue({ data: endpoints, isLoading: false, error: null, refetch: vi.fn() });
    hooks.preview.mockReturnValue({ data: previewFor([]), isLoading: false, error: null, refetch: vi.fn() });
    renderExpanded();

    expect(screen.getAllByTestId(/^endpoint-row-/)).toHaveLength(101);
    expect(screen.getByText("Endpoint 101")).toBeVisible();
    expect(screen.queryByText("当前 Route 会返回 503")).not.toBeInTheDocument();
  });

  it("warns when all 101 Endpoints are disabled", () => {
    hooks.endpoints.mockReturnValue({
      data: Array.from({ length: 101 }, (_, index) => endpoint({
        id: index + 1,
        name: `Disabled Endpoint ${index + 1}`,
        status: 0,
      })),
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
    hooks.preview.mockReturnValue({ data: previewFor([]), isLoading: false, error: null, refetch: vi.fn() });
    renderExpanded();

    expect(screen.getAllByTestId(/^endpoint-row-/)).toHaveLength(101);
    expect(screen.getByText("当前 Route 会返回 503")).toBeVisible();
  });

  it("carries an encoded Route context through every Target and Endpoint management link", async () => {
    const contextualRoute = { ...route, slug: "daily-report-9" };
    const user = userEvent.setup();
    render(
      <RouteExpandedWorkspace
        service={service}
        origin="https://gateway.test"
        route={contextualRoute}
        canManage
        onDeleteTarget={vi.fn()}
        onDeleteEndpoint={vi.fn()}
      />,
    );
    const context = `route_id=9&route_slug=${encodeURIComponent(contextualRoute.slug)}`;

    await user.click(screen.getByRole("button", { name: "targetActions" }));
    expect(screen.getByRole("menuitem", { name: "编辑 Target" })).toHaveAttribute(
      "href",
      `/api-services/backends/edit?id=17&service_id=7&${context}`,
    );
    await user.keyboard("{Escape}");

    await user.click(within(endpointTableRow(31)).getByRole("button", { name: "endpointActions" }));
    expect(screen.getByRole("menuitem", { name: "编辑 Endpoint" })).toHaveAttribute(
      "href",
      `/api-services/upstreams/edit?id=31&service_id=7&${context}`,
    );
    expect(screen.getByRole("menuitem", { name: "复制配置" })).toHaveAttribute(
      "href",
      `/api-services/upstreams/new?service_id=7&copy_id=31&${context}`,
    );
    await user.keyboard("{Escape}");
    expect(screen.getByRole("link", { name: "添加 Endpoint" })).toHaveAttribute(
      "href",
      `/api-services/upstreams/new?service_id=7&backend_id=17&${context}`,
    );
  });

  it("opens Target and Endpoint portals with explicit mobile touch targets", async () => {
    const user = userEvent.setup();
    renderExpanded();
    const workspace = screen.getByTestId("route-expanded-workspace-9");

    await user.click(screen.getByRole("button", { name: "targetActions" }));
    for (const item of screen.getAllByRole("menuitem")) {
      expect(workspace).not.toContainElement(item);
      expect(item).toHaveClass("max-sm:min-h-11");
    }
    expect(document.querySelector('[data-slot="dropdown-menu-content"]')).toHaveClass("max-sm:min-w-44");
    await user.keyboard("{Escape}");

    await user.click(within(endpointTableRow(31)).getByRole("button", { name: "endpointActions" }));
    for (const item of screen.getAllByRole("menuitem")) {
      expect(workspace).not.toContainElement(item);
      expect(item).toHaveClass("max-sm:min-h-11");
    }
    expect(document.querySelector('[data-slot="dropdown-menu-content"]')).toHaveClass("max-sm:min-w-44");
  });

  it("announces Target Endpoint and configured URL loading with localized live statuses", () => {
    hooks.backend.mockReturnValue({ data: undefined, isLoading: true, error: null, refetch: vi.fn() });
    const view = renderExpanded();
    expect(screen.getByRole("status", { name: "正在加载 Target" })).toHaveAttribute("aria-live", "polite");

    hooks.backend.mockReturnValue({ data: target, isLoading: false, error: null, refetch: vi.fn() });
    hooks.endpoints.mockReturnValue({ data: undefined, isLoading: true, error: null, refetch: vi.fn() });
    view.rerender(
      <RouteExpandedWorkspace service={service} origin="https://gateway.test" route={route} canManage onDeleteTarget={vi.fn()} onDeleteEndpoint={vi.fn()} />,
    );
    expect(screen.getByRole("status", { name: "正在加载 Endpoint" })).toHaveAttribute("aria-live", "polite");

    hooks.endpoints.mockReturnValue({ data: [endpoint()], isLoading: false, error: null, refetch: vi.fn() });
    hooks.preview.mockReturnValue({ data: undefined, isLoading: true, error: null, refetch: vi.fn() });
    view.rerender(
      <RouteExpandedWorkspace service={service} origin="https://gateway.test" route={route} canManage onDeleteTarget={vi.fn()} onDeleteEndpoint={vi.fn()} />,
    );
    expect(screen.getByRole("status", { name: "正在加载 Endpoint 配置 URL" })).toHaveAttribute("aria-live", "polite");
  });

  it("keeps Endpoint entity actions visible when Preview fails and retries only Preview", async () => {
    const refetch = vi.fn();
    hooks.preview.mockReturnValue({ data: undefined, isLoading: false, error: new Error("preview offline"), refetch });
    const user = userEvent.setup();
    renderExpanded();

    expect(screen.getByText("无法加载配置 URL")).toBeVisible();
    expect(screen.getByText("Primary Endpoint")).toBeVisible();
    expect(screen.queryByText("https://api.weather.test")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重试 Preview" }));
    expect(refetch).toHaveBeenCalledTimes(1);
    await user.click(within(endpointTableRow(31)).getByRole("button", { name: "endpointActions" }));
    expect(screen.getByRole("menuitem", { name: "编辑 Endpoint" })).toBeVisible();
    expect(screen.getByRole("menuitem", { name: "复制配置" })).toBeVisible();
  });

  it("confirms one Endpoint identity and closes after async deletion succeeds", async () => {
    const remove = vi.fn().mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderExpanded({ onDeleteEndpoint: remove });

    await user.click(within(endpointTableRow(32)).getByRole("button", { name: "endpointActions" }));
    await user.click(screen.getByRole("menuitem", { name: "删除 Endpoint" }));
    expect(screen.getByRole("alertdialog")).toHaveTextContent("Backup Endpoint");

    await user.click(screen.getByRole("button", { name: "删除" }));

    expect(remove).toHaveBeenCalledTimes(1);
    expect(remove).toHaveBeenCalledWith(expect.objectContaining({ id: 32, name: "Backup Endpoint" }));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("restores focus to the stable Endpoint heading after the deleted row disappears", async () => {
    const pending = deferred<void>();
    const remove = vi.fn().mockReturnValue(pending.promise);
    const user = userEvent.setup();
    const view = renderExpanded({ onDeleteEndpoint: remove });
    await user.click(within(endpointTableRow(32)).getByRole("button", { name: "endpointActions" }));
    await user.click(screen.getByRole("menuitem", { name: "删除 Endpoint" }));
    await user.click(screen.getByRole("button", { name: "删除" }));

    hooks.endpoints.mockReturnValue({ data: [endpoint()], isLoading: false, error: null, refetch: vi.fn() });
    view.rerender(<RouteExpandedWorkspace service={service} origin="https://gateway.test" route={route} canManage onDeleteTarget={vi.fn()} onDeleteEndpoint={remove} />);
    expect(screen.queryByTestId("endpoint-row-32")).not.toBeInTheDocument();
    pending.resolve();

    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    await waitFor(() => expect(screen.getByRole("heading", { name: "Endpoint" })).toHaveFocus());
  });

  it("keeps focus and dialog in place when deferred Endpoint deletion rejects", async () => {
    const pending = deferred<void>();
    const user = userEvent.setup();
    renderExpanded({ onDeleteEndpoint: vi.fn().mockReturnValue(pending.promise) });
    await user.click(within(endpointTableRow(32)).getByRole("button", { name: "endpointActions" }));
    await user.click(screen.getByRole("menuitem", { name: "删除 Endpoint" }));
    await user.click(screen.getByRole("button", { name: "删除" }));
    pending.reject(new Error("deferred delete rejected"));

    expect(await screen.findByRole("alertdialog")).toHaveTextContent("deferred delete rejected");
    expect(screen.getByText("Endpoint")).not.toHaveFocus();
  });

  it("restores focus after deleting the final enabled Endpoint", async () => {
    const pending = deferred<void>();
    const remove = vi.fn().mockReturnValue(pending.promise);
    const user = userEvent.setup();
    const view = renderExpanded({ onDeleteEndpoint: remove });
    await user.click(within(endpointTableRow(31)).getByRole("button", { name: "endpointActions" }));
    await user.click(screen.getByRole("menuitem", { name: "删除 Endpoint" }));
    await user.click(screen.getByRole("button", { name: "删除 Endpoint" }));
    hooks.endpoints.mockReturnValue({ data: [endpoint({ id: 32, name: "Backup Endpoint", status: 0 })], isLoading: false, error: null, refetch: vi.fn() });
    view.rerender(<RouteExpandedWorkspace service={service} origin="https://gateway.test" route={route} canManage onDeleteTarget={vi.fn()} onDeleteEndpoint={remove} />);
    pending.resolve();

    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    await waitFor(() => expect(screen.getByRole("heading", { name: "Endpoint" })).toHaveFocus());
  });

  it("keeps the deletion close-and-focus path mounted when the final Endpoint row disappears", async () => {
    hooks.endpoints.mockReturnValue({ data: [endpoint()], isLoading: false, error: null, refetch: vi.fn() });
    const pending = deferred<void>();
    const remove = vi.fn().mockReturnValue(pending.promise);
    const user = userEvent.setup();
    const view = renderExpanded({ onDeleteEndpoint: remove });
    await user.click(within(endpointTableRow(31)).getByRole("button", { name: "endpointActions" }));
    await user.click(screen.getByRole("menuitem", { name: "删除 Endpoint" }));
    await user.click(screen.getByRole("button", { name: "删除 Endpoint" }));
    hooks.endpoints.mockReturnValue({ data: [], isLoading: false, error: null, refetch: vi.fn() });
    view.rerender(<RouteExpandedWorkspace service={service} origin="https://gateway.test" route={route} canManage onDeleteTarget={vi.fn()} onDeleteEndpoint={remove} />);
    expect(screen.queryByTestId("endpoint-row-31")).not.toBeInTheDocument();
    expect(screen.getByText("还没有 Endpoint")).toBeVisible();
    pending.resolve();

    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    await waitFor(() => expect(screen.getByRole("heading", { name: "Endpoint" })).toHaveFocus());
  });

  it("warns and uses the Endpoint-specific action before deleting the final enabled Endpoint", async () => {
    const user = userEvent.setup();
    renderExpanded();

    await user.click(within(endpointTableRow(31)).getByRole("button", { name: "endpointActions" }));
    await user.click(screen.getByRole("menuitem", { name: "删除 Endpoint" }));

    expect(screen.getByRole("alertdialog")).toHaveTextContent("删除最后一个启用的 Endpoint？");
    expect(screen.getByRole("alertdialog")).toHaveTextContent("1 条 Route 将返回 503");
    expect(screen.getByRole("button", { name: "删除 Endpoint" })).toBeVisible();
  });

  it("uses the ordinary confirmation when Target metadata reports another enabled Endpoint", async () => {
    hooks.backend.mockReturnValue({
      data: { ...target, enabled_upstream_count: 2 },
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
    const user = userEvent.setup();
    renderExpanded();

    await user.click(within(endpointTableRow(31)).getByRole("button", { name: "endpointActions" }));
    await user.click(screen.getByRole("menuitem", { name: "删除 Endpoint" }));

    expect(screen.getByRole("alertdialog")).toHaveTextContent("确认删除？");
    expect(screen.getByRole("alertdialog")).toHaveTextContent("Primary Endpoint");
    expect(screen.getByRole("button", { name: "删除" })).toBeVisible();
  });

  it("keeps the Endpoint confirmation and visible error when async deletion rejects", async () => {
    const remove = vi.fn().mockRejectedValue(new Error("delete rejected"));
    const user = userEvent.setup();
    renderExpanded({ onDeleteEndpoint: remove });

    await user.click(within(endpointTableRow(32)).getByRole("button", { name: "endpointActions" }));
    await user.click(screen.getByRole("menuitem", { name: "删除 Endpoint" }));
    await user.click(screen.getByRole("button", { name: "删除" }));

    expect(await screen.findByText("delete rejected")).toBeVisible();
    expect(screen.getByRole("alertdialog")).toBeVisible();
    expect(remove).toHaveBeenCalledTimes(1);
  });

  it("disables confirmation controls and prevents duplicate Endpoint deletion while pending", async () => {
    let resolveDelete: (() => void) | undefined;
    const pending = new Promise<void>((resolve) => { resolveDelete = resolve; });
    const remove = vi.fn().mockReturnValue(pending);
    const user = userEvent.setup();
    renderExpanded({ onDeleteEndpoint: remove });

    await user.click(within(endpointTableRow(32)).getByRole("button", { name: "endpointActions" }));
    await user.click(screen.getByRole("menuitem", { name: "删除 Endpoint" }));
    const confirm = screen.getByRole("button", { name: "删除" });
    await user.click(confirm);

    expect(confirm).toBeDisabled();
    expect(screen.getByRole("button", { name: "取消" })).toBeDisabled();
    await user.keyboard("{Escape}");
    expect(screen.getByRole("alertdialog")).toBeVisible();
    await user.click(confirm);
    expect(remove).toHaveBeenCalledTimes(1);

    resolveDelete?.();
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
  });

  it("hides every mutation entry without manage permission", () => {
    renderExpanded({ canManage: false });

    expect(screen.queryByRole("link", { name: "编辑 Route" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "添加 Endpoint" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "targetActions" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "endpointActions" })).not.toBeInTheDocument();
  });

  it("retries the Target query without enabling child queries after a load failure", async () => {
    const refetch = vi.fn();
    hooks.backend.mockReturnValue({ data: undefined, isLoading: false, error: new Error("offline"), refetch });
    const user = userEvent.setup();
    renderExpanded();

    expect(screen.getByText("Target 加载失败")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "重试" }));
    expect(refetch).toHaveBeenCalledTimes(1);
    expect(hooks.endpoints).toHaveBeenCalledWith(0, { enabled: false });
    expect(hooks.preview).toHaveBeenCalledWith(expect.any(Object), { enabled: false, cacheKey: `${route.id}:configured:unavailable` });
  });

  it("uses the real Route table chevron to expand inline without hover Preview, Popover, or Dialog", async () => {
    const user = userEvent.setup();
    const renderWorkspace = (item: APIRoute) => (
      <RouteExpandedWorkspace
        service={service}
        origin="https://gateway.test"
        route={item}
        canManage
        onDeleteTarget={vi.fn()}
        onDeleteEndpoint={vi.fn()}
      />
    );
    function ControlledTable() {
      const [expandedRouteID, setExpandedRouteID] = useState<number>();
      return (
      <RouteDataTable
        service={service}
        origin="https://gateway.test"
        routes={[route]}
        loading={false}
        refreshing={false}
        error={null}
        onRetry={vi.fn()}
        total={1}
        displayedPage={1}
        displayedPageSize={10}
        requestedPage={1}
        isPlaceholderData={false}
        filters={{ search: "" }}
        expandedRouteID={expandedRouteID}
        canManage
        onFiltersChange={vi.fn()}
        onExpandedRouteChange={setExpandedRouteID}
        onPaginationChange={vi.fn()}
        onDeleteRoute={vi.fn()}
        renderExpandedRoute={renderWorkspace}
      />
      );
    }
    render(<ControlledTable />);

    expect(hooks.endpoints).not.toHaveBeenCalled();
    expect(hooks.preview).not.toHaveBeenCalled();

    await user.hover(screen.getByTitle("/v1/api/weather/forecast/…"));

    expect(hooks.preview).not.toHaveBeenCalled();
    expect(document.querySelector('[data-slot="popover-content"]')).toBeNull();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "expandRoute" }));

    expect(hooks.endpoints).toHaveBeenCalledWith(
      target.id,
      { enabled: true },
    );
    expect(hooks.preview).toHaveBeenCalledWith(expect.any(Object), { enabled: true, cacheKey: configuredCacheKey() });
    expect(screen.getByTestId("endpoint-row-31")).toBeVisible();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});
