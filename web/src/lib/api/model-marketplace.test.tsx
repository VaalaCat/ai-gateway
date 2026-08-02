import type { ReactNode } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createTestQueryClient } from "@/test/render";
import { useAuth } from "@/lib/auth";
import {
  useAdminModelMarketplaceDetail,
  useAdminModelMarketplaceList,
  useMarketplaceTokens,
  useModelMarketplaceDetail,
  useModelMarketplaceList,
  type AdminModelMarketplaceListResponse,
  type ModelMarketplaceListResponse,
} from "./model-marketplace";

const { apiGet } = vi.hoisted(() => ({ apiGet: vi.fn() }));

vi.mock("./client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./client")>();
  return { ...actual, api: { get: apiGet } };
});

const userResponse: ModelMarketplaceListResponse = {
  selected_token: { id: 17, name: "Primary" },
  models: [],
  filters: { providers: [], input_modalities: [], output_modalities: [] },
};

const adminResponse: AdminModelMarketplaceListResponse = {
  view: { mode: "token_preview", selected_token: { id: 17, name: "Primary" } },
  models: [],
  filters: { providers: [], input_modalities: [], output_modalities: [] },
};

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={createTestQueryClient()}>{children}</QueryClientProvider>;
}

function authToken(userId: number) {
  const payload = btoa(JSON.stringify({
    user_id: userId,
    username: `user-${userId}`,
    role: 1,
    exp: Math.floor(Date.now() / 1000) + 3_600,
  })).replace(/=/g, "").replace(/\+/g, "-").replace(/\//g, "_");
  return `header.${payload}.signature`;
}

describe("model marketplace list API hooks", () => {
  beforeEach(() => {
    apiGet.mockReset();
    window.localStorage.clear();
  });

  it("loads the user list with role token and every filter in its query identity", async () => {
    apiGet.mockResolvedValueOnce(userResponse);
    const queryClient = createTestQueryClient();
    const localWrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const params = { tokenId: 17, search: "gpt", provider: "OpenAI", kind: "real" as const };
    const { result } = renderHook(() => useModelMarketplaceList(params, 7), { wrapper: localWrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith(
      "/model-marketplace?token_id=17&search=gpt&provider=OpenAI&kind=real",
    );
    expect(queryClient.getQueryState([
      "model-marketplace",
      "list",
      { role: "user", viewerId: 7, tokenId: 17, search: "gpt", provider: "OpenAI", kind: "real" },
    ])?.status).toBe("success");
  });

  it("uses the independent administrator endpoint and role-specific key", async () => {
    apiGet.mockResolvedValueOnce(adminResponse);
    const queryClient = createTestQueryClient();
    const localWrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const params = { tokenId: 17, search: "", provider: "", kind: "" as const };
    const { result } = renderHook(() => useAdminModelMarketplaceList(params, 7), { wrapper: localWrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith("/admin/model-marketplace?token_id=17");
    expect(queryClient.getQueryState([
      "model-marketplace",
      "list",
      { role: "admin", viewerId: 7, tokenId: 17, search: "", provider: "", kind: "" },
    ])?.status).toBe("success");
    expect(queryClient.getQueryState([
      "model-marketplace",
      "list",
      { role: "user", viewerId: 7, tokenId: 17, search: "", provider: "", kind: "" },
    ])).toBeUndefined();
  });

  it("does not issue an ordinary-user request without a positive Token id", () => {
    const { result } = renderHook(
      () => useModelMarketplaceList({ tokenId: undefined, search: "", provider: "", kind: "" }, 7),
      { wrapper },
    );

    expect(result.current.fetchStatus).toBe("idle");
    expect(apiGet).not.toHaveBeenCalled();
  });

  it("surfaces a list failure without catalog data", async () => {
    apiGet.mockRejectedValueOnce(new Error("catalog unavailable"));
    const { result } = renderHook(
      () => useAdminModelMarketplaceList({ search: "", provider: "", kind: "" }, 7),
      { wrapper },
    );

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.data).toBeUndefined();
  });

  it("isolates both Token and catalog cache entries when auth storage changes in one QueryClient", async () => {
    let resolveBobCatalog!: (value: ModelMarketplaceListResponse) => void;
    let resolveBobTokens!: (value: { data: never[]; total: number; page: number; page_size: number }) => void;
    const bobCatalog = new Promise<ModelMarketplaceListResponse>((resolve) => { resolveBobCatalog = resolve; });
    const bobTokens = new Promise<{ data: never[]; total: number; page: number; page_size: number }>((resolve) => {
      resolveBobTokens = resolve;
    });
    apiGet.mockImplementation((path: string) => {
      if (path === "/tokens?page=1&page_size=100") {
        return apiGet.mock.calls.filter(([calledPath]) => calledPath === path).length === 1
          ? Promise.resolve({ data: [{ id: 17, name: "Alice Token" }], total: 1, page: 1, page_size: 100 })
          : bobTokens;
      }
      return apiGet.mock.calls.filter(([calledPath]) =>
        typeof calledPath === "string" && calledPath.startsWith("/model-marketplace")
      ).length === 1
        ? Promise.resolve(userResponse)
        : bobCatalog;
    });
    const queryClient = createTestQueryClient();
    const localWrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const aliceToken = authToken(7);
    const bobToken = authToken(8);
    window.localStorage.setItem("token", aliceToken);
    const { result } = renderHook(
      () => {
        const { user } = useAuth();
        const viewerId = user?.user_id;
        return {
          viewerId,
        tokens: useMarketplaceTokens(viewerId),
        catalog: useModelMarketplaceList({ tokenId: 17 }, viewerId),
        };
      },
      { wrapper: localWrapper },
    );

    await waitFor(() => expect(result.current.catalog.data).toBe(userResponse));
    expect(result.current.tokens.data?.[0]?.name).toBe("Alice Token");

    act(() => {
      window.localStorage.setItem("token", bobToken);
      window.dispatchEvent(new StorageEvent("storage", {
        key: "token",
        oldValue: aliceToken,
        newValue: bobToken,
      }));
    });

    expect(result.current.viewerId).toBe(8);
    expect(result.current.catalog.data).toBeUndefined();
    expect(result.current.tokens.data).toBeUndefined();
    expect(queryClient.getQueryData([
      "model-marketplace", "list",
      { role: "user", viewerId: 8, tokenId: 17, search: "", provider: "", kind: "" },
    ])).toBeUndefined();
    expect(queryClient.getQueryData(["model-marketplace", "tokens", { viewerId: 8 }])).toBeUndefined();

    resolveBobTokens({ data: [], total: 0, page: 1, page_size: 100 });
    resolveBobCatalog({ ...userResponse, selected_token: { id: 17, name: "Bob Token" } });
    await waitFor(() => expect(result.current.catalog.data?.selected_token.name).toBe("Bob Token"));
  });

  it("loads every Token page so a valid Token beyond the first 100 is selectable", async () => {
    const firstPage = Array.from({ length: 100 }, (_, index) => ({
      id: index + 1,
      name: `Disabled ${index + 1}`,
      status: 0,
      expired_at: -1,
    }));
    const lastToken = { id: 101, name: "Page two", status: 1, expired_at: -1 };
    apiGet
      .mockResolvedValueOnce({ data: firstPage, total: 101, page: 1, page_size: 100 })
      .mockResolvedValueOnce({ data: [lastToken], total: 101, page: 2, page_size: 100 });

    const { result } = renderHook(() => useMarketplaceTokens(7), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toHaveLength(101);
    expect(result.current.data?.[100]).toEqual(lastToken);
    expect(apiGet).toHaveBeenNthCalledWith(1, "/tokens?page=1&page_size=100");
    expect(apiGet).toHaveBeenNthCalledWith(2, "/tokens?page=2&page_size=100");
  });
});

describe("model marketplace detail API hooks", () => {
  beforeEach(() => {
    apiGet.mockReset();
  });

  it("loads an ordinary detail with every private scope field in the query identity", async () => {
    const response = {
      selected_token: { id: 17, name: "Primary" },
      window: "7d",
      usage_status: "available",
      model: { kind: "real", real: { model_name: "gpt-4o", offers: [] } },
    };
    apiGet.mockResolvedValueOnce(response);
    const queryClient = createTestQueryClient();
    const localWrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );

    const { result } = renderHook(() => useModelMarketplaceDetail({
      tokenId: 17,
      model: "gpt-4o",
      window: "7d",
      offerRef: "offer-a",
    }, 7), { wrapper: localWrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith(
      "/model-marketplace/detail?token_id=17&model=gpt-4o&window=7d&offer_ref=offer-a",
    );
    expect(queryClient.getQueryData([
      "model-marketplace",
      "detail",
      {
        role: "user",
        viewerId: 7,
        tokenId: 17,
        model: "gpt-4o",
        window: "7d",
        offerRef: "offer-a",
      },
    ])).toBe(response);
  });

  it("uses the independent administrator endpoint and isolates global detail cache", async () => {
    const response = {
      view: { mode: "global", selected_token: null },
      window: "30d",
      usage_status: "available",
      model: { kind: "real", real: { model_name: "gpt-4o", offers: [] } },
    };
    apiGet.mockResolvedValueOnce(response);
    const queryClient = createTestQueryClient();
    const localWrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );

    const { result } = renderHook(() => useAdminModelMarketplaceDetail({
      model: "gpt-4o",
      window: "30d",
    }, 9), { wrapper: localWrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith(
      "/admin/model-marketplace/detail?model=gpt-4o&window=30d",
    );
    expect(queryClient.getQueryData([
      "model-marketplace",
      "detail",
      {
        role: "admin",
        viewerId: 9,
        tokenId: null,
        model: "gpt-4o",
        window: "30d",
        offerRef: null,
      },
    ])).toBe(response);
    expect(queryClient.getQueryData([
      "model-marketplace",
      "detail",
      {
        role: "user",
        viewerId: 9,
        tokenId: null,
        model: "gpt-4o",
        window: "30d",
        offerRef: null,
      },
    ])).toBeUndefined();
  });

  it("does not request an ordinary detail without a positive Token or non-empty model", () => {
    const missingToken = renderHook(() => useModelMarketplaceDetail({
      model: "gpt-4o",
      window: "24h",
    }, 7), { wrapper });
    const missingModel = renderHook(() => useModelMarketplaceDetail({
      tokenId: 17,
      model: "  ",
      window: "24h",
    }, 7), { wrapper });

    expect(missingToken.result.current.fetchStatus).toBe("idle");
    expect(missingModel.result.current.fetchStatus).toBe("idle");
    expect(apiGet).not.toHaveBeenCalled();
  });
});
