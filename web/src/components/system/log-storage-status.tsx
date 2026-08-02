"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { CheckCircle2, Database, RefreshCw, SkipForward, Trash2 } from "lucide-react";
import { toast } from "sonner";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import {
  useClearLogBacklog,
  useCompleteHistoryBackfill,
  useDeleteLegacyArtifact,
  useDeleteLegacySource,
  useRetryHistoryBackfill,
  useRetryLogQueue,
  useSkipHistoryBackfill,
} from "@/lib/api/system";
import type { DatabaseStatus, HistoryCursorStatus, StorageStatus } from "@/lib/types";
import { formatFileSize, formatUptime } from "@/lib/utils/format";
import { shouldShowStorageMigration } from "./log-storage-visibility";

type Translator = ReturnType<typeof useTranslations<"system.logStorage">>;

function StatusValue({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words text-sm tabular-nums">{value}</dd>
    </div>
  );
}

function PathValue({ path }: { path: string }) {
  return <span className="min-w-0 break-all font-mono text-xs">{path || "—"}</span>;
}

function DatabaseStatusPanel({ title, data, t }: { title: string; data: DatabaseStatus; t: Translator }) {
  return (
    <section className="flex min-w-0 flex-col gap-3 border-t pt-3 first:border-t-0 first:pt-0 md:border-t-0 md:pt-0">
      <div className="flex min-w-0 items-center justify-between gap-2">
        <h3 className="min-w-0 text-sm font-medium">{title}</h3>
        <Badge variant={data.status === "available" ? "secondary" : "destructive"}>
          {t(data.status)}
        </Badge>
      </div>
      <dl className="grid min-w-0 grid-cols-2 gap-3">
        <StatusValue label={t("path")} value={<PathValue path={data.path} />} />
        <StatusValue label={t("size")} value={formatFileSize(data.size_bytes)} />
        <StatusValue label={t("schemaVersion")} value={data.schema_version || "—"} />
        <StatusValue label={t("openConnections")} value={data.open_connections} />
      </dl>
      {data.last_error && <p className="break-words text-xs text-destructive">{data.last_error}</p>}
    </section>
  );
}

function CursorStatus({ label, cursor, t }: { label: string; cursor: HistoryCursorStatus; t: Translator }) {
  return (
    <section className="flex min-w-0 flex-col gap-2 border-t pt-3 first:border-t-0 first:pt-0 sm:border-t-0 sm:pt-0">
      <div className="flex min-w-0 items-center justify-between gap-2">
        <h4 className="text-sm font-medium">{label}</h4>
        <Badge variant={cursor.skipped ? "outline" : "secondary"}>
          {t(cursor.skipped ? "skipped" : "active")}
        </Badge>
      </div>
      <dl className="grid min-w-0 grid-cols-2 gap-3">
        <StatusValue label={t("historyCursor")} value={cursor.last_source_id} />
        <StatusValue label={t("historyProcessed")} value={cursor.processed_rows} />
      </dl>
    </section>
  );
}

interface DeleteConfirmationDialogProps {
  triggerLabel: string;
  title: string;
  description: string;
  targetPath: string;
  targetSize: number;
  targetLabel: string;
  sizeLabel: string;
  inputID: string;
  inputLabel: string;
  confirmLabel: string;
  cancelLabel: string;
  disabled?: boolean;
  pending: boolean;
  onDelete: () => void;
}

