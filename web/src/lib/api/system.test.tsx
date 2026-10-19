import type { ReactNode } from "react";
import { focusManager, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createTestQueryClient } from "@/test/render";
import {
  deleteCleanupTableBatch,
  previewCleanupTable,
  useCompleteHistoryBackfill,
  useDeleteLegacyArtifact,
  useDeleteLegacySource,
  useSettings,
  useUpdateSettings,
  usePublicConfig,
} from "./system";

const { apiDelete, apiGet, apiPost, apiPut } = vi.hoisted(() => ({
  apiDelete: vi.fn(),
  apiGet: vi.fn(),
  apiPost: vi.fn(),
  apiPut: vi.fn(),
}));

vi.mock("./client", () => ({
  api: {
    delete: apiDelete,
    get: apiGet,
    post: apiPost,
    put: apiPut,
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

describe("data cleanup API", () => {
  it("sends the exact table preview query", async () => {
    const apiGet = vi.mocked((await import("./client")).api.get);
    apiGet.mockResolvedValueOnce({});

    await previewCleanupTable({ database: "log", table: "request_logs", cutoff_date: "2026-07-21" });

    expect(apiGet).toHaveBeenCalledWith(
      "/admin/system/cleanup/preview?database=log&table=request_logs&cutoff_date=2026-07-21",
    );
  });

  it("posts the exact batch body including the snapshot watermark", async () => {
    const body = {
      database: "core",
      table: "billing_logs",
      cutoff_date: "2026-07-21",
      snapshot_max_key: "42",
    } as const;
    apiPost.mockResolvedValueOnce({ deleted: 1, has_more: false });

    await deleteCleanupTableBatch(body);

    expect(apiPost).toHaveBeenCalledWith("/admin/system/cleanup/batch", body);
  });
});

describe("settings API hooks", () => {
  beforeEach(() => {
    apiGet.mockReset();
    apiPut.mockReset();
  });

  it("keeps the confirmed settings response cached when invalidation refetch fails", async () => {
    const settingsClient = createTestQueryClient();
    const key = ["system-settings"] as const;
    const oldSettings = { settings: { "billing.log_retention_days": "45" } };
    const confirmedSettings = { settings: { "billing.log_retention_days": "90" } };
    settingsClient.setQueryDefaults(key, { staleTime: Infinity });
    settingsClient.setQueryData(key, oldSettings);
    apiPut.mockResolvedValueOnce(confirmedSettings);
    apiGet.mockRejectedValueOnce(new Error("settings refetch failed"));
    const settingsWrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={settingsClient}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(
      () => ({ settings: useSettings(), update: useUpdateSettings() }),
      { wrapper: settingsWrapper },
    );

    await act(async () => {
      await result.current.update.mutateAsync({
        settings: { "billing.log_retention_days": "90" },
      });
    });
    await waitFor(() => expect(result.current.settings.isError).toBe(true));

    expect(apiPut).toHaveBeenCalledWith("/admin/system/settings", {
      settings: { "billing.log_retention_days": "90" },
    });
    expect(result.current.settings.data).toEqual(confirmedSettings);
    expect(settingsClient.getQueryData(key)).toEqual(confirmedSettings);
  });

  it("refreshes public branding after saving without publishing unrelated admin settings", async () => {
    const settingsClient = createTestQueryClient();
    let siteName = "Before";
    apiGet.mockImplementation(async (path: string) => {
      if (path === "/system/public-config") return { site_name: siteName };
      return { settings: { site_name: siteName, private_setting: "secret" } };
    });
    apiPut.mockImplementation(async () => {
      siteName = "After";
      return { settings: { site_name: siteName, private_setting: "secret" } };
    });
    const settingsWrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={settingsClient}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => ({ config: usePublicConfig(), update: useUpdateSettings() }), { wrapper: settingsWrapper });
    await waitFor(() => expect(result.current.config.data?.site_name).toBe("Before"));

    await act(async () => {
      await result.current.update.mutateAsync({ settings: { site_name: "After" } });
    });

    await waitFor(() => expect(result.current.config.data?.site_name).toBe("After"));
    expect(settingsClient.getQueryData(["public-config"])).toEqual({ site_name: "After" });
    expect(apiGet.mock.calls.filter(([path]) => path === "/system/public-config")).toHaveLength(2);
  });

  it("leaves public config untouched when saving fails", async () => {
    const settingsClient = createTestQueryClient();
    settingsClient.setQueryData(["public-config"], { site_name: "Before" });
    const invalidate = vi.spyOn(settingsClient, "invalidateQueries");
    apiPut.mockRejectedValueOnce(new Error("save failed"));
    const settingsWrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={settingsClient}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useUpdateSettings(), { wrapper: settingsWrapper });

    await act(async () => {
      await expect(result.current.mutateAsync({ settings: { site_name: "After" } })).rejects.toThrow("save failed");
    });

    expect(settingsClient.getQueryData(["public-config"])).toEqual({ site_name: "Before" });
    expect(invalidate).not.toHaveBeenCalled();
  });

  it("polls public config every thirty seconds and refreshes fresh data on window focus", async () => {
    vi.useFakeTimers();
    const publicClient = createTestQueryClient();
    publicClient.setQueryDefaults(["public-config"], { staleTime: Infinity });
    apiGet.mockResolvedValue({ site_name: "Site" });
    const publicWrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={publicClient}>{children}</QueryClientProvider>
    );
    focusManager.setFocused(true);
    const hook = renderHook(() => usePublicConfig(), { wrapper: publicWrapper });
    try {
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(apiGet).toHaveBeenCalledTimes(1);
      await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
      expect(apiGet).toHaveBeenCalledTimes(2);
      await act(async () => {
        focusManager.setFocused(false);
        focusManager.setFocused(true);
        await vi.advanceTimersByTimeAsync(1);
      });
      expect(apiGet).toHaveBeenCalledTimes(3);
      expect(apiGet).toHaveBeenLastCalledWith("/system/public-config");
    } finally {
      hook.unmount();
      publicClient.clear();
      focusManager.setFocused(undefined);
      vi.useRealTimers();
    }
  });
});
