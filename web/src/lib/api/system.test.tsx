import type { ReactNode } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createTestQueryClient } from "@/test/render";
import {
  useCompleteHistoryBackfill,
  useDeleteLegacyArtifact,
  useDeleteLegacySource,
} from "./system";

const { apiDelete, apiPost } = vi.hoisted(() => ({
  apiDelete: vi.fn(),
  apiPost: vi.fn(),
}));

vi.mock("./client", () => ({
  api: {
    delete: apiDelete,
    get: vi.fn(),
    post: apiPost,
    put: vi.fn(),
  },
}));

const queryClient = createTestQueryClient();

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

describe("history backfill admin API hooks", () => {
  beforeEach(() => {
    apiDelete.mockReset();
    apiPost.mockReset();
    queryClient.clear();
  });

  it("posts explicit confirmation before completing backfill", async () => {
    apiPost.mockResolvedValueOnce({ completed: true });
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    const { result } = renderHook(() => useCompleteHistoryBackfill(), { wrapper });

    await result.current.mutateAsync({ confirm: true });

    expect(apiPost).toHaveBeenCalledWith(
      "/admin/system/history-backfill/complete",
      { confirm: true },
    );
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["system-stats"] }));
    invalidate.mockRestore();
  });

  it("sends exact DELETE confirmation when removing the legacy source", async () => {
    apiDelete.mockResolvedValueOnce({ deleted: true });
    const { result } = renderHook(() => useDeleteLegacySource(), { wrapper });

    await result.current.mutateAsync({ confirmation: "DELETE" });

    expect(apiDelete).toHaveBeenCalledWith(
      "/admin/system/history-backfill/source?confirmation=DELETE",
    );
  });

  it("uses the independent artifact endpoint and invalidates storage status", async () => {
    apiDelete.mockResolvedValueOnce({ deleted: true });
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    const { result } = renderHook(() => useDeleteLegacyArtifact(), { wrapper });

    await result.current.mutateAsync({ confirmation: "DELETE" });

    expect(apiDelete).toHaveBeenCalledWith(
      "/admin/system/history-backfill/legacy-artifact?confirmation=DELETE",
    );
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["system-stats"] }));
    invalidate.mockRestore();
  });
});
