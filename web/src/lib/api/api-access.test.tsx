import type { ReactNode } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createTestQueryClient } from "@/test/render";
import { useAPICatalogEffective, useAPICatalogRoutes, useAPICatalogService, useAPICatalogServices, useAPIRole, useAPIRoleBindings, useAPIRoles, useCreateAPIRole, useCreateAPIRoleBinding, useReplaceAPIAccessGrant } from "./api-access";

const adminAll = { mode: "admin-all" } as const;
const token17 = { mode: "token", tokenID: 17 } as const;
const required = { mode: "required" } as const;

const { apiGet, apiPost, apiPut } = vi.hoisted(() => ({ apiGet: vi.fn(), apiPost: vi.fn(), apiPut: vi.fn() }));

vi.mock("./client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./client")>();
  return { ...actual, api: { ...actual.api, get: apiGet, post: apiPost, put: apiPut } };
});

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={createTestQueryClient()}>{children}</QueryClientProvider>;
}

describe("generic API access hooks", () => {
  beforeEach(() => { apiGet.mockReset(); apiPost.mockReset(); apiPut.mockReset(); });

  it("loads role grants and bindings from the administrator-only endpoints", async () => {
    apiGet
      .mockResolvedValueOnce({ data: [{ id: 3, key: "weather-invoker", permissions: [{ resource: "api_service", resource_id: 7, action: "invoke" }] }], total: 1, page: 1, page_size: 20 })
      .mockResolvedValueOnce({ data: [{ id: 4, principal_type: "user", principal_id: 8, role_id: 3 }], total: 1, page: 1, page_size: 20 });
    const { result } = renderHook(() => ({ roles: useAPIRoles(), bindings: useAPIRoleBindings() }), { wrapper });
    await waitFor(() => expect(result.current.bindings.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith("/admin/api-roles");
    expect(apiGet).toHaveBeenCalledWith("/admin/api-role-bindings");
  });

  it("includes role and binding filters in stable list requests", async () => {
    apiGet.mockResolvedValue({ data: [], total: 0, page: 2, page_size: 20 });
    const { result } = renderHook(
      () => ({
        roles: useAPIRoles({ search: "weather", status: 1, page: 2, page_size: 20 } as never),
        bindings: useAPIRoleBindings({
          principal_type: "token",
          principal_id: 8,
          role_id: 3,
          page: 2,
          page_size: 20,
        } as never),
      }),
      { wrapper },
    );
    await waitFor(() => expect(result.current.bindings.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith("/admin/api-roles?search=weather&status=1&page=2&page_size=20");
    expect(apiGet).toHaveBeenCalledWith("/admin/api-role-bindings?principal_type=token&principal_id=8&role_id=3&page=2&page_size=20");
  });

  it("preserves a server rejection when a binding targets an invalid principal", async () => {
    apiPost.mockRejectedValueOnce(new Error("principal does not exist"));
    const { result } = renderHook(() => useCreateAPIRoleBinding(), { wrapper });
    await act(async () => {
      await expect(result.current.mutateAsync({ principal_type: "user", principal_id: 0, role_id: 3 })).rejects.toThrow("principal does not exist");
    });
  });

  it("clears permission-derived data after role and grant mutations but preserves LLM data", async () => {
    apiPost.mockResolvedValueOnce({ id: 3 });
    apiPut.mockResolvedValueOnce({ scope: "routes", route_ids: [9] });
    const client = createTestQueryClient();
    const localWrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    const { result } = renderHook(() => ({ role: useCreateAPIRole(), grant: useReplaceAPIAccessGrant() }), { wrapper: localWrapper });
    const catalogKey = ["api-catalog", "services", 1];
    const validationKey = ["tokens", "usable-for-api-route", 1, 7, 9, 5];
    const seed = () => {
      client.setQueryData(catalogKey, { data: [{ id: 7 }] });
      client.setQueryData(validationKey, { id: 5 });
    };
    client.setQueryData(["available-models"], ["gpt-5"]);

    seed();
    await act(async () => { await result.current.role.mutateAsync({ key: "weather", name: "Weather", description: "", status: 1, permissions: [], members: [] }); });
    expect(client.getQueryData(catalogKey)).toBeUndefined();
    expect(client.getQueryData(validationKey)).toBeUndefined();

    seed();
    await act(async () => { await result.current.grant.mutateAsync({ principal_type: "token", principal_id: 5, api_service_id: 7, scope: "routes", route_ids: [9] }); });
    expect(client.getQueryData(catalogKey)).toBeUndefined();
    expect(client.getQueryData(validationKey)).toBeUndefined();
    expect(client.getQueryData(["available-models"])).toEqual(["gpt-5"]);
  });

  it("uses an independent query key for roles and bindings", async () => {
    apiGet.mockResolvedValue({ data: [], total: 0, page: 1, page_size: 20 });
    const client = createTestQueryClient();
    const localWrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    renderHook(() => ({ roles: useAPIRoles(), bindings: useAPIRoleBindings() }), { wrapper: localWrapper });
    await waitFor(() => expect(client.getQueryCache().getAll()).toHaveLength(2));
    expect(client.getQueryCache().getAll().map((query) => query.queryKey[0]).sort()).toEqual(["api-role-bindings", "api-roles"]);
  });

  it("does not issue a role detail read for zero or NaN ids", () => {
    renderHook(() => ({ zero: useAPIRole(0), nan: useAPIRole(Number.NaN) }), { wrapper });
    expect(apiGet).not.toHaveBeenCalled();
  });

  it("adds token_id to all catalog endpoint requests for a Token scope", async () => {
    apiGet
      .mockResolvedValueOnce({ data: [{ id: 7, slug: "weather", name: "Weather", description: "Forecast" }], total: 1, page: 1, page_size: 20 })
      .mockResolvedValueOnce({ id: 7, slug: "weather", name: "Weather", description: "Forecast" })
      .mockResolvedValueOnce({ data: [{ id: 9, api_service_id: 7, slug: "forecast", protocols: ["http"], allowed_methods: ["GET"], websocket_subprotocols: [] }], total: 1, page: 1, page_size: 20 })
      .mockResolvedValueOnce({ scope: "routes", route_ids: [9] });
    const { result } = renderHook(() => ({
      services: useAPICatalogServices(1, token17),
      service: useAPICatalogService(1, token17, 7),
      routes: useAPICatalogRoutes(1, token17, 7),
      effective: useAPICatalogEffective(1, token17, 7),
    }), { wrapper });
    await waitFor(() => expect(result.current.effective.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith("/api-catalog/services?token_id=17");
    expect(apiGet).toHaveBeenCalledWith("/api-catalog/services/detail?id=7&token_id=17");
    expect(apiGet).toHaveBeenCalledWith("/api-catalog/routes?service_id=7&token_id=17");
    expect(apiGet).toHaveBeenCalledWith("/api-catalog/effective?service_id=7&token_id=17");
  });

  it("omits token_id for all catalog endpoints in the admin-all scope", async () => {
    apiGet.mockResolvedValue({ data: [], total: 0, page: 1, page_size: 20 });
    const { result } = renderHook(() => ({
      services: useAPICatalogServices(1, adminAll),
      service: useAPICatalogService(1, adminAll, 7),
      routes: useAPICatalogRoutes(1, adminAll, 7),
      effective: useAPICatalogEffective(1, adminAll, 7),
    }), { wrapper });

    await waitFor(() => expect(result.current.effective.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith("/api-catalog/services");
    expect(apiGet).toHaveBeenCalledWith("/api-catalog/services/detail?id=7");
    expect(apiGet).toHaveBeenCalledWith("/api-catalog/routes?service_id=7");
    expect(apiGet).toHaveBeenCalledWith("/api-catalog/effective?service_id=7");
  });

  it("keeps the same Service id in separate Token query caches", async () => {
    apiGet.mockResolvedValue({ id: 7, slug: "weather", name: "Weather", description: "Forecast" });
    const client = createTestQueryClient();
    const localWrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    renderHook(() => ({
      token17: useAPICatalogService(1, token17, 7),
      token18: useAPICatalogService(1, { mode: "token", tokenID: 18 }, 7),
    }), { wrapper: localWrapper });

    await waitFor(() => expect(client.getQueryCache().getAll()).toHaveLength(2));
    expect(client.getQueryCache().getAll().map((query) => query.queryKey)).toEqual(expect.arrayContaining([
      ["api-catalog", "service", 1, ["token", 17], 7],
      ["api-catalog", "service", 1, ["token", 18], 7],
    ]));
  });

  it("keeps the same Token catalog in separate viewer query caches", async () => {
    apiGet.mockResolvedValue({ id: 7, slug: "weather", name: "Weather", description: "Forecast" });
    const client = createTestQueryClient();
    const localWrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    renderHook(() => ({
      viewerA: useAPICatalogService(1, token17, 7),
      viewerB: useAPICatalogService(2, token17, 7),
    }), { wrapper: localWrapper });

    await waitFor(() => expect(client.getQueryCache().getAll()).toHaveLength(2));
    expect(client.getQueryCache().getAll().map((query) => query.queryKey)).toEqual(expect.arrayContaining([
      ["api-catalog", "service", 1, ["token", 17], 7],
      ["api-catalog", "service", 2, ["token", 17], 7],
    ]));
    expect(apiGet).toHaveBeenCalledTimes(2);
  });

  it("does not request any catalog endpoint for required scope or invalid service ids", () => {
    renderHook(() => ({
      services: useAPICatalogServices(1, required),
      service: useAPICatalogService(1, required, 7),
      routes: useAPICatalogRoutes(1, required, 7),
      effective: useAPICatalogEffective(1, required, 7),
      invalidRoutes: useAPICatalogRoutes(1, adminAll, 0),
      invalidEffective: useAPICatalogEffective(1, adminAll, Number.NaN),
    }), { wrapper });
    expect(apiGet).not.toHaveBeenCalled();
  });
});
