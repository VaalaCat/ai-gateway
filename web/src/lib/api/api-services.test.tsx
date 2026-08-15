import type { ReactNode } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createTestQueryClient } from "@/test/render";
import {
	useAPIRoutes,
	useAllAPIBackends,
	useAllAPIRoutes,
	useAPIRoute,
	useAPIRoutePreview,
	useAPIBackends,
	useAPIService,
  useAPIServices,
  useAPIUpstreams,
  useAllAPIUpstreams,
	useAllAPIUpstreamsByService,
  useAPIUpstream,
	useCreateAPIRoute,
	useCreateAPIUpstream,
	useDeleteAPIBackend,
	useDeleteAPIRoute,
	useDeleteAPIService,
	useDeleteAPIUpstream,
	useUpdateAPIRoute,
	useUpdateAPIBackend,
	type APIRoutePreviewInput,
	type APIRouteInput,
	type APIBackend,
	type APIUpstream,
} from "./api-services";

const { apiDelete, apiGet, apiPost, apiPut } = vi.hoisted(() => ({ apiDelete: vi.fn(), apiGet: vi.fn(), apiPost: vi.fn(), apiPut: vi.fn() }));

vi.mock("./client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./client")>();
	return { ...actual, api: { ...actual.api, delete: apiDelete, get: apiGet, post: apiPost, put: apiPut } };
});

const route: APIRouteInput = {
  api_service_id: 7,
  slug: "forecast",
  protocols: ["http", "websocket"],
  allowed_methods: [],
  upstream_path: "/v1/forecast",
  forward_subpath: true,
  example_request: { method: "", subpath: "", query: "", headers: {}, body: "" },
  target: { mode: "existing", backend_id: 12 },
};

function page<T>(data: T[], total: number, current: number, pageSize: number) {
	return { data, total, page: current, page_size: pageSize };
}

function backend(id: number): APIBackend {
	return { id, api_service_id: 7, name: `Target ${id}`, route_count: 0, upstream_count: 0, enabled_upstream_count: 0, endpoint_hosts: [] };
}

function upstream(id: number): APIUpstream {
	return { id, backend_id: 17, name: `Endpoint ${id}`, base_url: `https://endpoint-${id}.test`, weight: 1, priority: 0, auth_type: "none", status: 1, credential_configured: false, proxy_url_configured: false };
}

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={createTestQueryClient()}>{children}</QueryClientProvider>;
}

