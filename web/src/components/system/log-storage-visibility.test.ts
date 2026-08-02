import { describe, expect, it } from "vitest";

import type { StorageStatus } from "@/lib/types";
import { shouldShowStorageMigration } from "./log-storage-visibility";

const storage = (overrides: Partial<StorageStatus> = {}): StorageStatus => ({
  core_db: {
    status: "available",
    path: "/data/core.db",
    schema_version: "12",
    last_error: "",
    size_bytes: 1024,
    open_connections: 1,
  },
  log_db: {
    status: "available",
    path: "/data/log.db",
    schema_version: "7",
    last_error: "",
    size_bytes: 2048,
    open_connections: 1,
  },
  legacy_db: {
    status: "unavailable",
    path: "",
    schema_version: "",
    last_error: "",
    size_bytes: 0,
    open_connections: 0,
  },
  legacy_artifact: {
    available: true,
    last_error: "",
    path: "",
    size_bytes: 0,
    exists: false,
    in_use: false,
    can_delete: false,
    delete_error: "",
  },
  log_delivery_queue: {
    pending: 0,
    retry: 0,
    inflight: 0,
    bytes: 0,
    oldest_seconds: 0,
    dropped: 0,
    last_error: "",
  },
  history_backfill: {
    state: "",
    source_kind: "none",
    source_path: "",
    source_size_bytes: 0,
    billing: { last_source_id: 0, processed_rows: 0, skipped: false },
    requests: { last_source_id: 0, processed_rows: 0, skipped: false },
    traces: { last_source_id: 0, processed_rows: 0, skipped: false },
    rows_per_second: 0,
    last_error: "",
    last_successful_at_unix: 0,
    can_complete: false,
    can_delete_source: false,
  },
  ...overrides,
});

describe("shouldShowStorageMigration", () => {
  it("hides a fresh installation and fully cleared migration without residual data", () => {
    expect(shouldShowStorageMigration(storage())).toBe(false);
    for (const sourceKind of ["none", ""] as const) {
      expect(shouldShowStorageMigration(storage({
        history_backfill: { ...storage().history_backfill, state: "completed", source_kind: sourceKind },
      }))).toBe(false);
    }
    expect(shouldShowStorageMigration(storage({
      history_backfill: { ...storage().history_backfill, state: "source_deleted" },
    }))).toBe(false);
  });

  it.each(["monolith", "v5_core"] as const)(
    "keeps a completed %s source visible even when the legacy database is unavailable",
    (sourceKind) => {
      expect(shouldShowStorageMigration(storage({
        history_backfill: {
          ...storage().history_backfill,
          state: "completed",
          source_kind: sourceKind,
        },
      }))).toBe(true);
    },
  );

  it.each(["completed", "source_deleted"] as const)(
    "shows legacy database errors in the terminal %s state",
    (state) => {
      expect(shouldShowStorageMigration(storage({
        legacy_db: { ...storage().legacy_db, last_error: "legacy database is locked" },
        history_backfill: { ...storage().history_backfill, state },
      }))).toBe(true);
    },
  );

  it("shows an active copying migration", () => {
    expect(shouldShowStorageMigration(storage({
      history_backfill: { ...storage().history_backfill, state: "copying" },
    }))).toBe(true);
  });

  it("keeps the migration section after source deletion while an artifact remains", () => {
    expect(shouldShowStorageMigration(storage({
      history_backfill: { ...storage().history_backfill, state: "source_deleted" },
      legacy_artifact: { ...storage().legacy_artifact, exists: true },
    }))).toBe(true);
  });

  it("shows residual legacy database and artifact diagnostics", () => {
    expect(shouldShowStorageMigration(storage({
      legacy_db: { ...storage().legacy_db, status: "available" },
    }))).toBe(true);
    expect(shouldShowStorageMigration(storage({
      legacy_artifact: { ...storage().legacy_artifact, last_error: "manifest missing" },
    }))).toBe(true);
    expect(shouldShowStorageMigration(storage({
      legacy_artifact: { ...storage().legacy_artifact, delete_error: "still in use" },
    }))).toBe(true);
  });
});
