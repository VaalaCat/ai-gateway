"use client";

import { useEffect, useEffectEvent, useMemo, useRef, useState } from "react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Progress } from "@/components/ui/progress";
import { DateRangeInputs, isDateRangeValid } from "@/components/business/date-range-inputs";
import {
  useInvalidateBillingCaches,
  useRebuildBillingJob,
  useRebuildBillingJobs,
  useRebuildBillingSubmit,
} from "@/lib/api/billing";
import { formatErrorToast } from "@/lib/api/error-toast";
import { localDateRangeToUTCRange } from "@/lib/utils/date-range";
import { cn } from "@/lib/utils";

interface RebuildDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** When set on open, dialog skips the form and tracks this job directly. */
  initialJobId?: string;
}

export function RebuildDialog({
  open,
  onOpenChange,
  initialJobId,
}: RebuildDialogProps) {
  const sessionKey = open ? `open:${initialJobId ?? ""}` : "closed";

  return (
    <RebuildDialogSession
      key={sessionKey}
      open={open}
      onOpenChange={onOpenChange}
      initialJobId={initialJobId}
    />
  );
}

function RebuildDialogSession({
  open,
  onOpenChange,
  initialJobId,
}: RebuildDialogProps) {
  const t = useTranslations("billing");
  const tc = useTranslations("common");

  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState("");
  const [jobOwnership, setJobOwnership] = useState(() => ({
    jobId: open ? initialJobId ?? null : null,
    ignoredAutomaticJobId: null as string | null,
  }));
  const jobId = jobOwnership.jobId;
  // When the picker is open and user clicks "new run", we override the
  // running-jobs gate to show the date form even though running jobs exist.
  const [forceForm, setForceForm] = useState(false);

  const submit = useRebuildBillingSubmit();
  // Only poll the list while dialog is open AND we're not already bound to a job.
  const jobsList = useRebuildBillingJobs({ enabled: open && !jobId });
  const invalidateBilling = useInvalidateBillingCaches();

  const runningJobs = useMemo(
    () =>
      (jobsList.data?.jobs ?? [])
        .filter((j) => j.status === "running")
        .sort((a, b) => b.started_at - a.started_at),
    [jobsList.data?.jobs],
  );

  const automaticJobId =
    open &&
    !jobId &&
    !forceForm &&
    runningJobs.length === 1 &&
    runningJobs[0].id !== jobOwnership.ignoredAutomaticJobId
      ? runningJobs[0].id
      : null;
  if (automaticJobId) {
    setJobOwnership((current) => ({ ...current, jobId: automaticJobId }));
  }
  const trackedJobId = jobId ?? automaticJobId;
  const job = useRebuildBillingJob(trackedJobId);

  const hasDate = !!(startDate || endDate);
  const validRange = isDateRangeValid(startDate, endDate);
  const isRunning = job.data?.status === "running";

  // View resolution: 1) bound to a specific job → progress; 2) running jobs
  // exist and user didn't ask for form → picker; 3) form.
  const showProgress = !!trackedJobId;
  const showPicker = !showProgress && !forceForm && runningJobs.length > 0;
  const showForm = !showProgress && !showPicker;

  const canSubmit = showForm && hasDate && validRange && !submit.isPending;

  const reset = () => {
    setStartDate("");
    setEndDate("");
    setJobOwnership({ jobId: null, ignoredAutomaticJobId: null });
    setForceForm(false);
  };

  const handleClose = (nextOpen: boolean) => {
    if (!nextOpen && !isRunning) {
      reset();
    }
    onOpenChange(nextOpen);
  };

  const handleSubmit = async () => {
    try {
      const utc = localDateRangeToUTCRange(startDate, endDate);
      const result = await submit.mutateAsync({
        ...(utc.from ? { start_date: utc.from } : {}),
        ...(utc.to ? { end_date: utc.to } : {}),
      });
      setJobOwnership((current) => ({ ...current, jobId: result.job_id }));
      setForceForm(false);
    } catch (e) {
      toast.error(formatErrorToast(e, t("rebuildFailed")));
    }
  };

  const handledTransition = useRef<string | null>(null);
  const clearTrackedJob = () => {
    setJobOwnership((current) => ({
      jobId: null,
      ignoredAutomaticJobId: current.jobId ?? trackedJobId,
    }));
  };
  const handleTerminalTransition = useEffectEvent(() => {
    if (job.data?.status === "succeeded") {
      toast.success(t("rebuildSuccess", { count: job.data.replayed_logs }));
      invalidateBilling();
      clearTrackedJob();
      onOpenChange(false);
      setStartDate("");
      setEndDate("");
    } else if (job.data?.status === "failed") {
      toast.error(`${t("rebuildFailed")}: ${job.data.error ?? ""}`);
      clearTrackedJob();
    } else if (job.data?.status === "canceled") {
      toast.warning(t("rebuildCanceled"));
      clearTrackedJob();
    } else if (job.isError) {
      toast.warning(t("rebuildJobLost"));
      clearTrackedJob();
    }
  });

  useEffect(() => {
    if (!trackedJobId) return;

    const status = job.data?.status;
    const terminalStatus =
      status === "succeeded" || status === "failed" || status === "canceled"
        ? status
        : job.isError
          ? "lost"
          : null;
    if (!terminalStatus) return;

    const transitionKey = `${trackedJobId}:${terminalStatus}`;
    if (handledTransition.current === transitionKey) return;
    handledTransition.current = transitionKey;
    handleTerminalTransition();
  }, [job.data?.status, job.isError, trackedJobId]);

  const progress = job.data
    ? Math.round(
        (job.data.done_slices / Math.max(job.data.total_slices, 1)) * 100,
      )
    : 0;

  const title = showProgress
    ? t("rebuildRunning")
    : showPicker
      ? t("rebuildPickerTitle")
      : t("rebuild");

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>

        {showForm && (
          <>
            <p className="text-sm text-muted-foreground">{t("rebuildHint")}</p>
            <DateRangeInputs
              startDate={startDate}
              endDate={endDate}
              onStartDateChange={setStartDate}
              onEndDateChange={setEndDate}
            />
            {!hasDate && (
              <p className="text-sm text-muted-foreground">{t("rebuildDateRequired")}</p>
            )}
          </>
        )}

        {showPicker && (
          <div className="space-y-2">
            <p className="text-sm text-muted-foreground">
              {t("rebuildPickerHint", { count: runningJobs.length })}
            </p>
            <ul className="divide-y divide-border rounded-md border">
              {runningJobs.map((rj) => {
                const pct = Math.round(
                  (rj.done_slices / Math.max(rj.total_slices, 1)) * 100,
                );
                return (
                  <li key={rj.id}>
                    <button
                      type="button"
                      onClick={() =>
                        setJobOwnership((current) => ({ ...current, jobId: rj.id }))
                      }
                      className={cn(
                        "group flex w-full items-center gap-3 px-3 py-2 text-left",
                        "transition-colors hover:bg-accent/60 focus:bg-accent/60 focus:outline-none",
                      )}
                    >
                      <span className="font-mono text-2xs text-muted-foreground">
                        {rj.id.slice(0, 8)}
                      </span>
                      <span className="flex-1 text-sm tabular-nums">
                        {t("rebuildSlicesDone", {
                          done: rj.done_slices,
                          total: rj.total_slices,
                        })}
                      </span>
                      <span className="text-sm font-medium tabular-nums">
                        {pct}%
                      </span>
                      <span
                        aria-hidden
                        className="ml-1 h-1 w-16 overflow-hidden rounded-full bg-primary/15"
                      >
                        <span
                          className="block h-full origin-left bg-primary transition-transform duration-300 ease-out"
                          style={{ transform: `scaleX(${pct / 100})` }}
                        />
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
            <button
              type="button"
              onClick={() => setForceForm(true)}
              className="text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
            >
              {t("rebuildPickerNew")}
            </button>
          </div>
        )}

        {showProgress && job.data && (
          <div className="space-y-2">
            <Progress value={progress} />
            <p className="text-xs text-muted-foreground tabular-nums">
              {progress}% ·{" "}
              {t("rebuildSlicesDone", {
                done: job.data.done_slices,
                total: job.data.total_slices,
              })}{" "}
              · {t("rebuildLogsReplayed", { count: job.data.replayed_logs })}
            </p>
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => handleClose(false)}>
            {showProgress ? t("rebuildRunInBackground") : tc("cancel")}
          </Button>
          {showForm && (
            <Button onClick={handleSubmit} disabled={!canSubmit}>
              {submit.isPending && <Loader2 className="mr-2 size-4 animate-spin" />}
              {t("rebuildConfirm")}
            </Button>
          )}
          {showProgress && (
            <Button disabled>
              <Loader2 className="mr-2 size-4 animate-spin" />
              {t("rebuildRunning")}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
