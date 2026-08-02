import { describe, expect, it } from "vitest";
import { buildCleanupCategoryRows, cleanupCategories } from "./system-cleanup";

describe("system cleanup categories", () => {
  it("defines exactly five ordered historical-data categories", () => {
    expect(cleanupCategories.map((item) => item.id)).toEqual([
      "requestLogs",
      "requestTraces",
      "observability",
      "billingFacts",
      "billingAggregates",
    ]);
    expect(cleanupCategories.map((item) => [item.id, item.tables])).toEqual([
      ["requestLogs", [{ database: "log", table: "request_logs" }]],
      ["requestTraces", [{ database: "log", table: "request_traces" }]],
      ["observability", [
        { database: "log", table: "usage_hourly_buckets" },
        { database: "log", table: "usage_duration_histograms" },
        { database: "log", table: "usage_ttft_histograms" },
        { database: "log", table: "usage_tps_histograms" },
        { database: "log", table: "usage_user_ttft_histograms" },
        { database: "log", table: "usage_user_tps_histograms" },
      ]],
      ["billingFacts", [
        { database: "core", table: "billing_logs" },
      ]],
      ["billingAggregates", [
        { database: "log", table: "token_daily_billings" },
        { database: "log", table: "channel_daily_billings" },
      ]],
    ]);
    const keys = cleanupCategories.flatMap((item) =>
      item.tables.map((table) => `${table.database}:${table.table}`),
    );
    expect(new Set(keys).size).toBe(11);
    expect(cleanupCategories.find((item) => item.id === "billingFacts")?.tables).toEqual([
      { database: "core", table: "billing_logs" },
    ]);
  });

  it("never includes business tables in a cleanup category", () => {
    const tables = cleanupCategories.flatMap((category) =>
      category.tables.map((table) => `${table.database}:${table.table}`),
    );
    for (const denied of ["core:users", "core:tokens", "core:channels", "core:oauth_identities"]) {
      expect(tables).not.toContain(denied);
    }
  });

  it("sums exact physical table stats and keeps zero-row categories", () => {
    const rows = buildCleanupCategoryRows([
      { database: "log", name: "request_logs", count: 9 },
      { database: "core", name: "request_logs", count: 100 },
      { database: "core", name: "users", count: 999 },
    ]);

    expect(rows).toHaveLength(5);
    expect(rows.find((row) => row.id === "requestLogs")?.count).toBe(9);
    expect(rows.find((row) => row.id === "billingFacts")?.count).toBe(0);
  });
});