describe("generic API service hooks", () => {
	beforeEach(() => { apiDelete.mockReset(); apiGet.mockReset(); apiPost.mockReset(); apiPut.mockReset(); });

	it("separates a numeric Route cache key from the same opaque draft revision", async () => {
		const client = createTestQueryClient();
		const localWrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		const dynamicDraft: APIRoutePreviewInput = {
			api_service_id: 7, slug: "dynamic", upstream_path: "", forward_subpath: false,
			sample: { method: "GET", subpath: "", query: "", headers: {}, body: "" },
			target: { mode: "create", backend: { name: "edge" }, first_upstream: { name: "primary", base_url: "https://edge.test", priority: 0, weight: 1, auth_type: "bearer", credential: { bearer_token: "collision-secret" } } },
		};
		const routeDraft: APIRoutePreviewInput = { ...dynamicDraft, slug: "route", target: { mode: "existing", backend_id: 12 } };
		apiPost.mockImplementation(async (_path, draft: APIRoutePreviewInput) => ({ endpoints: [], diagnostics: [draft.slug] }));
		const { result, rerender } = renderHook(
			({ stableDraft, cacheKey }: { stableDraft?: APIRoutePreviewInput; cacheKey: number }) => ({
				dynamic: useAPIRoutePreview(dynamicDraft),
				stable: useAPIRoutePreview(stableDraft, { cacheKey }),
			}),
			{ initialProps: { stableDraft: undefined as APIRoutePreviewInput | undefined, cacheKey: -1 }, wrapper: localWrapper },
		);
		await waitFor(() => expect(result.current.dynamic.data?.diagnostics).toEqual(["dynamic"]));
		const dynamicKey = client.getQueryCache().findAll({ queryKey: ["api-route-preview", 7] })
			.map((query) => query.queryKey)
			.find((key) => key.at(-1) !== -1);
		const collidingNumber = dynamicKey?.at(-1);
		expect(collidingNumber).toBeTypeOf("number");

		rerender({ stableDraft: routeDraft, cacheKey: collidingNumber as number });

		await waitFor(() => expect(result.current.stable.data?.diagnostics).toEqual(["route"]));
		expect(apiPost).toHaveBeenCalledTimes(2);
		const keys = client.getQueryCache().findAll({ queryKey: ["api-route-preview", 7] }).map((query) => query.queryKey);
		expect(keys).toContainEqual(["api-route-preview", 7, "draft", collidingNumber]);
		expect(keys).toContainEqual(["api-route-preview", 7, "route", collidingNumber]);
		for (const key of keys) expect(JSON.stringify(key)).not.toContain("collision-secret");
	});

	it("loads searchable Backend candidates within one API Service", async () => {
		apiGet.mockResolvedValueOnce({ data: [], total: 0, page: 1, page_size: 20 });
		const { result } = renderHook(
			() => useAPIBackends({ api_service_id: 7, search: "forecast" }),
			{ wrapper },
		);

		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(apiGet).toHaveBeenCalledWith("/admin/api-backends?api_service_id=7&search=forecast");
	});

	it("loads Upstreams through an exact Backend scope", async () => {
		apiGet.mockResolvedValueOnce({ data: [], total: 0, page: 1, page_size: 20 });
		const { result } = renderHook(
			() => useAPIUpstreams({ backend_id: 12 }),
			{ wrapper },
		);

		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(apiGet).toHaveBeenCalledWith("/admin/api-upstreams?backend_id=12");
	});

	it("shares one Endpoint query for Routes using the same Target", async () => {
		apiGet.mockResolvedValue({ data: [], total: 0, page: 1, page_size: 100 });
		const { result } = renderHook(() => ({
			first: useAPIUpstreams({ backend_id: 17, page: 1, page_size: 100 }),
			second: useAPIUpstreams({ backend_id: 17, page: 1, page_size: 100 }),
		}), { wrapper });

		await waitFor(() => expect(result.current.first.isSuccess).toBe(true));
		expect(result.current.second.isSuccess).toBe(true);
		expect(apiGet).toHaveBeenCalledTimes(1);
		expect(apiGet).toHaveBeenCalledWith("/admin/api-upstreams?backend_id=17&page=1&page_size=100");
	});

	it("loads every Endpoint page for a Target review", async () => {
		apiGet
			.mockResolvedValueOnce({ data: Array.from({ length: 100 }, (_, index) => ({ id: index + 1, base_url: `https://edge-${index + 1}.test` })), total: 101, page: 1, page_size: 100 })
			.mockResolvedValueOnce({ data: [{ id: 101, base_url: "https://edge-101.test" }], total: 101, page: 2, page_size: 100 });
		const { result } = renderHook(() => useAllAPIUpstreams(12), { wrapper });

		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(apiGet).toHaveBeenNthCalledWith(1, "/admin/api-upstreams?backend_id=12&page=1&page_size=100");
		expect(apiGet).toHaveBeenNthCalledWith(2, "/admin/api-upstreams?backend_id=12&page=2&page_size=100");
		expect(result.current.data).toHaveLength(101);
	});

	it("shares the complete Endpoint cache for Routes using the same Target", async () => {
		apiGet.mockResolvedValueOnce({ data: [{ id: 1, backend_id: 12 }], total: 1, page: 1, page_size: 100 });
		const { result } = renderHook(() => ({
			first: useAllAPIUpstreams(12),
			second: useAllAPIUpstreams(12),
		}), { wrapper });

		await waitFor(() => expect(result.current.first.isSuccess).toBe(true));
		expect(result.current.second.data).toEqual([{ id: 1, backend_id: 12 }]);
		expect(apiGet).toHaveBeenCalledTimes(1);
	});

	it("loads every Target page for one Service", async () => {
		apiGet.mockResolvedValueOnce(page([backend(1)], 101, 1, 100));
		apiGet.mockResolvedValueOnce(page([backend(101)], 101, 2, 100));
		const { result } = renderHook(() => useAllAPIBackends(7), { wrapper });
		await waitFor(() => expect(result.current.data).toHaveLength(2));
		expect(apiGet).toHaveBeenNthCalledWith(1, "/admin/api-backends?api_service_id=7&page=1&page_size=100");
	});

	it("fails the complete Target query when a later Service page rejects", async () => {
		apiGet.mockResolvedValueOnce(page(Array.from({ length: 100 }, (_, index) => backend(index + 1)), 201, 1, 100));
		apiGet.mockRejectedValueOnce(new Error("page 2 rejected"));
		const { result } = renderHook(() => useAllAPIBackends(7), { wrapper });
		await waitFor(() => expect(result.current.isError).toBe(true));
		expect(result.current.data).toBeUndefined();
		expect(apiGet).toHaveBeenCalledTimes(2);
	});

	it("loads every Endpoint page through the Service scope", async () => {
		apiGet.mockResolvedValueOnce(page([upstream(3)], 1, 1, 100));
		const { result } = renderHook(() => useAllAPIUpstreamsByService(7), { wrapper });
		await waitFor(() => expect(result.current.data?.[0].backend_id).toBe(17));
	});

	it.each([0, -1, 1.5, Number.NaN])("不查询无效的 Service id %s", (id) => {
		renderHook(() => useAllAPIBackends(id), { wrapper });
		expect(apiGet).not.toHaveBeenCalled();
	});

	it("loads every Route page before filtering Target references", async () => {
		apiGet
			.mockResolvedValueOnce({ data: Array.from({ length: 100 }, (_, index) => ({ id: index + 1, backend_id: 11 })), total: 101, page: 1, page_size: 100 })
			.mockResolvedValueOnce({ data: [{ id: 101, backend_id: 12 }], total: 101, page: 2, page_size: 100 });
		const { result } = renderHook(() => useAllAPIRoutes(7), { wrapper });

		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(apiGet).toHaveBeenNthCalledWith(1, "/admin/api-routes?api_service_id=7&page=1&page_size=100");
		expect(apiGet).toHaveBeenNthCalledWith(2, "/admin/api-routes?api_service_id=7&page=2&page_size=100");
		expect(result.current.data?.at(-1)).toEqual({ id: 101, backend_id: 12 });
	});

	it("refreshes the complete Endpoint review after an Endpoint mutation", async () => {
		apiGet.mockResolvedValue({ data: [{ id: 1, base_url: "https://edge.test" }], total: 1, page: 1, page_size: 100 });
		apiPost.mockResolvedValue({ id: 2 });
		const client = createTestQueryClient();
		const localWrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		const { result } = renderHook(() => ({ endpoints: useAllAPIUpstreams(12), create: useCreateAPIUpstream() }), { wrapper: localWrapper });
		await waitFor(() => expect(result.current.endpoints.isSuccess).toBe(true));

		await act(async () => { await result.current.create.mutateAsync({ backend_id: 12, name: "backup", base_url: "https://backup.test", weight: 1, priority: 0, auth_type: "none" }); });

		await waitFor(() => expect(apiGet).toHaveBeenCalledTimes(2));
	});

	it("posts a route draft to the read-only preview contract", async () => {
		const draft: APIRoutePreviewInput = {
			api_service_id: 7,
			slug: "forecast",
			upstream_path: "/v1",
			forward_subpath: true,
			sample: { method: "GET", subpath: "/today", query: "city=Tokyo", headers: {}, body: "" },
			target: { mode: "existing", backend_id: 12 },
		};
		apiPost.mockResolvedValueOnce({ endpoints: [], diagnostics: ["no static upstream endpoints"] });
		const { result } = renderHook(() => useAPIRoutePreview(draft), { wrapper });

		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(apiPost).toHaveBeenCalledWith("/admin/api-routes/preview", draft, expect.objectContaining({ signal: expect.any(AbortSignal) }));
		expect(result.current.data?.diagnostics).toEqual(["no static upstream endpoints"]);
	});

	it("does not post a Route preview while its lazy draft is absent", async () => {
		const { result } = renderHook(() => useAPIRoutePreview(undefined, { cacheKey: 9 }), { wrapper });

		await waitFor(() => expect(result.current.fetchStatus).toBe("idle"));
		expect(apiPost).not.toHaveBeenCalled();
	});

	it("uses an opaque preview cache revision so credentials never enter query keys", async () => {
		const client = createTestQueryClient();
		const localWrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		const draft = (token: string): APIRoutePreviewInput => ({
			api_service_id: 7, slug: "forecast", upstream_path: "", forward_subpath: false,
			sample: { method: "GET", subpath: "", query: "", headers: {}, body: "" },
			target: { mode: "create", backend: { name: "edge" }, first_upstream: { name: "primary", base_url: "https://edge.test", priority: 0, weight: 1, auth_type: "bearer", credential: { bearer_token: token } } },
		});
		apiPost.mockResolvedValue({ endpoints: [], diagnostics: [] });
		const { rerender, unmount } = renderHook(({ value }) => useAPIRoutePreview(value), { initialProps: { value: draft("secret-a") }, wrapper: localWrapper });
		await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(1));
		rerender({ value: draft("secret-b") });
		await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(2));

		for (const query of client.getQueryCache().getAll()) expect(JSON.stringify(query.queryKey)).not.toContain("secret-");
		await waitFor(() => expect(client.getQueryCache().findAll({ queryKey: ["api-route-preview"] })).toHaveLength(1));
		unmount();
		await waitFor(() => expect(client.getQueryCache().findAll({ queryKey: ["api-route-preview"] })).toHaveLength(0));
	});

	it("reuses one stable Route preview cache entry for equivalent default drafts", async () => {
		const client = createTestQueryClient();
		const localWrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		const draft = (): APIRoutePreviewInput => ({
			api_service_id: 7,
			slug: "forecast",
			upstream_path: "/v2",
			forward_subpath: true,
			sample: { method: "GET", subpath: "today", query: "", headers: {}, body: "" },
			target: { mode: "existing", backend_id: 12 },
		});
		apiPost.mockResolvedValue({ endpoints: [], diagnostics: [] });
		const { rerender } = renderHook(({ value }) => useAPIRoutePreview(value, { cacheKey: 9 }), { initialProps: { value: draft() }, wrapper: localWrapper });
		await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(1));

		rerender({ value: draft() });

		await waitFor(() => expect(client.getQueryCache().findAll({ queryKey: ["api-route-preview", 7, "route", 9] })).toHaveLength(1));
		expect(apiPost).toHaveBeenCalledTimes(1);
	});

	it("isolates configured and concrete previews for one Route while sharing configured consumers", async () => {
		const client = createTestQueryClient();
		const localWrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		const concrete: APIRoutePreviewInput = {
			api_service_id: 7, slug: "forecast", upstream_path: "/v2", forward_subpath: true,
			sample: { method: "GET", subpath: "/today", query: "unit=c", headers: {}, body: "" },
			target: { mode: "existing", backend_id: 12 },
		};
		const configured: APIRoutePreviewInput = {
			...concrete,
			sample: { method: "GET", subpath: "", query: "", headers: {}, body: "" },
		};
		apiPost.mockImplementation(async (_path, draft: APIRoutePreviewInput) => ({
			endpoints: [],
			diagnostics: [draft.sample.subpath || "configured"],
		}));

		const { result } = renderHook(() => ({
			concrete: useAPIRoutePreview(concrete, { cacheKey: 9 }),
			configuredFirst: useAPIRoutePreview(configured, { cacheKey: "9:configured" }),
			configuredSecond: useAPIRoutePreview(configured, { cacheKey: "9:configured" }),
		}), { wrapper: localWrapper });

		await waitFor(() => expect(result.current.concrete.data?.diagnostics).toEqual(["/today"]));
		await waitFor(() => expect(result.current.configuredFirst.data?.diagnostics).toEqual(["configured"]));
		expect(result.current.configuredSecond.data?.diagnostics).toEqual(["configured"]);
		expect(apiPost).toHaveBeenCalledTimes(2);
		expect(client.getQueryCache().findAll({ queryKey: ["api-route-preview", 7] }).map((query) => query.queryKey)).toEqual(expect.arrayContaining([
			["api-route-preview", 7, "route", 9],
			["api-route-preview", 7, "route", "9:configured"],
		]));
	});

	it("preserves create and existing target command modes in route mutations", async () => {
		const createTarget = {
			mode: "create" as const,
			backend: { name: "forecast" },
			first_upstream: { name: "primary", base_url: "https://weather.test", weight: 1, priority: 0, auth_type: "none" as const },
		};
		apiPost.mockResolvedValueOnce({ id: 9 });
		apiPut.mockResolvedValueOnce({ status: "ok" });
		const { result } = renderHook(
			() => ({ create: useCreateAPIRoute(), update: useUpdateAPIRoute() }),
			{ wrapper },
		);

		await act(async () => {
			await result.current.create.mutateAsync({ ...route, target: createTarget });
			const response = await result.current.update.mutateAsync({ id: 9, target: { mode: "existing", backend_id: 12 } });
			expect(response).toEqual({ status: "ok" });
		});

		expect(apiPost).toHaveBeenCalledWith("/admin/api-routes", expect.objectContaining({ target: createTarget }));
		expect(apiPut).toHaveBeenCalledWith("/admin/api-routes/9", { target: { mode: "existing", backend_id: 12 } });
	});

	it("returns the Backend update status response from the PUT contract", async () => {
		apiPut.mockResolvedValueOnce({ status: "ok" });
		const { result } = renderHook(() => useUpdateAPIBackend(), { wrapper });

		await act(async () => {
			const response = await result.current.mutateAsync({ id: 17, name: "Forecast production" });
			expect(response).toEqual({ status: "ok" });
		});

		expect(apiPut).toHaveBeenCalledWith("/admin/api-backends/17", { name: "Forecast production" });
	});

	it("invalidates service, route, Backend, Upstream and preview queries after a route mutation", async () => {
		const client = createTestQueryClient();
		const invalidate = vi.spyOn(client, "invalidateQueries");
		const localWrapper = ({ children }: { children: ReactNode }) => (
			<QueryClientProvider client={client}>{children}</QueryClientProvider>
		);
		apiPost.mockResolvedValueOnce({ id: 9 });
		const { result } = renderHook(() => useCreateAPIRoute(), { wrapper: localWrapper });

		await act(async () => {
			await result.current.mutateAsync({
				...route,
				target: { mode: "existing", backend_id: 12 },
			});
		});

		const keys = invalidate.mock.calls.map(([filters]) => filters?.queryKey?.[0]);
		expect(keys).toEqual(expect.arrayContaining([
			"api-services",
			"api-routes",
			"api-backends",
			"api-upstreams",
			"api-route-preview",
		]));
	});

	it.each([
		["Route", useDeleteAPIRoute, 9, "/admin/api-routes/9"],
		["Target", useDeleteAPIBackend, 17, "/admin/api-backends/17"],
		["Endpoint", useDeleteAPIUpstream, 31, "/admin/api-upstreams/31"],
	] as const)("deletes one %s and invalidates every related topology query", async (_subject, useDelete, id, path) => {
		const client = createTestQueryClient();
		const invalidate = vi.spyOn(client, "invalidateQueries");
		const localWrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		apiDelete.mockResolvedValueOnce(undefined);
		const { result } = renderHook(() => useDelete(), { wrapper: localWrapper });

		await act(async () => { await result.current.mutateAsync(id); });

		expect(apiDelete).toHaveBeenCalledWith(path);
		const keys = invalidate.mock.calls.map(([filters]) => filters?.queryKey?.[0]);
		expect(keys).toEqual(expect.arrayContaining(["api-routes", "api-backends", "api-upstreams", "api-service"]));
	});

	it.each([
		["Route", useDeleteAPIRoute, 9, "/admin/api-routes/9"],
		["Target", useDeleteAPIBackend, 17, "/admin/api-backends/17"],
		["Endpoint", useDeleteAPIUpstream, 31, "/admin/api-upstreams/31"],
	] as const)("does not invalidate topology queries when %s deletion fails", async (_subject, useDelete, id, path) => {
		const client = createTestQueryClient();
		const invalidate = vi.spyOn(client, "invalidateQueries");
		const localWrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		apiDelete.mockRejectedValueOnce(new Error("delete rejected"));
		const { result } = renderHook(() => useDelete(), { wrapper: localWrapper });

		await act(async () => { await expect(result.current.mutateAsync(id)).rejects.toThrow("delete rejected"); });

		expect(apiDelete).toHaveBeenCalledWith(path);
		expect(invalidate).not.toHaveBeenCalled();
	});

	it("refetches an active all-Service Route query after a successful Route deletion", async () => {
		apiGet
			.mockResolvedValueOnce(page([{ id: 9, backend_id: 12, ...route }], 1, 1, 100))
			.mockResolvedValueOnce(page([], 0, 1, 100));
		apiDelete.mockResolvedValueOnce(undefined);
		const client = createTestQueryClient();
		const localWrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		const { result } = renderHook(() => ({ routes: useAllAPIRoutes(7), remove: useDeleteAPIRoute() }), { wrapper: localWrapper });
		await waitFor(() => expect(result.current.routes.data).toHaveLength(1));

		await act(async () => { await result.current.remove.mutateAsync(9); });

		await waitFor(() => expect(result.current.routes.data).toEqual([]));
		expect(apiGet).toHaveBeenNthCalledWith(2, "/admin/api-routes?api_service_id=7&page=1&page_size=100");
	});

  it("loads one service and its route/upstream children with the required service filter", async () => {
    apiGet
      .mockResolvedValueOnce({ id: 7, slug: "weather", name: "Weather", status: 1 })
      .mockResolvedValueOnce({ data: [{ id: 9, backend_id: 12, ...route }], total: 1, page: 1, page_size: 20 })
      .mockResolvedValueOnce({ data: [{ id: 4, backend_id: 12, name: "primary", credential_configured: true, proxy_url_configured: false }], total: 1, page: 1, page_size: 20 });
    const { result } = renderHook(() => ({ service: useAPIService(7), routes: useAPIRoutes(7), upstreams: useAPIUpstreams(7) }), { wrapper });

    await waitFor(() => expect(result.current.upstreams.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith("/admin/api-services/7");
    expect(apiGet).toHaveBeenCalledWith("/admin/api-routes?api_service_id=7");
    expect(apiGet).toHaveBeenCalledWith("/admin/api-upstreams?api_service_id=7");
    expect(result.current.routes.data?.data[0]?.allowed_methods).toEqual([]);
    expect(result.current.routes.data?.api_service_id).toBe(7);
    expect(result.current.upstreams.data?.api_service_id).toBe(7);
  });

  it("requests a filtered route page", async () => {
    apiGet.mockResolvedValueOnce({ data: [], total: 0, page: 2, page_size: 20 });
    const params = {
      api_service_id: 7,
      search: "forecast",
      status: 1,
      page: 2,
      page_size: 20,
    };
    const { result } = renderHook(
      () => useAPIRoutes(params),
      { wrapper },
    );

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith(
      "/admin/api-routes?api_service_id=7&search=forecast&status=1&page=2&page_size=20",
    );
  });

  it("retains the previous Route page as an explicit placeholder until the requested page resolves", async () => {
    let resolveSecondPage: ((value: ReturnType<typeof page>) => void) | undefined;
    const secondPage = new Promise<ReturnType<typeof page>>((resolve) => { resolveSecondPage = resolve; });
    apiGet
      .mockResolvedValueOnce(page([{ id: 9, backend_id: 12, ...route }], 2, 1, 1))
      .mockReturnValueOnce(secondPage);
    const { result, rerender } = renderHook(
      ({ currentPage }) => useAPIRoutes(
        { api_service_id: 7, page: currentPage, page_size: 1 },
        { retainPreviousData: true },
      ),
      { initialProps: { currentPage: 1 }, wrapper },
    );
    await waitFor(() => expect(result.current.data?.page).toBe(1));

    rerender({ currentPage: 2 });

    await waitFor(() => expect(result.current.isPlaceholderData).toBe(true));
    expect(result.current.data?.page).toBe(1);
    expect(result.current.data?.data[0]?.id).toBe(9);

    resolveSecondPage?.(page([{ id: 10, backend_id: 12, ...route }], 2, 2, 1));
    await waitFor(() => expect(result.current.isPlaceholderData).toBe(false));
    expect(result.current.data?.page).toBe(2);
    expect(result.current.data?.data[0]?.id).toBe(10);
  });

  it("does not issue child reads for an invalid service id", () => {
    renderHook(() => ({ routes: useAPIRoutes(0), upstreams: useAPIUpstreams(Number.NaN) }), { wrapper });
    expect(apiGet).not.toHaveBeenCalled();
  });

  it("fails closed when either side of a combined Upstream scope is invalid", () => {
    renderHook(() => useAPIUpstreams({ api_service_id: 7, backend_id: 0 }), { wrapper });
    expect(apiGet).not.toHaveBeenCalled();
  });

  it("does not issue API route or upstream detail reads for zero or NaN ids", () => {
    renderHook(() => ({ route: useAPIRoute(0), upstream: useAPIUpstream(Number.NaN) }), { wrapper });
    expect(apiGet).not.toHaveBeenCalled();
  });

  it("fails closed for every service read when capability loading or denial disables it", () => {
    renderHook(() => ({
      service: useAPIService(7, { enabled: false }),
      routes: useAPIRoutes(7, { enabled: false }),
      upstreams: useAPIUpstreams(7, { enabled: false }),
    }), { wrapper });
    expect(apiGet).not.toHaveBeenCalled();
  });

  it("keeps a rejected route creation observable to the form", async () => {
    apiPost.mockRejectedValueOnce(new Error("route rejected"));
    const { result } = renderHook(() => useCreateAPIRoute(), { wrapper });
    await act(async () => {
      await expect(result.current.mutateAsync(route)).rejects.toThrow("route rejected");
    });
  });

  it("resolves service deletion without waiting for an active detail refetch", async () => {
    apiGet
      .mockResolvedValueOnce({ id: 7, slug: "weather", name: "Weather", description: "Current weather", price_per_call: 12, status: 1 })
      .mockReturnValueOnce(new Promise(() => {}));
    apiDelete.mockResolvedValueOnce(undefined);
    const { result } = renderHook(() => ({ service: useAPIService(7), remove: useDeleteAPIService() }), { wrapper });
    await waitFor(() => expect(result.current.service.isSuccess).toBe(true));

    let settled = false;
    void result.current.remove.mutateAsync(7).then(() => { settled = true; });

    await waitFor(() => expect(apiDelete).toHaveBeenCalledWith("/admin/api-services/7"));
    await waitFor(() => expect(settled).toBe(true));
    expect(apiGet).toHaveBeenCalledTimes(2);
  });

  it("keeps a failed service deletion rejected without refreshing queries", async () => {
    apiGet.mockResolvedValueOnce({ id: 7, slug: "weather", name: "Weather", description: "Current weather", price_per_call: 12, status: 1 });
    apiDelete.mockRejectedValueOnce(new Error("delete rejected"));
    const { result } = renderHook(() => ({ service: useAPIService(7), remove: useDeleteAPIService() }), { wrapper });
    await waitFor(() => expect(result.current.service.isSuccess).toBe(true));

    await act(async () => {
      await expect(result.current.remove.mutateAsync(7)).rejects.toThrow("delete rejected");
    });
    expect(apiGet).toHaveBeenCalledTimes(1);
  });

  it("refreshes the active service list after a successful deletion", async () => {
    apiGet
      .mockResolvedValueOnce({ data: [{ id: 7, slug: "weather", name: "Weather", description: "Current weather", price_per_call: 12, status: 1 }], total: 1, page: 1, page_size: 20 })
      .mockResolvedValueOnce({ data: [], total: 0, page: 1, page_size: 20 });
    apiDelete.mockResolvedValueOnce(undefined);
    const { result } = renderHook(() => ({ services: useAPIServices(), remove: useDeleteAPIService() }), { wrapper });
    await waitFor(() => expect(result.current.services.data?.total).toBe(1));

    await act(async () => { await result.current.remove.mutateAsync(7); });

    await waitFor(() => expect(result.current.services.data?.total).toBe(0));
    expect(apiGet).toHaveBeenCalledTimes(2);
  });
});
