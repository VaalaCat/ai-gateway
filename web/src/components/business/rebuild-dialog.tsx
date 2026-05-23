"use client";

import { useEffect, useMemo, useState } from "react";
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
  const t = useTranslations("billing");
  const tc = useTranslations("common");

  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState("");
  const [jobId, setJobId] = useState<string | null>(null);
  // When the picker is open and user clicks "new run", we override the
  // running-jobs gate to show the date form even though running jobs exist.
  const [forceForm, setForceForm] = useState(false);

  const submit = useRebuildBillingSubmit();
  const job = useRebuildBillingJob(jobId);
  // Only poll the list while dialog is open AND we're not already bound to a job.
  const jobsList = useRebuildBillingJobs({ enabled: open && !jobId });
  const invalidateBilling = useInvalidateBillingCaches();

  // Bind to initialJobId when dialog opens with one supplied.
  useEffect(() => {
    if (open && initialJobId && !jobId) {
      setJobId(initialJobId);
    }
    if (!open) {
      // closed → reset overrides; jobId is preserved only if still running
      // (handleClose decides; effect just clears the picker override).
      setForceForm(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, initialJobId]);

  const runningJobs = useMemo(
    () =>
      (jobsList.data?.jobs ?? [])
        .filter((j) => j.status === "running")
        .sort((a, b) => b.started_at - a.started_at),
    [jobsList.data?.jobs],
  );

  // Single running job shortcut: when dialog opens and exactly one job is
  // running, auto-bind to it so the user lands directly on the progress view
  // (skips the picker). Multi-running keeps the picker for selection.
  useEffect(() => {
    if (open && !jobId && !forceForm && runningJobs.length === 1) {
      setJobId(runningJobs[0].id);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, runningJobs.length]);

  const hasDate = !!(startDate || endDate);
  const validRange = isDateRangeValid(startDate, endDate);
  const isRunning = job.data?.status === "running";

  // View resolution: 1) bound to a specific job → progress; 2) running jobs
  // exist and user didn't ask for form → picker; 3) form.
  const showProgress = !!jobId;
  const showPicker = !showProgress && !forceForm && runningJobs.length > 0;
  const showForm = !showProgress && !showPicker;

  const canSubmit = showForm && hasDate && validRange && !submit.isPending;

  const reset = () => {
    setStartDate("");
    setEndDate("");
    setJobId(null);
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
      setJobId(result.job_id);
      setForceForm(false);
    } catch (e) {
      toast.error(formatErrorToast(e, t("rebuildFailed")));
    }
  };

  // Terminal state handling: fire toast + invalidate caches + clear jobId.
  useEffect(() => {
    if (!jobId || !job.data) return;
    if (job.data.status === "succeeded") {
      toast.success(t("rebuildSuccess", { count: job.data.replayed_logs }));
      invalidateBilling();
      setJobId(null);
      onOpenChange(false);
      setStartDate("");
      setEndDate("");
    } else if (job.data.status === "failed") {
      toast.error(`${t("rebuildFailed")}: ${job.data.error ?? ""}`);
      setJobId(null);
    } else if (job.data.status === "canceled") {
      toast.warning(t("rebuildCanceled"));
      setJobId(null);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [job.data?.status]);

  // Job-not-found (e.g., master restarted between submit and poll).
  useEffect(() => {
    if (jobId && job.isError) {
      toast.warning(t("rebuildJobLost"));
      setJobId(null);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [job.isError]);

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
                      onClick={() => setJobId(rj.id)}
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
