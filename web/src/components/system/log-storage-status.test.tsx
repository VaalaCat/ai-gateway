import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import type { StorageStatus } from "@/lib/types";
import { LogStorageStatus } from "./log-storage-status";

const retryQueueMutate = vi.fn();
const clearQueueMutate = vi.fn();
const retryHistoryMutate = vi.fn();
const skipHistoryMutate = vi.fn();
const completeHistoryMutate = vi.fn();
const deleteLegacySourceMutate = vi.fn();
const deleteLegacyArtifactMutate = vi.fn();
let retryQueuePending = false;
let deleteArtifactPending = false;

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, unknown>) =>
    values ? `${key}:${JSON.stringify(values)}` : key,
}));

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

vi.mock("@/lib/api/system", () => ({
  useRetryLogQueue: () => ({ mutate: retryQueueMutate, isPending: retryQueuePending }),
  useClearLogBacklog: () => ({ mutate: clearQueueMutate, isPending: false }),
  useRetryHistoryBackfill: () => ({ mutate: retryHistoryMutate, isPending: false }),
  useSkipHistoryBackfill: () => ({ mutate: skipHistoryMutate, isPending: false }),
  useCompleteHistoryBackfill: () => ({ mutate: completeHistoryMutate, isPending: false }),
  useDeleteLegacySource: () => ({ mutate: deleteLegacySourceMutate, isPending: false }),
  useDeleteLegacyArtifact: () => ({ mutate: deleteLegacyArtifactMutate, isPending: deleteArtifactPending }),
}));

const LONG_LEGACY_PATH = `/data/${"legacy-history/".repeat(16)}master.db`;

const STORAGE: StorageStatus = {
  core_db: {
    status: "available",
    path: "/data/core.db",
    schema_version: "12",
    last_error: "",
    size_bytes: 1024,
    open_connections: 1,
  },
  log_db: {
    status: "unavailable",
    path: "/data/log.db",
    schema_version: "7",
    last_error: "database is locked",
    size_bytes: 2048,
    open_connections: 0,
  },
  legacy_db: {
    status: "available",
    path: LONG_LEGACY_PATH,
    schema_version: "5",
    last_error: "",
    size_bytes: 8192,
    open_connections: 0,
  },
  legacy_artifact: {
    available: true,
    last_error: "",
    path: "/data/master.db.20260725.pre-split.bak",
    size_bytes: 4096,
    exists: true,
    in_use: false,
    can_delete: true,
    delete_error: "",
  },
  log_delivery_queue: {
    pending: 9,
    retry: 3,
    inflight: 1,
    bytes: 4096,
    oldest_seconds: 61,
    dropped: 2,
    last_error: "write failed",
  },
  history_backfill: {
    state: "caught_up",
    source_kind: "monolith",
    source_path: LONG_LEGACY_PATH,
    source_size_bytes: 8192,
    billing: { last_source_id: 90, processed_rows: 80, skipped: false },
    requests: { last_source_id: 70, processed_rows: 60, skipped: false },
    traces: { last_source_id: 50, processed_rows: 40, skipped: true },
    rows_per_second: 12.5,
    last_error: "",
    last_successful_at_unix: 1_800_000_098,
    can_complete: true,
    can_delete_source: false,
  },
};

beforeEach(() => {
  retryQueueMutate.mockReset();
  clearQueueMutate.mockReset();
  retryHistoryMutate.mockReset();
  skipHistoryMutate.mockReset();
  completeHistoryMutate.mockReset();
  deleteLegacySourceMutate.mockReset();
  deleteLegacyArtifactMutate.mockReset();
  retryQueuePending = false;
  deleteArtifactPending = false;
});

it("shows caught-up source and requires explicit completion", async () => {
  const user = userEvent.setup();
  render(<LogStorageStatus storage={STORAGE} />);

  expect(screen.getByText("historyState.caught_up")).toBeInTheDocument();
  expect(screen.getByText("sourceKind.monolith")).toBeInTheDocument();
  expect(screen.getAllByText(LONG_LEGACY_PATH).length).toBeGreaterThan(0);
  expect(screen.getByText("90")).toBeInTheDocument();
  expect(screen.getByText("80")).toBeInTheDocument();
  expect(screen.getByText("12.5 rows/s")).toBeInTheDocument();
  expect(screen.getByText("skipped")).toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: "completeHistory" }));
  expect(completeHistoryMutate).not.toHaveBeenCalled();
  expect(screen.getByRole("alertdialog")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "confirmCompleteHistory" }));

  await waitFor(() => expect(completeHistoryMutate).toHaveBeenCalledWith(
    { confirm: true },
    expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
  ));
  const options = completeHistoryMutate.mock.calls[0][1];
  options.onSuccess();
  options.onError();
  const { toast } = await import("sonner");
  expect(toast.success).toHaveBeenCalledWith("completeHistorySuccess");
  expect(toast.error).toHaveBeenCalledWith("completeHistoryError");
});

