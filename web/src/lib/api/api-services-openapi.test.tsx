import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }));

vi.mock("./client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./client")>()),
  api,
  buildQuery: () => "",
}));

import { ApiError } from "./client";
import { useGetOpenAPIDocument, useImportOpenAPI, useOpenAPIPreview, useUpdateOpenAPIDocument, type OpenAPIImportInput, type OpenAPIUpdateRequest } from "./api-services";

function setup() {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  return { client, wrapper };
}

const document = { openapi: "3.1.0", info: { title: "Weather", version: "1" }, paths: {} };

describe("OpenAPI API service hooks", () => {
  beforeEach(() => {
    api.get.mockReset();
    api.post.mockReset();
    api.put.mockReset();
  });

  it("reads the versioned OpenAPI snapshot from the dedicated endpoint", async () => {
    api.get.mockResolvedValue({ service: { id: 7, slug: "weather", name: "Weather", description: "", updated_at: 1, document: {} }, routes: [] });
    const { wrapper } = setup();
    const { result } = renderHook(() => useGetOpenAPIDocument(7), { wrapper });
    let fetched: Awaited<ReturnType<typeof result.current.refetch>> | undefined;
    await act(async () => { fetched = await result.current.refetch(); });
    expect(api.get).toHaveBeenCalledWith("/admin/api-services/7/openapi");
    expect(fetched?.data?.routes).toEqual([]);
  });

  it.each([
    { routes: null },
    { routes: undefined },
    { routes: [{ id: 9, slug: "root", upstream_path: "", updated_at: 2, paths: null }] },
    { routes: [{ id: 9, slug: "root", upstream_path: "", updated_at: 2, paths: undefined }] },
  ])("normalizes nullable GET route collections at the wire boundary: %#", async ({ routes }) => {
    api.get.mockResolvedValue({ service: { id: 7, slug: "weather", name: "Weather", description: "", updated_at: 1, document: {} }, routes, export: {} });
    const { wrapper } = setup();
    const { result } = renderHook(() => useGetOpenAPIDocument(7), { wrapper });
    let fetched: Awaited<ReturnType<typeof result.current.refetch>> | undefined;
    await act(async () => { fetched = await result.current.refetch(); });
    expect(fetched?.data?.routes).toEqual(routes?.length ? [{ ...routes[0], paths: {} }] : []);
  });

  it.each([null, undefined, [], "invalid"])("rejects a non-object service document at the GET boundary: %j", async (invalidDocument) => {
    api.get.mockResolvedValue({ service: { id: 7, slug: "weather", name: "Weather", description: "", updated_at: 1, document: invalidDocument }, routes: [] });
    const { wrapper } = setup();
    const { result } = renderHook(() => useGetOpenAPIDocument(7), { wrapper });
    let fetched: Awaited<ReturnType<typeof result.current.refetch>> | undefined;
    await act(async () => { fetched = await result.current.refetch(); });
    expect(fetched?.error).toEqual(expect.objectContaining({ message: "invalid OpenAPI document response" }));
  });

  it("puts only the dedicated versioned update request and invalidates the fresh document snapshot", async () => {
    const request: OpenAPIUpdateRequest = {
      service: { id: 7, updated_at: 11, document: { openapi: "3.1.0" } },
      routes: [{ id: 9, updated_at: 12, paths: { "/weather": { operations: { GET: { responses: { 200: { description: "OK" } } } } } } }],
    };
    api.put.mockResolvedValueOnce({ service: request.service, routes: request.routes });
    const { client, wrapper } = setup();
    const invalidate = vi.spyOn(client, "invalidateQueries").mockResolvedValue(undefined);
    const { result } = renderHook(() => useUpdateOpenAPIDocument(7), { wrapper });
    await act(async () => { await result.current.mutateAsync(request); });
    expect(api.put).toHaveBeenCalledWith("/admin/api-services/7/openapi", request);
    expect(invalidate.mock.calls.map(([options]) => options?.queryKey)).toContainEqual(["api-service-openapi", 7]);
  });

  it("posts the preview document and normalizes nullable wire collections once at the API boundary", async () => {
    api.post.mockResolvedValueOnce({
      service: { slug: "weather", name: "Weather", description: "" },
      servers: null,
      routes: [{ slug: "", display_name: "根路由", upstream_path: "", allowed_methods: null, paths: undefined, public_paths: null, path_count: 0, operation_count: 0 }],
      problems: null,
    });
    const { wrapper } = setup();
    const { result } = renderHook(() => useOpenAPIPreview(), { wrapper });

    let response: unknown;
    await act(async () => { response = await result.current.mutateAsync(document); });

    expect(api.post).toHaveBeenCalledWith("/admin/api-services/openapi/preview", { document });
    expect(response).toEqual({
      service: { slug: "weather", name: "Weather", description: "" },
      servers: [],
      routes: [{ slug: "", display_name: "根路由", upstream_path: "", allowed_methods: [], paths: [], public_paths: {}, path_count: 0, operation_count: 0 }],
      problems: [],
    });
  });

  it("posts the exact import command and invalidates exactly the four resource list families", async () => {
    const input: OpenAPIImportInput = {
      document,
      slug: "weather",
      choices: [],
      selected_server: 0,
      backend_name: "Weather Target",
      upstream: { name: "Weather Upstream", weight: 1, priority: 0, auth_type: "none" },
      price_per_call: 0,
    };
    const created = { service_id: 42, backend_id: 43, upstream_id: 44, route_ids: [45] };
    api.post.mockResolvedValueOnce(created);
    const { client, wrapper } = setup();
    const invalidate = vi.spyOn(client, "invalidateQueries").mockResolvedValue(undefined);
    const { result } = renderHook(() => useImportOpenAPI(), { wrapper });

    let response: unknown;
    await act(async () => { response = await result.current.mutateAsync(input); });

    expect(api.post).toHaveBeenCalledWith("/admin/api-services/openapi/import", input);
    expect(response).toEqual({ kind: "imported", ...created });
    expect(invalidate.mock.calls.map(([options]) => options?.queryKey)).toEqual([
      ["api-services"], ["api-routes"], ["api-backends"], ["api-upstreams"],
    ]);
  });

  it("classifies sync publication failure with a valid service id as committed and invalidates all imported lists", async () => {
    api.post.mockRejectedValueOnce(new ApiError(500, "untrusted sync wording", {
      code: "sync_publish_failed",
      details: { service_id: 42 },
    }));
    const { client, wrapper } = setup();
    const invalidate = vi.spyOn(client, "invalidateQueries").mockResolvedValue(undefined);
    const { result } = renderHook(() => useImportOpenAPI(), { wrapper });

    let response: unknown;
    await act(async () => { response = await result.current.mutateAsync({} as OpenAPIImportInput); });

    expect(response).toEqual({ kind: "committed-with-sync-failure", service_id: 42 });
    expect(invalidate.mock.calls.map(([options]) => options?.queryKey)).toEqual([
      ["api-services"], ["api-routes"], ["api-backends"], ["api-upstreams"],
    ]);
  });

  it.each([
    new ApiError(500, "untrusted sync wording", { code: "sync_publish_failed", details: { service_id: 0 } }),
    new ApiError(500, "untrusted sync wording", { code: "sync_publish_failed", details: { service_id: "42" } }),
    new ApiError(500, "ordinary failure", { code: "future_code", details: { service_id: 42 } }),
  ])("does not classify an invalid or ordinary API failure as committed", async (failure) => {
    api.post.mockRejectedValueOnce(failure);
    const { client, wrapper } = setup();
    const invalidate = vi.spyOn(client, "invalidateQueries").mockResolvedValue(undefined);
    const { result } = renderHook(() => useImportOpenAPI(), { wrapper });

    await expect(result.current.mutateAsync({} as OpenAPIImportInput)).rejects.toBe(failure);
    expect(invalidate).not.toHaveBeenCalled();
  });
});
