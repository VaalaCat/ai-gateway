import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import type { CleanupRun } from "@/lib/hooks/use-data-cleanup";
import type { TableStats } from "@/lib/types";
import {
  DataCleanupStats,
  utcCalendarToday,
  type DataCleanupRunner,
} from "./data-cleanup-stats";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, unknown>) =>
    values?.count === undefined ? key : `${key}:${values.count}`,
}));

vi.mock("@/components/business/date-picker/date-picker", () => ({
  DatePicker: ({ onChange, disabledRange }: { onChange: (value: string) => void; disabledRange?: { after?: Date } }) => (
    <div>
      <button type="button" onClick={() => onChange("2026-07-21")}>
        {disabledRange?.after ? "pickDateWithFutureDisabled" : "pickDate"}
      </button>
      <button type="button" onClick={() => onChange("2026-07-20")}>changeDate</button>
    </div>
  ),
}));

const idleRun: CleanupRun = {
  cutoffDate: "",
  status: "idle",
  tables: [],
  totalToDelete: 0,
  deleted: 0,
  activeTableIndex: 0,
};

function runner(run: CleanupRun = idleRun): DataCleanupRunner {
  return {
    run,
    preview: vi.fn(async () => {}),
    start: vi.fn(async () => {}),
    stop: vi.fn(),
    retry: vi.fn(async () => {}),
    reset: vi.fn(),
  };
}

function renderCleanupStats(tables: TableStats[], cleanupRunner = runner()) {
  const queryClient = new QueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  return { ...render(<DataCleanupStats tables={tables} runner={cleanupRunner} />, { wrapper }), runner: cleanupRunner };
}

beforeEach(() => vi.clearAllMocks());

it("uses the UTC date as the local calendar disable boundary", () => {
  const originalTimezone = process.env.TZ;
  process.env.TZ = "America/Los_Angeles";
  try {
    const boundary = utcCalendarToday(new Date("2026-07-28T00:30:00Z"));
    expect([
      boundary.getFullYear(),
      boundary.getMonth() + 1,
      boundary.getDate(),
    ]).toEqual([2026, 7, 28]);
  } finally {
    process.env.TZ = originalTimezone;
  }
});

it("renders five cleanup categories and never exposes business tables", () => {
  renderCleanupStats([
    { database: "log", name: "request_logs", count: 9 },
    { database: "core", name: "billing_logs", count: 4 },
    { database: "core", name: "users", count: 99 },
  ]);

  expect(screen.getAllByRole("button", { name: "actions.clearCategory" })).toHaveLength(5);
  expect(screen.queryByText("users")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /users/ })).not.toBeInTheDocument();
});

it("expands a category into localized physical table details", async () => {
  renderCleanupStats([
    { database: "log", name: "usage_hourly_buckets", count: 18_320 },
  ]);

  await userEvent.click(screen.getAllByRole("button", { name: "actions.showDetails" })[2]);

  const detail = screen.getByText("tables.usageHourlyBuckets").parentElement;
  expect(detail).not.toBeNull();
  expect(within(detail as HTMLElement).getByText("18,320")).toBeInTheDocument();
});

it("requires a date and explicit billing risk confirmation before starting", async () => {
  const cleanupRunner = runner({
    categoryID: "billingFacts",
    cutoffDate: "2026-07-21",
    status: "ready",
    tables: [],
    totalToDelete: 10,
    deleted: 0,
    activeTableIndex: 0,
  });
  const view = renderCleanupStats([], cleanupRunner);
  await userEvent.click(screen.getAllByRole("button", { name: "actions.clearCategory" })[3]);

  expect(screen.getByRole("button", { name: "actions.preview" })).toBeDisabled();
  expect(screen.getByText("pickDateWithFutureDisabled")).toBeInTheDocument();
  await userEvent.click(screen.getByText("pickDateWithFutureDisabled"));
  await userEvent.click(screen.getByRole("button", { name: "actions.preview" }));
  expect(cleanupRunner.preview).toHaveBeenCalledWith("billingFacts", "2026-07-21");

  view.rerender(<DataCleanupStats tables={[]} runner={cleanupRunner} />);
  expect(screen.getByText("billingFactsRisk")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "actions.confirmCleanup" })).toBeDisabled();
  await userEvent.click(screen.getByRole("checkbox", { name: "acceptBillingRisk" }));
  expect(screen.getByRole("button", { name: "actions.confirmCleanup" })).toBeEnabled();
});

it("invalidates a ready preview when the cutoff date changes", async () => {
  const cleanupRunner = runner({
    categoryID: "billingFacts",
    cutoffDate: "2026-07-21",
    status: "ready",
    tables: [],
    totalToDelete: 10,
    deleted: 0,
    activeTableIndex: 0,
  });
  renderCleanupStats([], cleanupRunner);
  await userEvent.click(screen.getAllByRole("button", { name: "actions.clearCategory" })[3]);
  await userEvent.click(screen.getByText("pickDateWithFutureDisabled"));
  await userEvent.click(screen.getByRole("button", { name: "actions.preview" }));
  const resetsBeforeChange = vi.mocked(cleanupRunner.reset).mock.calls.length;
  await userEvent.click(screen.getByText("changeDate"));

  expect(cleanupRunner.reset).toHaveBeenCalledTimes(resetsBeforeChange + 1);
  expect(screen.queryByRole("button", { name: "actions.confirmCleanup" })).not.toBeInTheDocument();
});

it("shows stop, failed retry, and actual completed totals", async () => {
  const cleanupRunner = runner({
    categoryID: "requestLogs",
    cutoffDate: "2026-07-21",
    status: "deleting",
    tables: [{
      database: "log", table: "request_logs", cutoff_date: "2026-07-21",
      total: 10, to_delete: 10, snapshot_max_key: "10", deleted: 4, status: "deleting",
    }],
    totalToDelete: 10,
    deleted: 4,
    activeTableIndex: 0,
  });
  const view = renderCleanupStats([], cleanupRunner);
  await userEvent.click(screen.getAllByRole("button", { name: "actions.clearCategory" })[0]);
  await userEvent.click(screen.getByRole("button", { name: "actions.stop" }));
  expect(cleanupRunner.stop).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("progressbar", { name: "progress" })).toBeInTheDocument();

  cleanupRunner.run = {
    ...cleanupRunner.run,
    status: "paused",
    tables: [{ ...cleanupRunner.run.tables[0], status: "failed", error: "locked" }],
  };
  view.rerender(<DataCleanupStats tables={[]} runner={cleanupRunner} />);
  expect(screen.getByText("locked")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "actions.retry" }));
  expect(cleanupRunner.retry).toHaveBeenCalledTimes(1);

  cleanupRunner.run = { ...cleanupRunner.run, status: "completed", deleted: 4 };
  view.rerender(<DataCleanupStats tables={[]} runner={cleanupRunner} />);
  expect(screen.getByText("completedDeleted:4")).toBeInTheDocument();
});
