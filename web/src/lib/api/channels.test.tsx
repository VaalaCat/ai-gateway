import type { ReactNode } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Channel, PaginatedResponse } from "@/lib/types";
import { createTestQueryClient } from "@/test/render";
import { useChannels, useUpdateChannel } from "./channels";

const { apiGet, apiPut } = vi.hoisted(() => ({
  apiGet: vi.fn(),
  apiPut: vi.fn(),
}));

vi.mock("./client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./client")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      get: apiGet,
      put: apiPut,
    },
  };
});

const channel: Channel = {
  id: 1,
  name: "internal",
  public_display_name: "Public",
  type: 1,
  key: "key",
  base_url: "https://example.com",
  models: "gpt-5",
  model_mapping: "",
  weight: 1,
  priority: 0,
  status: 1,
  setting: "",
  organization: "",
  api_version: "",
  tag: "",
  remark: "",
  test_model: "",
  auto_ban: 0,
  status_code_mapping: "",
  param_override: "",
  header_override: "",
  other_settings: "",
  supported_api_types: "",
  endpoints: "",
  passthrough_enabled: false,
  use_legacy_adaptor: false,
  system_prompt: "",
  proxy_url: "",
  role_mapping: "",
  created_at: 1,
  updated_at: 1,
};

function pageOf(value: Channel): PaginatedResponse<Channel> {
  return { data: [value], total: 1, page: 1, page_size: 10 };
}

describe("channel update query lifecycle", () => {
  beforeEach(() => {
    apiGet.mockReset();
    apiPut.mockReset();
  });

  it("does not resolve a successful update until the active channel list contains refreshed server state", async () => {
    const queryClient = createTestQueryClient();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const updated = { ...channel, status: 0 };
    let resolveRefetch!: (value: PaginatedResponse<Channel>) => void;
    apiGet
      .mockResolvedValueOnce(pageOf(channel))
      .mockImplementationOnce(() => new Promise((resolve) => { resolveRefetch = resolve; }));
    apiPut.mockResolvedValueOnce(updated);
    const { result } = renderHook(
      () => ({ list: useChannels({ page: 1, page_size: 10 }), update: useUpdateChannel() }),
      { wrapper },
    );
    await waitFor(() => expect(result.current.list.isSuccess).toBe(true));

    let settled = false;
    let request!: Promise<Channel>;
    act(() => {
      request = result.current.update.mutateAsync({ id: 1, status: 0 });
      void request.then(() => { settled = true; });
    });
    await waitFor(() => expect(apiGet).toHaveBeenCalledTimes(2));
    await act(async () => undefined);
    expect(settled).toBe(false);

    await act(async () => {
      resolveRefetch(pageOf(updated));
      await request;
    });
    await waitFor(() => expect(result.current.list.data?.data[0].status).toBe(0));
  });

  it("keeps the current list state and skips refetch when the update fails", async () => {
    const queryClient = createTestQueryClient();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    apiGet.mockResolvedValueOnce(pageOf(channel));
    apiPut.mockRejectedValueOnce(new Error("network unavailable"));
    const { result } = renderHook(
      () => ({ list: useChannels({ page: 1, page_size: 10 }), update: useUpdateChannel() }),
      { wrapper },
    );
    await waitFor(() => expect(result.current.list.isSuccess).toBe(true));

    await expect(result.current.update.mutateAsync({ id: 1, status: 0 })).rejects.toThrow("network unavailable");

    expect(apiGet).toHaveBeenCalledTimes(1);
    expect(result.current.list.data?.data[0].status).toBe(1);
  });

  it("returns the server channel when no channel-list query is active", async () => {
    const queryClient = createTestQueryClient();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const updated = { ...channel, status: 0 };
    apiPut.mockResolvedValueOnce(updated);
    const { result } = renderHook(() => useUpdateChannel(), { wrapper });

    await expect(result.current.mutateAsync({ id: 1, status: 0 })).resolves.toEqual(updated);

    expect(apiGet).not.toHaveBeenCalled();
  });
});
