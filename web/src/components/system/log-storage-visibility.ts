import type { StorageStatus } from "@/lib/types";

const ACTIVE_MIGRATION_STATES = new Set([
  "pending",
  "copying",
  "caught_up",
  "degraded",
]);

export function shouldShowStorageMigration(storage: StorageStatus): boolean {
  const artifact = storage.legacy_artifact;
  const history = storage.history_backfill;
  const completedLegacySource = history.state === "completed"
    && (history.source_kind === "monolith" || history.source_kind === "v5_core");

  return ACTIVE_MIGRATION_STATES.has(history.state)
    || completedLegacySource
    || storage.legacy_db.status === "available"
    || Boolean(storage.legacy_db.last_error)
    || artifact.exists
    || Boolean(artifact.last_error)
    || Boolean(artifact.delete_error);
}
