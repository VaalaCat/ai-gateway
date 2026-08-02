"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { deleteCleanupTableBatch, previewCleanupTable } from "@/lib/api/system";
import { ApiError } from "@/lib/api/client";
import {
  cleanupCategories,
  type CleanupCategoryID,
} from "@/lib/system-cleanup";
import type {
  CleanupBatchRequest,
  CleanupBatchResponse,
  CleanupTablePreview,
  CleanupTableRef,
} from "@/lib/types";

export const cleanupQueryRoots = [
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
] as const;

export type CleanupRunStatus =
  | "idle"
  | "previewing"
  | "ready"
  | "deleting"
  | "paused"
  | "stopped"
  | "completed";

export interface CleanupTableRun extends CleanupTablePreview {
  deleted: number;
  status: "pending" | "deleting" | "completed" | "failed";
  error?: string;
}

export interface CleanupRun {
  categoryID?: CleanupCategoryID;
  cutoffDate: string;
  status: CleanupRunStatus;
  tables: CleanupTableRun[];
  totalToDelete: number;
  deleted: number;
  activeTableIndex: number;
  failurePhase?: "preview" | "delete";
}

export interface DataCleanupDependencies {
  previewTable(input: CleanupTableRef & { cutoff_date: string }): Promise<CleanupTablePreview>;
  deleteBatch(input: CleanupBatchRequest): Promise<CleanupBatchResponse>;
}

const idleRun: CleanupRun = {
  cutoffDate: "",
  status: "idle",
  tables: [],
  totalToDelete: 0,
  deleted: 0,
  activeTableIndex: 0,
};

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function isStoppedError(error: unknown) {
  if (error instanceof DOMException && error.name === "AbortError") return true;
  if (!(error instanceof ApiError)) return false;
  const code = error.body?.code;
  return code === "CleanupTableNotAllowed"
    || code === "InvalidCleanupRequest"
    || code === "InvalidCleanupCutoff";
}

async function invalidateCleanupViews(queryClient: QueryClient) {
  await Promise.all(
    cleanupQueryRoots.map((key) =>
      queryClient.invalidateQueries({ queryKey: [key] }),
    ),
  );
}

async function deletePreviewedTable(
  table: CleanupTableRun,
  deleteBatch: DataCleanupDependencies["deleteBatch"],
  stopped: () => boolean,
  publishBatch: () => void,
) {
  while (!stopped()) {
    const batch = await deleteBatch({
      database: table.database,
      table: table.table,
      cutoff_date: table.cutoff_date,
      snapshot_max_key: table.snapshot_max_key,
    });
    table.deleted += batch.deleted;
    table.error = undefined;
    publishBatch();
    if (!batch.has_more) return;
  }
}

