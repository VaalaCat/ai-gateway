import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode, type ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api/client";
import type {
  CleanupBatchResponse,
  CleanupTablePreview,
  CleanupTableRef,
} from "@/lib/types";
import {
  cleanupQueryRoots,
  useDataCleanup,
  type DataCleanupDependencies,
} from "./use-data-cleanup";

function previewFor(table: CleanupTableRef, toDelete: number): CleanupTablePreview {
  return {
    ...table,
    cutoff_date: "2026-07-21",
    total: toDelete,
    to_delete: toDelete,
    snapshot_max_key: `${table.table}-snapshot`,
  };
}

function renderCleanupHook(
  overrides: Partial<DataCleanupDependencies> = {},
  strict = false,
) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const previewTable = overrides.previewTable ?? vi.fn(async (table) => previewFor(table, 1));
  const deleteBatch = overrides.deleteBatch ?? vi.fn(async () => ({ deleted: 1, has_more: false }));
  const wrapper = ({ children }: { children: ReactNode }) => {
    const content = <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
    return strict ? <StrictMode>{content}</StrictMode> : content;
  };
  return {
    ...renderHook(
      () => useDataCleanup({ previewTable, deleteBatch }),
      { wrapper },
    ),
    previewTable,
    deleteBatch,
    queryClient,
  };
}

