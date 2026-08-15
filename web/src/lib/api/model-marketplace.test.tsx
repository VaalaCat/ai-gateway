import type { ReactNode } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createTestQueryClient } from "@/test/render";
import * as marketplaceApi from "./model-marketplace";
import {
  useAdminModelMarketplaceDetail,
  useAdminModelMarketplaceList,
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
  total: 0,
  page: 1,
  page_size: 20,
};

const adminResponse: AdminModelMarketplaceListResponse = {
  view: { mode: "token_preview", selected_token: { id: 17, name: "Primary" } },
  models: [],
  filters: { providers: [], input_modalities: [], output_modalities: [] },
  total: 0,
  page: 1,
  page_size: 20,
};

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={createTestQueryClient()}>{children}</QueryClientProvider>;
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
    const params = {
      tokenId: 17,
      search: "gpt",
      provider: "OpenAI",
      kind: "real" as const,
      page: 2,
      pageSize: 10,
    };
    const { result } = renderHook(() => useModelMarketplaceList(params, 7), { wrapper: localWrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith(
      "/model-marketplace?token_id=17&search=gpt&provider=OpenAI&kind=real&page=2&page_size=10",
    );
    expect(queryClient.getQueryState([
      "model-marketplace",
      "list",
      {
        role: "user",
        viewerId: 7,
        tokenId: 17,
        search: "gpt",
        provider: "OpenAI",
        kind: "real",
        page: 2,
        pageSize: 10,
      },
    ])?.status).toBe("success");
  });

  it("uses the independent administrator endpoint and role-specific key", async () => {
    apiGet.mockResolvedValueOnce(adminResponse);
    const queryClient = createTestQueryClient();
    const localWrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const params = {
      tokenId: 17,
      search: "gpt",
      provider: "OpenAI",
      kind: "real" as const,
      page: 2,
      pageSize: 10,
    };
    const { result } = renderHook(() => useAdminModelMarketplaceList(params, 7), { wrapper: localWrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith(
      "/admin/model-marketplace?token_id=17&search=gpt&provider=OpenAI&kind=real&page=2&page_size=10",
    );
    expect(queryClient.getQueryState([
      "model-marketplace",
      "list",
      {
        role: "admin",
        viewerId: 7,
        tokenId: 17,
        search: "gpt",
        provider: "OpenAI",
        kind: "real",
        page: 2,
        pageSize: 10,
      },
    ])?.status).toBe("success");
    expect(queryClient.getQueryState([
      "model-marketplace",
      "list",
      {
        role: "user",
        viewerId: 7,
        tokenId: 17,
        search: "gpt",
        provider: "OpenAI",
        kind: "real",
        page: 2,
        pageSize: 10,
      },
    ])).toBeUndefined();
  });

  it("does not issue an ordinary-user request without a positive Token id", () => {
    const { result } = renderHook(
      () => useModelMarketplaceList({
        tokenId: undefined,
        search: "",
        provider: "",
        kind: "",
        page: 1,
        pageSize: 20,
      }, 7),
      { wrapper },
    );

    expect(result.current.fetchStatus).toBe("idle");
    expect(apiGet).not.toHaveBeenCalled();
  });

  it.each([
    ["user missing Token", () => useModelMarketplaceList({
      tokenId: undefined,
      search: "",
      provider: "",
      kind: "" as const,
      page: 1,
      pageSize: 20,
    }, 7, { enabled: true }).fetchStatus],
    ["user invalid viewer", () => useModelMarketplaceList({
      tokenId: 17,
      search: "",
      provider: "",
      kind: "" as const,
      page: 1,
      pageSize: 20,
    }, 0, { enabled: true }).fetchStatus],
    ["admin invalid viewer", () => useAdminModelMarketplaceList({
      search: "",
      provider: "",
      kind: "" as const,
      page: 1,
      pageSize: 20,
    }, undefined, { enabled: true }).fetchStatus],
    ["user caller-disabled valid scope", () => useModelMarketplaceList({
      tokenId: 17,
      search: "",
      provider: "",
      kind: "" as const,
      page: 1,
      pageSize: 20,
    }, 7, { enabled: false }).fetchStatus],
    ["admin caller-disabled valid scope", () => useAdminModelMarketplaceList({
      search: "",
      provider: "",
      kind: "" as const,
      page: 1,
      pageSize: 20,
    }, 7, { enabled: false }).fetchStatus],
  ] as const)("keeps the %s hard gate when caller options specify enabled", (_name, useFetchStatus) => {
    const { result } = renderHook(useFetchStatus, { wrapper });

    expect(result.current).toBe("idle");
    expect(apiGet).not.toHaveBeenCalled();
  });

  it("surfaces a list failure without catalog data", async () => {
    apiGet.mockRejectedValueOnce(new Error("catalog unavailable"));
    const { result } = renderHook(
      () => useAdminModelMarketplaceList({
        search: "",
        provider: "",
        kind: "",
        page: 1,
        pageSize: 20,
      }, 7),
      { wrapper },
    );

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.data).toBeUndefined();
  });

  it("isolates catalog cache entries by page and page size", async () => {
    apiGet
      .mockResolvedValueOnce({ ...userResponse, page: 1, page_size: 20 })
      .mockResolvedValueOnce({ ...userResponse, page: 2, page_size: 10 });
    const queryClient = createTestQueryClient();
    const localWrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const pageOneParams = {
      tokenId: 17,
      search: "gpt",
      provider: "OpenAI",
      kind: "real" as const,
      page: 1,
      pageSize: 20,
    };
    const pageTwoParams = { ...pageOneParams, page: 2, pageSize: 10 };
    const pageOne = renderHook(
      () => useModelMarketplaceList(pageOneParams, 7),
      { wrapper: localWrapper },
    );
    await waitFor(() => expect(pageOne.result.current.isSuccess).toBe(true));
    const pageTwo = renderHook(
      () => useModelMarketplaceList(pageTwoParams, 7),
      { wrapper: localWrapper },
    );
    await waitFor(() => expect(pageTwo.result.current.isSuccess).toBe(true));

    expect(apiGet).toHaveBeenCalledTimes(2);
    expect(queryClient.getQueryData([
      "model-marketplace",
      "list",
      { role: "user", viewerId: 7, ...pageOneParams },
    ])).toEqual(expect.objectContaining({ page: 1, page_size: 20 }));
    expect(queryClient.getQueryData([
      "model-marketplace",
      "list",
      { role: "user", viewerId: 7, ...pageTwoParams },
    ])).toEqual(expect.objectContaining({ page: 2, page_size: 10 }));
  });

  it("does not expose a marketplace-specific Token list hook", () => {
    expect(marketplaceApi).not.toHaveProperty("useMarketplaceTokens");
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
