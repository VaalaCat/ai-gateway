import { QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { APIBackend, APIRoute } from "@/lib/api/api-services";
import { createTestQueryClient } from "@/test/render";

import { InlineRouteInvocationPreview } from "./_components/route-table/inline-route-invocation-preview";

const { apiPost } = vi.hoisted(() => ({ apiPost: vi.fn() }));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => ({
  method: "方法", subpath: "子路径", query: "Query", headers: "Header", body: "Body",
  requestDetails: "请求详情", retryPreview: "重试", copyCommand: "复制命令", clientRequest: "客户端请求",
  invocationProtocol: "协议", websocketProtocol: "WebSocket", websocketSubprotocols: "WebSocket 子协议", httpProtocol: "HTTP",
  routingPreviewFailed: "预览失败", routingPreviewFailedDescription: "预览失败后可以重试", endpointSummary: "转发结果",
  noEndpointCandidates: "没有 Endpoint", routingPreviewEmptyDescription: "没有候选 Endpoint", endpointDisabled: "Endpoint 已停用",
  endpointUnavailable503: "当前 Route 会返回 503", routingPreviewStaticOnlyDisabled: "Endpoint 都已停用", requestHeadersJSONInvalid: "Header JSON 无效",
  routingPreviewLoading: "正在加载预览", publicUrlCopied: "已复制客户端 URL", finalUrlCopied: "已复制 Endpoint URL",
  copyClientRequest: "复制客户端 URL", copyEndpointURL: "复制 Endpoint URL", copyCommandFailed: "复制命令失败",
}[key] ?? key) }));
vi.mock("@/lib/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api/client")>();
  return { ...actual, api: { ...actual.api, post: apiPost } };
});
vi.mock("@/lib/utils/clipboard", () => ({ copyTextWithFeedback: vi.fn().mockResolvedValue(true) }));

const target: APIBackend = { id: 17, api_service_id: 7, name: "Primary", route_count: 1, upstream_count: 0, enabled_upstream_count: 0, endpoint_hosts: [] };
const route: APIRoute = {
  id: 9, api_service_id: 7, backend_id: 17, slug: "forecast", protocols: ["http", "websocket"], allowed_methods: ["GET"], upstream_path: "/forecast", forward_subpath: true,
  example_request: { method: "GET", subpath: "", query: "", headers: {}, body: "" }, websocket_subprotocols: ["chat"], status: 1,
};

function renderPreview(previewRoute = route, dependencyRevision = "revision-a") {
  const client = createTestQueryClient();
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <InlineRouteInvocationPreview origin="https://gateway.test" serviceSlug="weather" route={previewRoute} target={target} dependencyRevision={dependencyRevision} />
      </QueryClientProvider>,
    ),
  };
}