async function previewBillingFacts(
  result: ReturnType<typeof renderCleanupHook>["result"],
) {
  await act(() => result.current.preview("billingFacts", "2026-07-21"));
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("useDataCleanup", () => {
  it("previews category tables sequentially and sums deletable rows", async () => {
    const calls: string[] = [];
    const previewTable = vi.fn(async (table: CleanupTableRef) => {
      calls.push(table.table);
      return previewFor(table, table.table === "billing_logs" ? 7 : 3);
    });
    const { result } = renderCleanupHook({ previewTable });

    await previewBillingFacts(result);

    expect(calls).toEqual(["billing_logs"]);
    expect(result.current.run.status).toBe("ready");
    expect(result.current.run.totalToDelete).toBe(7);
  });

  it("runs every batch before advancing to the next table", async () => {
    const deleteBatch = vi
      .fn()
      .mockResolvedValueOnce({ deleted: 500, has_more: true })
      .mockResolvedValueOnce({ deleted: 2, has_more: false });
    const { result } = renderCleanupHook({
      previewTable: vi.fn(async (table) => previewFor(table, 1)),
      deleteBatch,
    });
    await previewBillingFacts(result);

    await act(() => result.current.start());

    expect(deleteBatch).toHaveBeenCalledTimes(2);
    expect(result.current.run.deleted).toBe(502);
    expect(result.current.run.status).toBe("completed");
  });

  it("stops before sending the next batch", async () => {
    const firstBatch = deferred<CleanupBatchResponse>();
    const deleteBatch = vi.fn().mockReturnValueOnce(firstBatch.promise);
    const { result } = renderCleanupHook({ deleteBatch });
    await previewBillingFacts(result);

    let running!: Promise<void>;
    act(() => { running = result.current.start(); });
    act(() => result.current.stop());
    firstBatch.resolve({ deleted: 500, has_more: true });
    await act(() => running);

    expect(deleteBatch).toHaveBeenCalledTimes(1);
    expect(result.current.run.status).toBe("stopped");
  });

  it("pauses on failure and retry re-previews only the failed table", async () => {
    const previewTable = vi.fn(async (table: CleanupTableRef) => previewFor(table, 1));
    const deleteBatch = vi
      .fn()
      .mockRejectedValueOnce(new Error("locked"))
      .mockResolvedValue({ deleted: 1, has_more: false });
    const { result } = renderCleanupHook({ previewTable, deleteBatch });
    await previewBillingFacts(result);
    await act(() => result.current.start());
    expect(result.current.run.status).toBe("paused");

    await act(() => result.current.retry());

    expect(previewTable).toHaveBeenCalledTimes(2);
    expect(previewTable).toHaveBeenLastCalledWith(expect.objectContaining({ table: "billing_logs" }));
    expect(result.current.run.status).toBe("completed");
  });

  it("retries a preview failure from the first table and requires confirmation", async () => {
    const calls: string[] = [];
    let failPreview = true;
    const previewTable = vi.fn(async (table: CleanupTableRef) => {
      calls.push(table.table);
      if (failPreview) {
        failPreview = false;
        throw new Error("preview failed");
      }
      return previewFor(table, 1);
    });
    const deleteBatch = vi.fn();
    const { result } = renderCleanupHook({ previewTable, deleteBatch });

    await previewBillingFacts(result);
    expect(result.current.run.status).toBe("paused");
    await act(() => result.current.retry());

    expect(calls).toEqual(["billing_logs", "billing_logs"]);
    expect(result.current.run.status).toBe("ready");
    expect(result.current.run.tables).toHaveLength(1);
    expect(deleteBatch).not.toHaveBeenCalled();
  });

  it("ignores an old preview response after a new category starts", async () => {
    const oldPreview = deferred<CleanupTablePreview>();
    const previewTable = vi.fn(async (table: CleanupTableRef) => {
      if (table.table === "billing_logs") return oldPreview.promise;
      return previewFor(table, 1);
    });
    const { result } = renderCleanupHook({ previewTable });

    let oldRun!: Promise<void>;
    act(() => { oldRun = result.current.preview("billingFacts", "2026-07-21"); });
    await act(() => result.current.preview("requestLogs", "2026-07-20"));
    oldPreview.resolve(previewFor({ database: "core", table: "billing_logs" }, 99));
    await act(() => oldRun);

    expect(result.current.run.categoryID).toBe("requestLogs");
    expect(result.current.run.cutoffDate).toBe("2026-07-20");
    expect(result.current.run.tables.map((table) => table.table)).toEqual(["request_logs"]);
  });

  it("prevents concurrent retries while the failed table is being re-previewed", async () => {
    const retryPreview = deferred<CleanupTablePreview>();
    const previewTable = vi
      .fn()
      .mockImplementationOnce(async (table: CleanupTableRef) => previewFor(table, 1))
      .mockReturnValueOnce(retryPreview.promise);
    const deleteBatch = vi
      .fn()
      .mockRejectedValueOnce(new Error("locked"))
      .mockResolvedValue({ deleted: 1, has_more: false });
    const { result } = renderCleanupHook({ previewTable, deleteBatch });
    await previewBillingFacts(result);
    await act(() => result.current.start());

    let firstRetry!: Promise<void>;
    let secondRetry!: Promise<void>;
    act(() => {
      firstRetry = result.current.retry();
      secondRetry = result.current.retry();
    });
    retryPreview.resolve(previewFor({ database: "core", table: "billing_logs" }, 1));
    await act(async () => { await Promise.all([firstRetry, secondRetry]); });

    expect(previewTable).toHaveBeenCalledTimes(2);
    expect(deleteBatch).toHaveBeenCalledTimes(2);
  });

  it("publishes state after the Strict Mode effect replay", async () => {
    const { result } = renderCleanupHook({}, true);

    await previewBillingFacts(result);

    expect(result.current.run.status).toBe("ready");
  });

  it("terminates a category when the backend rejects its table", async () => {
    const previewTable = vi.fn().mockRejectedValue(
      new ApiError(400, "not allowed", { code: "CleanupTableNotAllowed" }),
    );
    const { result } = renderCleanupHook({ previewTable });

    await previewBillingFacts(result);
    await act(() => result.current.retry());

    expect(result.current.run.status).toBe("stopped");
    expect(previewTable).toHaveBeenCalledTimes(1);
  });

  it("keeps a cumulative per-table denominator after a partial retry", async () => {
    const previewTable = vi
      .fn()
      .mockImplementationOnce(async (table: CleanupTableRef) => previewFor(table, 1_000))
      .mockImplementationOnce(async (table: CleanupTableRef) => previewFor(table, 500));
    const deleteBatch = vi
      .fn()
      .mockResolvedValueOnce({ deleted: 500, has_more: true })
      .mockRejectedValueOnce(new Error("locked"))
      .mockResolvedValueOnce({ deleted: 500, has_more: false });
    const { result } = renderCleanupHook({ previewTable, deleteBatch });
    await previewBillingFacts(result);
    await act(() => result.current.start());

    expect(result.current.run.tables[0]).toMatchObject({ deleted: 500, to_delete: 1_000 });
    await act(() => result.current.retry());

    expect(result.current.run.tables[0]).toMatchObject({ deleted: 1_000, to_delete: 1_000 });
  });

  it("skips zero-row tables and completes without batch requests", async () => {
    const deleteBatch = vi.fn();
    const { result } = renderCleanupHook({
      previewTable: vi.fn(async (table) => previewFor(table, 0)),
      deleteBatch,
    });
    await previewBillingFacts(result);
    await act(() => result.current.start());

    expect(deleteBatch).not.toHaveBeenCalled();
    expect(result.current.run.status).toBe("completed");
  });

  it("pauses when preview fails and can preview again after stop", async () => {
    const previewTable = vi
      .fn()
      .mockRejectedValueOnce(new Error("preview failed"))
      .mockImplementation(async (table: CleanupTableRef) => previewFor(table, 0));
    const { result } = renderCleanupHook({ previewTable });

    await previewBillingFacts(result);
    expect(result.current.run.status).toBe("paused");
    expect(result.current.run.tables.at(-1)?.status).toBe("failed");
    act(() => result.current.stop());
    await previewBillingFacts(result);

    expect(result.current.run.status).toBe("ready");
  });

  it("keeps actual progress below preview when the DAO finishes early", async () => {
    const deleteBatch = vi
      .fn()
      .mockResolvedValueOnce({ deleted: 2, has_more: false })
      .mockResolvedValueOnce({ deleted: 3, has_more: false });
    const { result } = renderCleanupHook({
      previewTable: vi.fn(async (table) => previewFor(table, 10)),
      deleteBatch,
    });
    await previewBillingFacts(result);
    await act(() => result.current.start());

    expect(result.current.run.status).toBe("completed");
    expect(result.current.run.deleted).toBe(2);
    expect(result.current.run.totalToDelete).toBe(10);
  });

  it("invalidates only cleanup-related query roots at a terminal state", async () => {
    const { result, queryClient } = renderCleanupHook({
      previewTable: vi.fn(async (table) => previewFor(table, 0)),
    });
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    await previewBillingFacts(result);
    await act(() => result.current.start());

    expect(cleanupQueryRoots).toEqual([
      "system-stats",
      "dashboard",
      "market-share",
      "metric-trend",
      "model-distribution",
      "billing-overview",
      "billing-token-list",
      "billing-channel-list",
      "billing-insights",
      "billing-token-daily",
      "billing-channel-daily",
      "billing-channel-model-breakdown",
      "byok-billing-overview",
      "byok-billing-by-channel",
      "byok-billing-by-model",
      "monitoring-insights",
      "insight",
      "logs",
      "logs-insights",
      "log-trace",
    ]);
    expect(invalidate.mock.calls.map(([filters]) => filters?.queryKey?.[0])).toEqual(cleanupQueryRoots);
    expect(invalidate).not.toHaveBeenCalledWith(expect.objectContaining({ queryKey: ["users"] }));
  });
});