it("renders a degraded migration error and offers retry", async () => {
  render(<LogStorageStatus storage={{
    ...STORAGE,
    history_backfill: {
      ...STORAGE.history_backfill,
      state: "degraded",
      last_error: "legacy source became unavailable",
      can_complete: false,
    },
  }} />);

  expect(screen.getByText("historyState.degraded")).toBeInTheDocument();
  expect(screen.getByText("legacy source became unavailable")).toHaveClass("break-words");
  await userEvent.click(screen.getByRole("button", { name: "retryHistory" }));
  expect(retryHistoryMutate).toHaveBeenCalledWith(undefined, expect.objectContaining({
    onSuccess: expect.any(Function),
    onError: expect.any(Function),
  }));
});

it("requires the exact DELETE confirmation before removing a completed source", async () => {
  const user = userEvent.setup();
  render(<LogStorageStatus storage={{
    ...STORAGE,
    history_backfill: {
      ...STORAGE.history_backfill,
      state: "completed",
      can_complete: false,
      can_delete_source: true,
    },
  }} />);

  await user.click(screen.getByRole("button", { name: "deleteLegacySource" }));
  const dialog = screen.getByRole("alertdialog");
  expect(within(dialog).getByText(LONG_LEGACY_PATH)).toHaveClass("min-w-0", "break-all", "font-mono", "text-xs");
  expect(within(dialog).getByText("8.0 KB")).toBeInTheDocument();
  const confirmation = screen.getByLabelText("deleteSourceConfirmation");
  const deleteButton = screen.getByRole("button", { name: "confirmDeleteLegacySource" });
  expect(deleteButton).toBeDisabled();

  await user.type(confirmation, "delete");
  expect(deleteButton).toBeDisabled();
  await user.clear(confirmation);
  await user.type(confirmation, "DELETE");
  expect(deleteButton).toBeEnabled();
  await user.click(deleteButton);

  await waitFor(() => expect(deleteLegacySourceMutate).toHaveBeenCalledWith(
    { confirmation: "DELETE" },
    expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
  ));
});

it("keeps source_deleted read-only while artifact cleanup remains independent", async () => {
  const user = userEvent.setup();
  render(<LogStorageStatus storage={{
    ...STORAGE,
    legacy_db: { ...STORAGE.legacy_db, status: "unavailable", size_bytes: 0 },
    history_backfill: {
      ...STORAGE.history_backfill,
      state: "source_deleted",
      source_size_bytes: 0,
      can_complete: false,
      can_delete_source: false,
    },
  }} />);

  expect(screen.getByText("historyState.source_deleted")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "completeHistory" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "deleteLegacySource" })).not.toBeInTheDocument();
  expect(screen.getByText("/data/master.db.20260725.pre-split.bak")).toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: "deleteLegacyArtifact" }));
  const dialog = screen.getByRole("alertdialog");
  expect(within(dialog).getByText("/data/master.db.20260725.pre-split.bak")).toHaveClass("min-w-0", "break-all", "font-mono", "text-xs");
  expect(within(dialog).getByText("4.0 KB")).toBeInTheDocument();
  const confirmation = screen.getByLabelText("deleteArtifactConfirmation");
  const deleteButton = screen.getByRole("button", { name: "confirmDeleteLegacyArtifact" });
  expect(deleteButton).toBeDisabled();
  await user.type(confirmation, "DELETE");
  await user.click(deleteButton);
  expect(deleteLegacyArtifactMutate).toHaveBeenCalledWith(
    { confirmation: "DELETE" },
    expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
  );
});