function DeleteConfirmationDialog({
  triggerLabel,
  title,
  description,
  targetPath,
  targetSize,
  targetLabel,
  sizeLabel,
  inputID,
  inputLabel,
  confirmLabel,
  cancelLabel,
  disabled,
  pending,
  onDelete,
}: DeleteConfirmationDialogProps) {
  const [open, setOpen] = useState(false);
  const [confirmation, setConfirmation] = useState("");

  const handleOpenChange = (nextOpen: boolean) => {
    setOpen(nextOpen);
    if (!nextOpen) setConfirmation("");
  };

  return (
    <AlertDialog open={open} onOpenChange={handleOpenChange}>
      <AlertDialogTrigger asChild>
        <Button type="button" variant="destructive" size="sm" disabled={disabled || pending} className="w-full sm:w-auto">
          <Trash2 data-icon="inline-start" />
          {triggerLabel}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <dl className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-[minmax(0,1fr)_auto]">
          <StatusValue label={targetLabel} value={<PathValue path={targetPath} />} />
          <StatusValue label={sizeLabel} value={formatFileSize(targetSize)} />
        </dl>
        <Field>
          <FieldLabel htmlFor={inputID}>{inputLabel}</FieldLabel>
          <Input
            id={inputID}
            aria-label={inputLabel}
            autoComplete="off"
            value={confirmation}
            onChange={(event) => setConfirmation(event.target.value)}
            placeholder="DELETE"
          />
        </Field>
        <AlertDialogFooter>
          <AlertDialogCancel>{cancelLabel}</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            aria-label={confirmLabel}
            disabled={pending || confirmation !== "DELETE"}
            onClick={onDelete}
          >
            {confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

export function LogStorageStatus({ storage }: { storage?: StorageStatus }) {
  const t = useTranslations("system.logStorage");
  const retryQueue = useRetryLogQueue();
  const clearQueue = useClearLogBacklog();
  const retryHistory = useRetryHistoryBackfill();
  const skipHistory = useSkipHistoryBackfill();
  const completeHistory = useCompleteHistoryBackfill();
  const deleteLegacySource = useDeleteLegacySource();
  const deleteLegacyArtifact = useDeleteLegacyArtifact();

  if (!storage) return null;

  const queue = storage.log_delivery_queue;
  const history = storage.history_backfill;
  const artifact = storage.legacy_artifact;
  const showMigration = shouldShowStorageMigration(storage);
  const historyTerminal = history.state === "completed" || history.state === "source_deleted" || history.state === "";
  const logsSkipped = history.requests.skipped && history.traces.skipped;
  const databases = [
    { key: "core", title: t("coreDatabase"), data: storage.core_db },
    { key: "log", title: t("logDatabase"), data: storage.log_db },
  ];

  return (
    <Card className="min-w-0 rounded-[8px]">
      <CardHeader className="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex min-w-0 flex-col gap-1">
          <CardTitle className="flex items-center gap-2 text-heading">
            <Database className="size-5" />
            {t("title")}
          </CardTitle>
          <CardDescription>{t("description")}</CardDescription>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  type="button"
                  variant="outline"
                  size="icon-sm"
                  aria-label={t("retryQueue")}
                  disabled={retryQueue.isPending || queue.pending + queue.retry === 0}
                  onClick={() => retryQueue.mutate(undefined, {
                    onSuccess: () => toast.success(t("retryQueueSuccess")),
                    onError: () => toast.error(t("retryQueueError")),
                  })}
                >
                  <RefreshCw data-icon="inline-start" className={retryQueue.isPending ? "animate-spin" : undefined} />
                </Button>
              </TooltipTrigger>
              <TooltipContent>{t("retryQueue")}</TooltipContent>
            </Tooltip>
          </TooltipProvider>
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button type="button" variant="destructive" size="sm" disabled={clearQueue.isPending || queue.pending + queue.retry === 0}>
                <Trash2 data-icon="inline-start" />
                {t("clearBacklog")}
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>{t("confirmClearTitle")}</AlertDialogTitle>
                <AlertDialogDescription>{t("confirmClearDescription", { count: queue.pending + queue.retry })}</AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
                <AlertDialogAction
                  variant="destructive"
                  aria-label={t("confirmClearBacklog")}
                  onClick={() => clearQueue.mutate({ confirm: true }, {
                    onSuccess: () => toast.success(t("clearBacklogSuccess")),
                    onError: () => toast.error(t("clearBacklogError")),
                  })}
                >
                  {t("confirmClearBacklog")}
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </div>
      </CardHeader>

      <CardContent className="flex min-w-0 flex-col gap-5">
        <div data-testid="storage-database-grid" className="grid min-w-0 gap-4 md:grid-cols-2">
          {databases.map(({ key, title, data }) => (
            <DatabaseStatusPanel key={key} title={title} data={data} t={t} />
          ))}
        </div>

        <section className="flex min-w-0 flex-col gap-3 border-t pt-4">
          <h3 className="text-sm font-medium">{t("queue")}</h3>
          <dl className="grid min-w-0 grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-7">
            <StatusValue label={t("pending")} value={queue.pending} />
            <StatusValue label={t("retry")} value={queue.retry} />
            <StatusValue label={t("inflight")} value={queue.inflight} />
            <StatusValue label={t("bytes")} value={formatFileSize(queue.bytes)} />
            <StatusValue label={t("oldest")} value={formatUptime(queue.oldest_seconds)} />
            <StatusValue label={t("dropped")} value={queue.dropped} />
            <StatusValue label={t("lastError")} value={queue.last_error || "—"} />
          </dl>
        </section>

        {showMigration && (
          <section data-testid="storage-migration" className="flex min-w-0 flex-col gap-5 border-t pt-4">
            <h3 className="text-sm font-medium">{t("migration")}</h3>
            <DatabaseStatusPanel title={t("legacyDatabase")} data={storage.legacy_db} t={t} />

            <section className="flex min-w-0 flex-col gap-3 border-t pt-4">
              <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
                <h3 className="text-sm font-medium">{t("legacyArtifact")}</h3>
                <Badge variant={!artifact.available ? "destructive" : artifact.exists ? "secondary" : "outline"}>
                  {t(!artifact.available ? "unavailable" : artifact.exists ? "exists" : "missing")}
                </Badge>
              </div>
              {artifact.path && (
                <dl className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-3">
                  <StatusValue label={t("path")} value={<PathValue path={artifact.path} />} />
                  <StatusValue label={t("size")} value={formatFileSize(artifact.size_bytes)} />
                  <StatusValue label={t("artifactUse")} value={t(artifact.in_use ? "inUse" : "notInUse")} />
                </dl>
              )}
              {artifact.last_error && <p className="break-words text-xs text-destructive">{artifact.last_error}</p>}
              {artifact.delete_error && <p className="break-words text-xs text-destructive">{artifact.delete_error}</p>}
              {artifact.available && artifact.exists && (
                <div className="flex flex-wrap">
                  <DeleteConfirmationDialog
                    triggerLabel={t("deleteLegacyArtifact")}
                    title={t("confirmDeleteArtifactTitle")}
                    description={t("confirmDeleteArtifactDescription")}
                    targetPath={artifact.path}
                    targetSize={artifact.size_bytes}
                    targetLabel={t("path")}
                    sizeLabel={t("size")}
                    inputID="delete-legacy-artifact-confirmation"
                    inputLabel={t("deleteArtifactConfirmation")}
                    confirmLabel={t("confirmDeleteLegacyArtifact")}
                    cancelLabel={t("cancel")}
                    disabled={!artifact.can_delete}
                    pending={deleteLegacyArtifact.isPending}
                    onDelete={() => deleteLegacyArtifact.mutate({ confirmation: "DELETE" }, {
                      onSuccess: () => toast.success(t("deleteLegacyArtifactSuccess")),
                      onError: () => toast.error(t("deleteLegacyArtifactError")),
                    })}
                  />
                </div>
              )}
            </section>

            {history.state && (
          <section data-testid="history-backfill" className="flex min-w-0 flex-col gap-4 border-t pt-4">
            <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <h3 className="text-sm font-medium">{t("history")}</h3>
                <Badge data-testid="history-backfill-state" data-state-value={history.state} variant={history.state === "degraded" ? "destructive" : "secondary"}>
                  {t(`historyState.${history.state}`)}
                </Badge>
              </div>
              <div data-testid="history-actions" className="flex w-full flex-col flex-wrap gap-2 sm:w-auto sm:flex-row">
                {!historyTerminal && history.state !== "caught_up" && (
                  <TooltipProvider>
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <Button
                          type="button"
                          variant="outline"
                          size="icon-sm"
                          aria-label={t("retryHistory")}
                          disabled={retryHistory.isPending}
                          onClick={() => retryHistory.mutate(undefined, {
                            onSuccess: () => toast.success(t("retryHistorySuccess")),
                            onError: () => toast.error(t("retryHistoryError")),
                          })}
                        >
                          <RefreshCw data-icon="inline-start" className={retryHistory.isPending ? "animate-spin" : undefined} />
                        </Button>
                      </TooltipTrigger>
                      <TooltipContent>{t("retryHistory")}</TooltipContent>
                    </Tooltip>
                  </TooltipProvider>
                )}
                {!historyTerminal && history.state !== "caught_up" && !logsSkipped && (
                  <AlertDialog>
                    <AlertDialogTrigger asChild>
                      <Button type="button" variant="outline" size="sm" disabled={skipHistory.isPending} className="w-full sm:w-auto">
                        <SkipForward data-icon="inline-start" />
                        {t("skipHistory")}
                      </Button>
                    </AlertDialogTrigger>
                    <AlertDialogContent>
                      <AlertDialogHeader>
                        <AlertDialogTitle>{t("confirmSkipHistoryTitle")}</AlertDialogTitle>
                        <AlertDialogDescription>{t("confirmSkipHistoryDescription")}</AlertDialogDescription>
                      </AlertDialogHeader>
                      <AlertDialogFooter>
                        <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
                        <AlertDialogAction
                          variant="destructive"
                          aria-label={t("confirmSkipHistory")}
                          onClick={() => skipHistory.mutate({ confirm: true }, {
                            onSuccess: () => toast.success(t("skipHistorySuccess")),
                            onError: () => toast.error(t("skipHistoryError")),
                          })}
                        >
                          {t("confirmSkipHistory")}
                        </AlertDialogAction>
                      </AlertDialogFooter>
                    </AlertDialogContent>
                  </AlertDialog>
                )}
                {history.state === "caught_up" && (
                  <AlertDialog>
                    <AlertDialogTrigger asChild>
                      <Button type="button" size="sm" disabled={!history.can_complete || completeHistory.isPending} className="w-full sm:w-auto">
                        <CheckCircle2 data-icon="inline-start" />
                        {t("completeHistory")}
                      </Button>
                    </AlertDialogTrigger>
                    <AlertDialogContent>
                      <AlertDialogHeader>
                        <AlertDialogTitle>{t("confirmCompleteHistoryTitle")}</AlertDialogTitle>
                        <AlertDialogDescription>{t("confirmCompleteHistoryDescription")}</AlertDialogDescription>
                      </AlertDialogHeader>
                      <AlertDialogFooter>
                        <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
                        <AlertDialogAction
                          aria-label={t("confirmCompleteHistory")}
                          onClick={() => completeHistory.mutate({ confirm: true }, {
                            onSuccess: () => toast.success(t("completeHistorySuccess")),
                            onError: () => toast.error(t("completeHistoryError")),
                          })}
                        >
                          {t("confirmCompleteHistory")}
                        </AlertDialogAction>
                      </AlertDialogFooter>
                    </AlertDialogContent>
                  </AlertDialog>
                )}
                {history.state === "completed" && (
                  <DeleteConfirmationDialog
                    triggerLabel={t("deleteLegacySource")}
                    title={t("confirmDeleteSourceTitle")}
                    description={t("confirmDeleteSourceDescription")}
                    targetPath={history.source_path}
                    targetSize={history.source_size_bytes}
                    targetLabel={t("path")}
                    sizeLabel={t("size")}
                    inputID="delete-legacy-source-confirmation"
                    inputLabel={t("deleteSourceConfirmation")}
                    confirmLabel={t("confirmDeleteLegacySource")}
                    cancelLabel={t("cancel")}
                    disabled={!history.can_delete_source}
                    pending={deleteLegacySource.isPending}
                    onDelete={() => deleteLegacySource.mutate({ confirmation: "DELETE" }, {
                      onSuccess: () => toast.success(t("deleteLegacySourceSuccess")),
                      onError: () => toast.error(t("deleteLegacySourceError")),
                    })}
                  />
                )}
              </div>
            </div>

            <dl className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
              <StatusValue label={t("historySourceKind")} value={t(`sourceKind.${history.source_kind || "none"}`)} />
              <StatusValue label={t("historySourcePath")} value={<PathValue path={history.source_path} />} />
              <StatusValue label={t("historySourceSize")} value={formatFileSize(history.source_size_bytes)} />
              <StatusValue label={t("historySpeed")} value={`${history.rows_per_second.toFixed(1)} rows/s`} />
              <StatusValue label={t("historyLastSuccess")} value={history.last_successful_at_unix > 0 ? new Date(history.last_successful_at_unix * 1000).toLocaleString() : "—"} />
            </dl>
            <div className="grid min-w-0 gap-4 sm:grid-cols-3">
              <CursorStatus label={t("historyBilling")} cursor={history.billing} t={t} />
              <CursorStatus label={t("historyRequests")} cursor={history.requests} t={t} />
              <CursorStatus label={t("historyTraces")} cursor={history.traces} t={t} />
            </div>
            {history.last_error && <p className="break-words text-xs text-destructive">{history.last_error}</p>}
          </section>
            )}
          </section>
        )}
      </CardContent>
    </Card>
  );
}
