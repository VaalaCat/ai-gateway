import type { ReactNode } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { PaginatedResponse, Token } from "@/lib/types";
import { createTestQueryClient } from "@/test/render";

import { useDeleteToken, useUpdateToken, useUsableTokenForAPIRoute } from "./tokens";

const { apiDelete, apiGet, apiPut } = vi.hoisted(() => ({ apiDelete: vi.fn(), apiGet: vi.fn(), apiPut: vi.fn() }));

vi.mock("./client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./client")>();
  return { ...actual, api: { ...actual.api, delete: apiDelete, get: apiGet, put: apiPut } };
});

const token: Token = {
  id: 5,
  user_id: 1,
  key: "sk-exact-scope",
  name: "Exact Scope",
  status: 1,
  expired_at: 0,
  models: "",
  trace_enabled: false,
  trace_mode: "full",
  api_role_mode: "explicit",
  created_at: 1,
  updated_at: 1,
};

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={createTestQueryClient()}>{children}</QueryClientProvider>;
}

function page(data: Token[]): PaginatedResponse<Token> {
  return { data, total: data.length, page: 1, page_size: 1 };
}

describe("useUsableTokenForAPIRoute", () => {
  beforeEach(() => { apiDelete.mockReset(); apiGet.mockReset(); apiPut.mockReset(); });

  it("validates one exact Token with one scoped request", async () => {
    apiGet.mockResolvedValueOnce(page([token]));

    const { result } = renderHook(() => useUsableTokenForAPIRoute({
      viewerUserID: 1,
      apiServiceID: 7,
      apiRouteID: 9,
      tokenID: 5,
      ownerUserID: 1,
    }), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual(token);
    expect(apiGet).toHaveBeenCalledOnce();
    expect(apiGet).toHaveBeenCalledWith("/tokens?usable_only=true&user_id=1&api_service_id=7&api_route_id=9&token_id=5&page=1&page_size=1");
  });

  it("keeps management validation global when no owner constraint is requested", async () => {
    apiGet.mockResolvedValueOnce(page([token]));

    const { result } = renderHook(() => useUsableTokenForAPIRoute({
      viewerUserID: 1,
      apiServiceID: 7,
      apiRouteID: 9,
      tokenID: 5,
    }), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith("/tokens?usable_only=true&api_service_id=7&api_route_id=9&token_id=5&page=1&page_size=1");
  });

  it("fails closed when an owner-scoped exact response contains another user's Token", async () => {
    apiGet.mockResolvedValueOnce(page([{ ...token, user_id: 2 }]));

    const { result } = renderHook(() => useUsableTokenForAPIRoute({
      viewerUserID: 1,
      apiServiceID: 7,
      apiRouteID: 9,
      tokenID: 5,
      ownerUserID: 1,
    }), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toBeNull();
  });

  it("returns null when the exact scoped Token is absent", async () => {
    apiGet.mockResolvedValueOnce(page([]));

    const { result } = renderHook(() => useUsableTokenForAPIRoute({
      viewerUserID: 1,
      apiServiceID: 7,
      apiRouteID: 9,
      tokenID: 5,
    }), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toBeNull();
    expect(apiGet).toHaveBeenCalledOnce();
  });

  it("does not request an invalid scope", () => {
    const { result } = renderHook(() => useUsableTokenForAPIRoute({
      viewerUserID: 1,
      apiServiceID: 7,
      apiRouteID: 0,
      tokenID: 5,
    }), { wrapper });

    expect(result.current.fetchStatus).toBe("idle");
    expect(apiGet).not.toHaveBeenCalled();
  });

  it("clears catalog and real-key validation data after Token update and delete", async () => {
    apiPut.mockResolvedValueOnce(token);
    apiDelete.mockResolvedValueOnce(undefined);
    const client = createTestQueryClient();
    const localWrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    const { result } = renderHook(() => ({ update: useUpdateToken(), remove: useDeleteToken() }), { wrapper: localWrapper });
    const catalogKey = ["api-catalog", "services", 1];
    const validationKey = ["tokens", "usable-for-api-route", 1, 7, 9, 5];
    const seed = () => {
      client.setQueryData(catalogKey, { data: [{ id: 7 }] });
      client.setQueryData(validationKey, token);
    };

    seed();
    await act(async () => { await result.current.update.mutateAsync({ id: 5, name: "Updated" }); });
    expect(client.getQueryData(catalogKey)).toBeUndefined();
    expect(client.getQueryData(validationKey)).toBeUndefined();

    seed();
    await act(async () => { await result.current.remove.mutateAsync(5); });
    expect(client.getQueryData(catalogKey)).toBeUndefined();
    expect(client.getQueryData(validationKey)).toBeUndefined();
  });
});