describe("InlineRouteInvocationPreview protocol preview cache", () => {
  beforeEach(() => {
    apiPost.mockReset();
    apiPost.mockResolvedValue({ endpoints: [], diagnostics: [] });
  });

  it("keeps HTTP and WebSocket preview caches distinct while reusing the HTTP result on return", async () => {
    const user = userEvent.setup();
    const { client } = renderPreview();

    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(1));
    expect(apiPost).toHaveBeenNthCalledWith(1, "/admin/api-routes/preview", expect.objectContaining({ sample: expect.objectContaining({ headers: {} }) }), expect.objectContaining({ signal: expect.any(AbortSignal) }));

    await user.click(screen.getByRole("radio", { name: "WebSocket" }));
    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(2));
    expect(apiPost).toHaveBeenNthCalledWith(2, "/admin/api-routes/preview", expect.objectContaining({ sample: expect.objectContaining({ headers: { "Sec-WebSocket-Protocol": "chat" } }) }), expect.objectContaining({ signal: expect.any(AbortSignal) }));

    await user.click(screen.getByRole("radio", { name: "HTTP" }));
    await waitFor(() => expect(screen.getByRole("radio", { name: "HTTP" })).toHaveAttribute("data-state", "on"));
    expect(apiPost).toHaveBeenCalledTimes(2);
    expect(client.getQueryCache().findAll({ queryKey: ["api-route-preview", 7, "route"] }).map((query) => query.queryKey)).toEqual(expect.arrayContaining([
      ["api-route-preview", 7, "route", "9:invocation:http:revision-a"],
      ["api-route-preview", 7, "route", "9:invocation:websocket:revision-a"],
    ]));
  });

  it("uses the stable HTTP invocation cache for an HTTP-only Route", async () => {
    const { client } = renderPreview({ ...route, protocols: ["http"] });

    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(1));
    expect(client.getQueryCache().findAll({ queryKey: ["api-route-preview", 7, "route"] }).map((query) => query.queryKey)).toContainEqual([
      "api-route-preview", 7, "route", "9:invocation:http:revision-a",
    ]);
    expect(apiPost).toHaveBeenCalledWith("/admin/api-routes/preview", expect.objectContaining({ sample: expect.objectContaining({ headers: {} }) }), expect.objectContaining({ signal: expect.any(AbortSignal) }));
  });

  it("reuses equivalent dependencies inside the fresh window and refetches after the Route contract revision changes", async () => {
    apiPost
      .mockResolvedValueOnce({ endpoints: [{ upstream_id: 31, upstream_name: "Primary", status: 1, priority: 0, weight: 1, final_url: "https://old.example/forecast" }], diagnostics: [] })
      .mockResolvedValueOnce({ endpoints: [{ upstream_id: 31, upstream_name: "Primary", status: 1, priority: 0, weight: 1, final_url: "https://new.example/v2/forecast" }], diagnostics: [] });
    const view = renderPreview(route, "route-revision-1");

    await waitFor(() => expect(screen.getAllByTestId("segmented-url-text").some((item) => item.textContent === "https://old.example/forecast")).toBe(true));
    view.rerender(<QueryClientProvider client={view.client}><InlineRouteInvocationPreview origin="https://gateway.test" serviceSlug="weather" route={{ ...route }} target={{ ...target }} dependencyRevision="route-revision-1" /></QueryClientProvider>);
    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(1));

    view.rerender(<QueryClientProvider client={view.client}><InlineRouteInvocationPreview origin="https://gateway.test" serviceSlug="weather" route={{ ...route, upstream_path: "/v2/forecast" }} target={target} dependencyRevision="route-revision-2" /></QueryClientProvider>);
    await waitFor(() => expect(screen.getAllByTestId("segmented-url-text").some((item) => item.textContent === "https://new.example/v2/forecast")).toBe(true));
    expect(apiPost).toHaveBeenCalledTimes(2);
  });

  it("refetches inside the fresh window when Target or Endpoint dependencies change", async () => {
    apiPost
      .mockResolvedValueOnce({ endpoints: [{ upstream_id: 31, upstream_name: "Primary", status: 1, priority: 0, weight: 1, final_url: "https://primary.example/forecast" }], diagnostics: [] })
      .mockResolvedValueOnce({ endpoints: [{ upstream_id: 32, upstream_name: "Backup", status: 1, priority: 1, weight: 1, final_url: "https://backup.example/forecast" }], diagnostics: [] });
    const view = renderPreview(route, "topology-revision-1");
    await waitFor(() => expect(screen.getAllByTestId("segmented-url-text").some((item) => item.textContent === "https://primary.example/forecast")).toBe(true));

    view.rerender(<QueryClientProvider client={view.client}><InlineRouteInvocationPreview origin="https://gateway.test" serviceSlug="weather" route={route} target={{ ...target, name: "Failover" }} dependencyRevision="topology-revision-2" /></QueryClientProvider>);
    await waitFor(() => expect(screen.getAllByTestId("segmented-url-text").some((item) => item.textContent === "https://backup.example/forecast")).toBe(true));
    expect(apiPost).toHaveBeenCalledTimes(2);
  });
});
