import type { ReactNode } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createTestQueryClient } from "@/test/render";
import {
  useCreateModelRouting,
  useDeleteModelRouting,
  useModelRoutings,
} from "./model-routings";

const { apiDelete, apiGet, apiPost } = vi.hoisted(() => ({
  apiDelete: vi.fn(),
  apiGet: vi.fn(),
  apiPost: vi.fn(),
}));

vi.mock("./client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./client")>();
  return {
    ...actual,
    api: {
      delete: apiDelete,
      get: apiGet,
      post: apiPost,
      put: vi.fn(),
    },
  };
});

function wrapper({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider client={createTestQueryClient()}>
      {children}
    </QueryClientProvider>
  );
}

describe("token model routing API hooks", () => {
  beforeEach(() => {
    apiDelete.mockReset();
    apiGet.mockReset();
    apiPost.mockReset();
  });

  it("loads an admin token subresource with token-scoped query identity", async () => {
    apiGet.mockResolvedValueOnce({ data: [], total: 0, page: 2, page_size: 10 });
    const { result } = renderHook(
      () => useModelRoutings(
        { page: 2, page_size: 10 },
        "admin",
        { kind: "token", tokenId: 17 },
      ),
      { wrapper },
    );

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith(
      "/admin/tokens/17/model-routings?page=2&page_size=10",
    );
  });

  it("posts through the user token path without client-supplied owner fields", async () => {
    apiPost.mockResolvedValueOnce({ id: 9 });
    const { result } = renderHook(
      () => useCreateModelRouting("user", { kind: "token", tokenId: 23 }),
      { wrapper },
    );

    await result.current.mutateAsync({
      name: "smart",
      scope: "token",
      user_id: 99,
      token_id: 88,
      members: [{ ref: "gpt-4.1", priority: 0, weight: 1 }],
      enabled: true,
      remark: "",
    });

    expect(apiPost).toHaveBeenCalledWith(
      "/tokens/23/model-routings",
      {
        name: "smart",
        members: [{ ref: "gpt-4.1", priority: 0, weight: 1 }],
        enabled: true,
        remark: "",
      },
    );
  });

  it("surfaces a delete failure without invalidating it into success", async () => {
    apiDelete.mockRejectedValueOnce(new Error("network"));
    const { result } = renderHook(
      () => useDeleteModelRouting("admin", { kind: "token", tokenId: 5 }),
      { wrapper },
    );

    await expect(result.current.mutateAsync(6)).rejects.toThrow("network");
    expect(apiDelete).toHaveBeenCalledWith("/admin/tokens/5/model-routings/6");
    await waitFor(() => expect(result.current.isError).toBe(true));
  });
});
