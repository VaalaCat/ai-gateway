import type { CleanupTableRef, TableStats } from "@/lib/types";

export type CleanupCategoryID =
  | "requestLogs"
  | "requestTraces"
  | "observability"
  | "billingFacts"
  | "billingAggregates";

export interface CleanupCategory {
  readonly id: CleanupCategoryID;
  readonly tables: readonly CleanupTableRef[];
}

export interface CleanupCategoryRow extends CleanupCategory {
  readonly count: number;
}

export const cleanupCategories: readonly CleanupCategory[] = [
  {
    id: "requestLogs",
    tables: [{ database: "log", table: "request_logs" }],
  },
  {
    id: "requestTraces",
    tables: [{ database: "log", table: "request_traces" }],
  },
  {
    id: "observability",
    tables: [
      { database: "log", table: "usage_hourly_buckets" },
      { database: "log", table: "usage_duration_histograms" },
      { database: "log", table: "usage_ttft_histograms" },
      { database: "log", table: "usage_tps_histograms" },
      { database: "log", table: "usage_user_ttft_histograms" },
      { database: "log", table: "usage_user_tps_histograms" },
    ],
  },
  {
    id: "billingFacts",
    tables: [{ database: "core", table: "billing_logs" }],
  },
  {
    id: "billingAggregates",
    tables: [
      { database: "log", table: "token_daily_billings" },
      { database: "log", table: "channel_daily_billings" },
    ],
  },
] as const;

export function buildCleanupCategoryRows(
  stats: readonly TableStats[],
): CleanupCategoryRow[] {
  const counts = new Map(
    stats.map((table) => [`${table.database}:${table.name}`, table.count]),
  );
  return cleanupCategories.map((category) => ({
    ...category,
    count: category.tables.reduce(
      (total, table) => total + (counts.get(`${table.database}:${table.table}`) ?? 0),
      0,
    ),
  }));
}
