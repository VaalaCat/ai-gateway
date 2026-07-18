import { render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { RebuildDialog } from "./rebuild-dialog";

const mocks = vi.hoisted(() => ({
  job: undefined as undefined | { status: string; replayed_logs: number; error?: string; done_slices: number; total_slices: number },
  jobs: [] as { id: string; status: string; replayed_logs: number; done_slices: number; total_slices: number; started_at: number }[],
  jobsEnabled: vi.fn(),
  trackedJob: vi.fn(),
  invalidate: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
  warning: vi.fn(),
}));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("sonner", () => ({ toast: { success: mocks.success, error: mocks.error, warning: mocks.warning } }));
vi.mock("@/lib/api/billing", () => ({
  useRebuildBillingSubmit: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useRebuildBillingJob: (id: string | null) => {
    mocks.trackedJob(id);
    return { data: id ? mocks.job : undefined, isError: false };
  },
  useRebuildBillingJobs: ({ enabled }: { enabled: boolean }) => {
    mocks.jobsEnabled(enabled);
    return { data: { jobs: mocks.jobs } };
  },
  useInvalidateBillingCaches: () => mocks.invalidate,
}));
vi.mock("@/components/ui/dialog", () => ({
  Dialog: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DialogContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogFooter: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogHeader: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogTitle: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));

beforeEach(() => {
  mocks.job = undefined;
  mocks.jobs = [];
  mocks.jobsEnabled.mockReset();
  mocks.trackedJob.mockReset();
  mocks.invalidate.mockReset();
  mocks.success.mockReset();
  mocks.error.mockReset();
  mocks.warning.mockReset();
});

function runningJob(id: string, startedAt = 1) {
  return {
    id,
    status: "running",
    replayed_logs: 0,
    done_slices: 0,
    total_slices: 1,
    started_at: startedAt,
  };
}

describe("RebuildDialog automatic job ownership", () => {
  it("keeps tracking the selected job and stops list polling when the list grows", () => {
    const automatic = runningJob("job-a");
    mocks.jobs = [automatic];
    mocks.job = automatic;
    const { rerender } = render(<RebuildDialog open onOpenChange={vi.fn()} />);

    expect(mocks.trackedJob).toHaveBeenLastCalledWith("job-a");
    expect(mocks.jobsEnabled).toHaveBeenLastCalledWith(false);

    mocks.jobs = [automatic, runningJob("job-b", 2)];
    rerender(<RebuildDialog open onOpenChange={vi.fn()} />);

    expect(mocks.trackedJob).toHaveBeenLastCalledWith("job-a");
    expect(mocks.jobsEnabled).toHaveBeenLastCalledWith(false);
  });

  it("keeps tracking the selected job when it disappears from the jobs list", () => {
    const automatic = runningJob("job-a");
    mocks.jobs = [automatic];
    mocks.job = automatic;
    const { rerender } = render(<RebuildDialog open onOpenChange={vi.fn()} />);

    mocks.jobs = [];
    rerender(<RebuildDialog open onOpenChange={vi.fn()} />);

    expect(mocks.trackedJob).toHaveBeenLastCalledWith("job-a");
    expect(mocks.jobsEnabled).toHaveBeenLastCalledWith(false);
  });

  it.each([
    { status: "succeeded", toast: "success" as const, closes: true, invalidates: true },
    { status: "failed", toast: "error" as const, closes: false, invalidates: false },
    { status: "canceled", toast: "warning" as const, closes: false, invalidates: false },
  ])("handles automatic job $status once and clears its ownership", async ({ status, toast: toastKind, closes, invalidates }) => {
    const automatic = runningJob("job-a");
    mocks.jobs = [automatic];
    mocks.job = automatic;
    const onOpenChange = vi.fn();
    const { rerender } = render(<RebuildDialog open onOpenChange={onOpenChange} />);

    mocks.job = { ...automatic, status, error: status === "failed" ? "failed slice" : undefined };
    rerender(<RebuildDialog open onOpenChange={onOpenChange} />);
    rerender(<RebuildDialog open onOpenChange={onOpenChange} />);

    await waitFor(() => expect(mocks[toastKind]).toHaveBeenCalledOnce());
    expect(mocks.trackedJob).toHaveBeenLastCalledWith(null);
    expect(mocks.jobsEnabled).toHaveBeenLastCalledWith(true);
    expect(onOpenChange).toHaveBeenCalledTimes(closes ? 1 : 0);
    expect(mocks.invalidate).toHaveBeenCalledTimes(invalidates ? 1 : 0);
  });
});

describe("RebuildDialog terminal transitions", () => {
  it("handles success exactly once and invalidates billing caches", () => {
    mocks.job = { status: "succeeded", replayed_logs: 7, done_slices: 1, total_slices: 1 };
    const onOpenChange = vi.fn();
    const { rerender } = render(<RebuildDialog open onOpenChange={onOpenChange} initialJobId="job-success" />);
    rerender(<RebuildDialog open onOpenChange={onOpenChange} initialJobId="job-success" />);

    expect(mocks.success).toHaveBeenCalledOnce();
    expect(mocks.invalidate).toHaveBeenCalledOnce();
    expect(onOpenChange).toHaveBeenCalledOnce();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("handles failure exactly once without invalidating successful billing caches", () => {
    mocks.job = { status: "failed", error: "failed slice", replayed_logs: 0, done_slices: 0, total_slices: 1 };
    const { rerender } = render(<RebuildDialog open onOpenChange={vi.fn()} initialJobId="job-failed" />);
    rerender(<RebuildDialog open onOpenChange={vi.fn()} initialJobId="job-failed" />);

    expect(mocks.error).toHaveBeenCalledOnce();
    expect(mocks.invalidate).not.toHaveBeenCalled();
  });

  it("handles cancellation exactly once", () => {
    mocks.job = { status: "canceled", replayed_logs: 0, done_slices: 0, total_slices: 1 };
    const { rerender } = render(<RebuildDialog open onOpenChange={vi.fn()} initialJobId="job-canceled" />);
    rerender(<RebuildDialog open onOpenChange={vi.fn()} initialJobId="job-canceled" />);

    expect(mocks.warning).toHaveBeenCalledOnce();
  });
});