it("shows unavailable artifact diagnostics without coupling it to migration state", () => {
  render(<LogStorageStatus storage={{
    ...STORAGE,
    legacy_artifact: {
      available: false,
      last_error: "manifest backup path is invalid",
      path: "",
      size_bytes: 0,
      exists: false,
      in_use: false,
      can_delete: false,
      delete_error: "",
    },
  }} />);

  expect(screen.getByText("manifest backup path is invalid")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "deleteLegacyArtifact" })).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "completeHistory" })).toBeEnabled();
});

it("uses artifact eligibility instead of migration state or in_use guesses", () => {
  render(<LogStorageStatus storage={{
    ...STORAGE,
    legacy_artifact: {
      ...STORAGE.legacy_artifact,
      in_use: false,
      can_delete: false,
      delete_error: "log database is unavailable",
    },
    history_backfill: { ...STORAGE.history_backfill, state: "copying", can_complete: false },
  }} />);

  expect(screen.getByText("log database is unavailable")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "deleteLegacyArtifact" })).toBeDisabled();
});

it("resets destructive confirmation and disables artifact deletion while pending", async () => {
  const user = userEvent.setup();
  const { rerender } = render(<LogStorageStatus storage={STORAGE} />);
  const trigger = screen.getByRole("button", { name: "deleteLegacyArtifact" });

  await user.click(trigger);
  const confirmation = screen.getByLabelText("deleteArtifactConfirmation");
  expect(confirmation).toHaveClass("text-base", "md:text-sm");
  await user.type(confirmation, "DELETE");
  expect(screen.getByRole("button", { name: "confirmDeleteLegacyArtifact" })).toBeEnabled();
  await user.click(screen.getByRole("button", { name: "cancel" }));
  await user.click(trigger);
  expect(screen.getByLabelText("deleteArtifactConfirmation")).toHaveValue("");
  expect(screen.getByRole("button", { name: "confirmDeleteLegacyArtifact" })).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "cancel" }));

  deleteArtifactPending = true;
  rerender(<LogStorageStatus storage={STORAGE} />);
  expect(screen.getByRole("button", { name: "deleteLegacyArtifact" })).toBeDisabled();
});

it.each(["completed", "degraded"] as const)("does not invent percentage progress for %s", (state) => {
  render(<LogStorageStatus storage={{
    ...STORAGE,
    history_backfill: { ...STORAGE.history_backfill, state, can_complete: false },
  }} />);

  expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  expect(screen.queryByText(state === "completed" ? "90%" : "0%")).not.toBeInTheDocument();
});

it("uses wrapping containers and break-all paths for narrow viewports", () => {
  render(<LogStorageStatus storage={STORAGE} />);

  for (const path of screen.getAllByText(LONG_LEGACY_PATH)) {
    expect(path).toHaveClass("break-all", "font-mono", "text-xs");
  }
  expect(screen.getByTestId("history-actions")).toHaveClass("flex-wrap");
  expect(screen.getByTestId("storage-database-grid")).toHaveClass("min-w-0");
});

it("keeps operational databases and queue visible while grouping legacy data in migration", () => {
  render(<LogStorageStatus storage={STORAGE} />);

  const operationalDatabases = screen.getByTestId("storage-database-grid");
  expect(within(operationalDatabases).getByText("/data/core.db")).toBeInTheDocument();
  expect(within(operationalDatabases).getByText("/data/log.db")).toBeInTheDocument();
  expect(within(operationalDatabases).queryByText(LONG_LEGACY_PATH)).not.toBeInTheDocument();
  expect(within(screen.getByTestId("storage-migration")).getAllByText(LONG_LEGACY_PATH).length).toBeGreaterThan(0);
  expect(screen.getByText("database is locked")).toBeInTheDocument();
  for (const value of ["9", "3", "1", "4.0 KB", "1m 1s", "2", "write failed"]) {
    expect(screen.getAllByText(value).length).toBeGreaterThan(0);
  }
});

it("keeps existing queue confirmations and pending guards", async () => {
  retryQueuePending = true;
  const { rerender } = render(<LogStorageStatus storage={STORAGE} />);
  expect(screen.getByRole("button", { name: "retryQueue" })).toBeDisabled();

  retryQueuePending = false;
  rerender(<LogStorageStatus storage={STORAGE} />);
  await userEvent.click(screen.getByRole("button", { name: "clearBacklog" }));
  expect(clearQueueMutate).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "confirmClearBacklog" }));
  await waitFor(() => expect(clearQueueMutate).toHaveBeenCalledWith(
    { confirm: true },
    expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
  ));
});