export function useDataCleanup(
  dependencies: DataCleanupDependencies = {
    previewTable: previewCleanupTable,
    deleteBatch: deleteCleanupTableBatch,
  },
) {
  const queryClient = useQueryClient();
  const { previewTable, deleteBatch } = dependencies;
  const [run, setRun] = useState<CleanupRun>(idleRun);
  const runRef = useRef(run);
  const stoppedRef = useRef(false);
  const mountedRef = useRef(true);
  const operationRef = useRef(0);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      stoppedRef.current = true;
      operationRef.current += 1;
    };
  }, []);

  const publish = useCallback((next: CleanupRun) => {
    runRef.current = next;
    if (mountedRef.current) setRun(next);
  }, []);

  const isActive = useCallback((operation: number) => (
    mountedRef.current && operationRef.current === operation
  ), []);

  const publishActive = useCallback((operation: number, next: CleanupRun) => {
    if (!isActive(operation)) return false;
    publish(next);
    return true;
  }, [isActive, publish]);

  const finish = useCallback(async (operation: number, next: CleanupRun) => {
    if (!publishActive(operation, next)) return;
    await invalidateCleanupViews(queryClient);
  }, [publishActive, queryClient]);

  const execute = useCallback(async (base: CleanupRun, operation: number) => {
    const tables = base.tables.map((table) => ({ ...table }));
    if (!publishActive(operation, { ...base, tables, status: "deleting", failurePhase: undefined })) return;

    for (let index = base.activeTableIndex; index < tables.length; index += 1) {
      const table = tables[index];
      if (stoppedRef.current) {
        await finish(operation, { ...runRef.current, tables, status: "stopped", activeTableIndex: index });
        return;
      }
      if (table.status === "completed" || table.to_delete === 0) {
        table.status = "completed";
        publishActive(operation, { ...runRef.current, tables: [...tables], activeTableIndex: index });
        continue;
      }

      table.status = "deleting";
      publishActive(operation, { ...runRef.current, tables: [...tables], activeTableIndex: index });
      try {
        await deletePreviewedTable(
          table,
          deleteBatch,
          () => stoppedRef.current || !isActive(operation),
          () => {
            publishActive(operation, {
              ...runRef.current,
              tables: [...tables],
              deleted: tables.reduce((total, item) => total + item.deleted, 0),
              activeTableIndex: index,
            });
          },
        );
      } catch (error) {
        table.status = "failed";
        table.error = errorMessage(error);
        await finish(operation, {
          ...runRef.current,
          tables: [...tables],
          status: isStoppedError(error) ? "stopped" : "paused",
          activeTableIndex: index,
          failurePhase: "delete",
        });
        return;
      }
      if (stoppedRef.current) {
        await finish(operation, { ...runRef.current, tables: [...tables], status: "stopped", activeTableIndex: index });
        return;
      }
      table.status = "completed";
      publishActive(operation, { ...runRef.current, tables: [...tables], activeTableIndex: index + 1 });
    }

    await finish(operation, { ...runRef.current, tables: [...tables], status: "completed", activeTableIndex: tables.length });
  }, [deleteBatch, finish, isActive, publishActive]);

  const preview = useCallback(async (categoryID: CleanupCategoryID, cutoffDate: string) => {
    const category = cleanupCategories.find((item) => item.id === categoryID);
    if (!category) return;
    const operation = operationRef.current + 1;
    operationRef.current = operation;
    stoppedRef.current = false;
    const tables: CleanupTableRun[] = [];
    publish({ categoryID, cutoffDate, status: "previewing", tables, totalToDelete: 0, deleted: 0, activeTableIndex: 0 });

    for (let index = 0; index < category.tables.length; index += 1) {
      const table = category.tables[index];
      try {
        const result = await previewTable({ ...table, cutoff_date: cutoffDate });
        if (!isActive(operation)) return;
        if (stoppedRef.current) {
          await finish(operation, { ...runRef.current, tables: [...tables], status: "stopped", activeTableIndex: index });
          return;
        }
        tables.push({ ...result, deleted: 0, status: "pending" });
        publishActive(operation, {
          ...runRef.current,
          tables: [...tables],
          totalToDelete: tables.reduce((total, item) => total + item.to_delete, 0),
          activeTableIndex: index,
        });
      } catch (error) {
        if (!isActive(operation)) return;
        tables.push({
          ...table,
          cutoff_date: cutoffDate,
          total: 0,
          to_delete: 0,
          snapshot_max_key: "",
          deleted: 0,
          status: "failed",
          error: errorMessage(error),
        });
        const status = isStoppedError(error) ? "stopped" : "paused";
        publishActive(operation, {
          ...runRef.current,
          tables: [...tables],
          status,
          activeTableIndex: index,
          failurePhase: "preview",
        });
        return;
      }
    }
    publishActive(operation, { ...runRef.current, tables: [...tables], status: "ready", activeTableIndex: 0, failurePhase: undefined });
  }, [finish, isActive, previewTable, publish, publishActive]);

  const start = useCallback(async () => {
    const current = runRef.current;
    if (current.status !== "ready") return;
    const operation = operationRef.current + 1;
    operationRef.current = operation;
    stoppedRef.current = false;
    await execute(current, operation);
  }, [execute]);

  const stop = useCallback(() => {
    stoppedRef.current = true;
    const current = runRef.current;
    if (current.status !== "deleting" && current.status !== "previewing") {
      publish({ ...current, status: "stopped" });
    }
  }, [publish]);

  const retry = useCallback(async () => {
    const current = runRef.current;
    const index = current.tables.findIndex((table) => table.status === "failed");
    if (current.status !== "paused" || index < 0) return;
    if (current.failurePhase === "preview") {
      if (current.categoryID) await preview(current.categoryID, current.cutoffDate);
      return;
    }
    const operation = operationRef.current + 1;
    operationRef.current = operation;
    stoppedRef.current = false;
    const failed = current.tables[index];
    publish({ ...current, status: "deleting", failurePhase: undefined });
    try {
      const result = await previewTable({
        database: failed.database,
        table: failed.table,
        cutoff_date: current.cutoffDate,
      });
      if (!isActive(operation)) return;
      if (stoppedRef.current) {
        await finish(operation, { ...current, status: "stopped" });
        return;
      }
      const tables = current.tables.map((table, tableIndex) =>
        tableIndex === index
          ? {
              ...result,
              deleted: table.deleted,
              to_delete: table.deleted + result.to_delete,
              status: "pending" as const,
            }
          : { ...table },
      );
      const next = {
        ...current,
        tables,
        status: "ready" as const,
        activeTableIndex: index,
        totalToDelete: tables.reduce((total, table) => total + table.to_delete, 0),
      };
      publishActive(operation, next);
      await execute(next, operation);
    } catch (error) {
      if (!isActive(operation)) return;
      const tables = current.tables.map((table, tableIndex) =>
        tableIndex === index ? { ...table, error: errorMessage(error) } : table,
      );
      await finish(operation, {
        ...current,
        tables,
        status: isStoppedError(error) ? "stopped" : "paused",
        activeTableIndex: index,
        failurePhase: "delete",
      });
    }
  }, [execute, finish, isActive, preview, previewTable, publish, publishActive]);

  const reset = useCallback(() => {
    stoppedRef.current = true;
    operationRef.current += 1;
    publish({ ...idleRun });
  }, [publish]);

  return { run, preview, start, stop, retry, reset };
}
