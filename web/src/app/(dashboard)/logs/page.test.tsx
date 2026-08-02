import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import { ApiError } from "@/lib/api/client";
import { buildCompleteDateRange, dateStrToTs, tsToDateStr } from "@/lib/utils/date-range";
import LogsPage from "./page";

const state = vi.hoisted(() => ({
  logs: {} as Record<string, unknown>,
  insights: {} as Record<string, unknown>,
  filterValues: {} as Record<string, string | number | undefined>,
  logsParams: undefined as Record<string, unknown> | undefined,
  logsOptions: undefined as Record<string, unknown> | undefined,
  insightsParams: undefined as Record<string, unknown> | undefined,
  refetchLogs: vi.fn(),
  refetchInsights: vi.fn(),
  setFilterValues: vi.fn(),
  observabilityHeaderProps: undefined as Record<string, unknown> | undefined,
  dataTableProps: undefined as Record<string, unknown> | undefined,
  filterableToolbarProps: undefined as Record<string, unknown> | undefined,
  filterStateSpec: undefined as Record<string, unknown> | undefined,
}));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ isAdmin: true }) }));
vi.mock("@/lib/api/channels", () => ({ useChannels: () => ({ data: { data: [] } }) }));
vi.mock("@/lib/api/byok-channels", () => ({ useBYOKChannels: () => ({ data: { data: [] } }) }));
vi.mock("@/lib/api/logs", () => ({
  useLogs: (params: Record<string, unknown>, options: Record<string, unknown>) => {
    state.logsParams = params;
    state.logsOptions = options;
    return state.logs;
  },
}));
vi.mock("@/lib/api/logs-insights", () => ({
  useLogsInsights: (params: Record<string, unknown>) => {
    state.insightsParams = params;
    return state.insights;
  },
}));
vi.mock("@/components/data-table/use-filter-state", () => ({
  useFilterState: (spec: Record<string, unknown>) => {
    state.filterStateSpec = spec;
    return [state.filterValues, state.setFilterValues];
  },
}));
vi.mock("@/components/data-table/use-pagination-state", () => ({ usePaginationState: () => [1, 20, vi.fn()] }));
vi.mock("@/hooks/use-user-pref", () => ({ useUserPref: () => [null, vi.fn()] }));
vi.mock("@/components/business/kpi-grid", () => ({ KpiGrid: () => <div>kpi-grid</div> }));
vi.mock("@/components/business/observability-header", () => ({
  ObservabilityHeader: (props: Record<string, unknown>) => {
    state.observabilityHeaderProps = props;
    return <div>observability-header</div>;
  },
}));
vi.mock("@/components/data-table/data-table", () => ({
  DataTable: (props: Record<string, unknown>) => {
    state.dataTableProps = props;
    const toolbar = props.toolbar;
    return (
      <div>
        data-table
        {typeof toolbar === "function" ? toolbar({ id: "logs-table" }) : toolbar}
      </div>
    );
  },
}));
vi.mock("@/components/data-table/column-header", () => ({ DataTableColumnHeader: () => null }));
vi.mock("@/components/data-table/filterable-toolbar", () => ({
  FilterableToolbar: (props: Record<string, unknown>) => {
    state.filterableToolbarProps = props;
    return (
      <div>
        filterable-toolbar
        {props.secondaryContent as React.ReactNode}
        {props.primaryAction as React.ReactNode}
      </div>
    );
  },
}));
vi.mock("@/components/data-table/column-visibility", () => ({
  ColumnVisibility: ({ table }: { table: { id?: string } }) => (
    <div data-testid="column-visibility">{table.id}</div>
  ),
}));
vi.mock("@/components/business/date-cell", () => ({ DateCell: () => null }));
vi.mock("@/components/business/cost-cell", () => ({ CostDetailCell: () => null }));
vi.mock("@/components/business/duration-cell", () => ({ DurationCell: () => null }));
vi.mock("@/components/business/status-badge", () => ({ StreamBadge: () => null }));
vi.mock("@/components/business/model-name", () => ({ ModelName: () => null }));
vi.mock("@/components/business/trace-detail", () => ({ TraceDetail: () => null }));
vi.mock("@/components/business/fallback-chain", () => ({ FallbackChain: () => null }));
vi.mock("@/components/business/rate-limit-section", () => ({ RateLimitSection: () => null }));
vi.mock("@/components/business/entity-label", () => ({ EntityLabel: () => null }));

beforeEach(() => {
  state.filterValues = {};
  state.logsParams = undefined;
  state.logsOptions = undefined;
  state.insightsParams = undefined;
  state.refetchLogs.mockReset();
  state.refetchInsights.mockReset();
  state.setFilterValues.mockReset();
  state.observabilityHeaderProps = undefined;
  state.dataTableProps = undefined;
  state.filterableToolbarProps = undefined;
  state.filterStateSpec = undefined;
  state.logs = {
    data: { data: [], total: 0 },
    error: null,
    isError: false,
    isLoading: false,
    isFetching: false,
    refetch: state.refetchLogs,
  };
  state.insights = {
    data: { totals: { total: 0, failed: 0, p95_ms: 0, slowest_ms: 0 } },
    error: null,
    isError: false,
    isFetching: false,
    refetch: state.refetchInsights,
  };
});

it("moves the shared date range into the page header without granularity", () => {
  state.filterValues = { start: 1_768_867_200, end: 1_768_953_599 };

  render(<LogsPage />);

  expect(state.observabilityHeaderProps).toMatchObject({
    title: "title",
    subtitle: "description",
    showGranularity: false,
    maxDays: 7,
    range: {
      start: state.insightsParams?.start,
      end: (state.insightsParams?.end as number) - 1,
      gran: "day",
    },
  });
  expect(state.filterableToolbarProps?.spec).not.toHaveProperty("time");
  expect(state.filterStateSpec).toHaveProperty("time");
});

it("writes a header date change atomically into the existing filter state", () => {
  render(<LogsPage />);

  const onRangeChange = state.observabilityHeaderProps?.onRangeChange as
    | ((range: { start: number; end: number; gran: "day" }) => void)
    | undefined;
  onRangeChange?.({ start: 100, end: 200, gran: "day" });

  expect(state.setFilterValues).toHaveBeenCalledOnce();
  expect(state.setFilterValues).toHaveBeenCalledWith({ start: 100, end: 200 });
});

it("keeps log-only filters, auto refresh, and column visibility inside the table toolbar", () => {
  render(<LogsPage />);

  expect(state.dataTableProps?.toolbar).toEqual(expect.any(Function));
  expect(state.filterableToolbarProps?.spec).toMatchObject({
    model_name: expect.any(Object),
    request_id: expect.any(Object),
    status: expect.any(Object),
    user_id: expect.any(Object),
    token_id: expect.any(Object),
    channel_id: expect.any(Object),
    private_channel_id: expect.any(Object),
  });
  expect(state.filterableToolbarProps).not.toHaveProperty("filtersOnOwnRow");
  expect(state.filterableToolbarProps?.secondaryContent).toBeTruthy();
  expect(state.filterableToolbarProps?.secondaryActions).toBeUndefined();
  expect(state.filterableToolbarProps?.primaryAction).toBeTruthy();
  expect(state.filterableToolbarProps?.secondaryContent).toMatchObject({
    props: expect.objectContaining({ children: expect.anything() }),
  });
  expect(screen.getByRole("combobox", { name: "autoRefreshOff" })).toHaveClass(
    "!size-9",
    "justify-center",
    "overflow-hidden",
    "sm:!h-8",
    "sm:justify-between",
  );
  expect(screen.getByTestId("column-visibility")).toHaveTextContent("logs-table");
});

it("keeps non-date filters out of insights and uses the header as the sole manual refresh", () => {
  state.filterValues = { model_name: "gpt-5", request_id: "req-1", status: "0" };
  render(<LogsPage />);

  expect(state.logsParams).toMatchObject({ model_name: "gpt-5", request_id: "req-1", status: "0" });
  expect(state.insightsParams).not.toHaveProperty("model_name");
  expect(state.insightsParams).not.toHaveProperty("request_id");
  expect(state.insightsParams).not.toHaveProperty("status");

  (state.observabilityHeaderProps?.onRefresh as () => void)();
  expect(state.refetchLogs).toHaveBeenCalledOnce();
  expect(state.refetchInsights).toHaveBeenCalledOnce();
});

it.each([
  { filterValues: { start: 1_768_867_200 }, kind: "start-only" },
  { filterValues: { end: 1_768_953_599 }, kind: "end-only" },
] as const)("normalizes a $kind legacy URL to one complete shared day", ({ filterValues }) => {
  state.filterValues = { ...filterValues };

  render(<LogsPage />);

  const listRange = {
    start: state.logsParams?.start,
    end: state.logsParams?.end,
  };
  expect(listRange).toEqual(state.insightsParams);
  expect(listRange.start).toEqual(expect.any(Number));
  expect(listRange.end).toEqual(expect.any(Number));
  expect(listRange.end).toBeGreaterThan(listRange.start as number);
  const timestamp = ("start" in filterValues ? filterValues.start : filterValues.end) as number;
  const date = tsToDateStr(timestamp);
  expect(listRange).toEqual({
    start: dateStrToTs(date, false),
    end: dateStrToTs(date, true) + 1,
  });
});

it("sends a known local day as an exclusive API range", () => {
  const selectedDayStart = Math.floor(new Date(2026, 6, 20, 0, 0, 0, 0).getTime() / 1000);
  const nextDayStart = Math.floor(new Date(2026, 6, 21, 0, 0, 0, 0).getTime() / 1000);
  state.filterValues = { start: selectedDayStart };

  render(<LogsPage />);

  expect(state.insightsParams).toEqual({ start: selectedDayStart, end: nextDayStart });
  expect({ start: state.logsParams?.start, end: state.logsParams?.end }).toEqual(
    state.insightsParams,
  );
  expect(tsToDateStr(nextDayStart - 1)).toBe("2026-07-20");
  expect(tsToDateStr(nextDayStart)).toBe("2026-07-21");
});

it("sorts and clamps a legacy range to seven inclusive days anchored at the end", () => {
  state.filterValues = {
    start: 1_768_953_599,
    end: 1_767_225_600,
  };

  render(<LogsPage />);

  expect({ start: state.logsParams?.start, end: state.logsParams?.end }).toEqual(
    state.insightsParams,
  );
  const expected = buildCompleteDateRange(
    tsToDateStr(state.filterValues.start as number),
    tsToDateStr(state.filterValues.end as number),
    7,
  );
  expect(state.insightsParams).toEqual({
    start: dateStrToTs(expected.startDate, false),
    end: dateStrToTs(expected.endDate, true) + 1,
  });
});

it("ignores non-finite URL bounds and keeps the list unlimited", () => {
  state.filterValues = { start: Number.NaN, end: Number.POSITIVE_INFINITY };

  render(<LogsPage />);

  expect(state.logsParams).not.toHaveProperty("start");
  expect(state.logsParams).not.toHaveProperty("end");
  expect(state.insightsParams?.start).toEqual(expect.any(Number));
  expect(state.insightsParams?.end).toEqual(expect.any(Number));
  expect(Number.isFinite(state.insightsParams?.start)).toBe(true);
  expect(Number.isFinite(state.insightsParams?.end)).toBe(true);
  expect(JSON.stringify({ list: state.logsParams, insights: state.insightsParams })).not.toMatch(
    /NaN|Infinity/,
  );
});

it("keeps an unfiltered list unlimited and uses the default seven-day insights window", () => {
  render(<LogsPage />);

  expect(state.logsParams).not.toHaveProperty("start");
  expect(state.logsParams).not.toHaveProperty("end");
  expect((state.insightsParams?.end as number) - (state.insightsParams?.start as number)).toBe(
    7 * 86_400,
  );
});

it("shows log database unavailable instead of an empty table when the list returns 503", () => {
  state.logs = {
    ...state.logs,
    data: undefined,
    error: new ApiError(503, "log database is temporarily unavailable"),
    isError: true,
  };

  render(<LogsPage />);

  expect(screen.getByRole("alert")).toHaveTextContent("logDatabaseUnavailable");
  expect(screen.queryByText("data-table")).not.toBeInTheDocument();
});

it("shows log database unavailable when a refetch returns 503 with stale list data", () => {
  state.logs = {
    ...state.logs,
    error: new ApiError(503, "log database is temporarily unavailable"),
    isError: true,
  };

  render(<LogsPage />);

  expect(screen.getByRole("alert")).toHaveTextContent("logDatabaseUnavailable");
  expect(screen.getByRole("button", { name: "retry" })).toBeVisible();
  expect(screen.queryByText("kpi-grid")).not.toBeInTheDocument();
  expect(screen.queryByText("data-table")).not.toBeInTheDocument();
});

it("shows log database unavailable instead of zero KPIs when insights returns 503", () => {
  state.insights = {
    ...state.insights,
    data: undefined,
    error: new ApiError(503, "log database is temporarily unavailable"),
    isError: true,
  };

  render(<LogsPage />);

  expect(screen.getByRole("alert")).toHaveTextContent("logDatabaseUnavailable");
  expect(screen.queryByText("kpi-grid")).not.toBeInTheDocument();
});

it("retries both log queries from the load error state", async () => {
  state.logs = { ...state.logs, data: undefined, error: new Error("offline"), isError: true };
  const user = userEvent.setup();
  render(<LogsPage />);

  expect(screen.getByRole("alert")).toHaveTextContent("loadFailed");
  await user.click(screen.getByRole("button", { name: "retry" }));

  expect(state.refetchLogs).toHaveBeenCalledTimes(1);
  expect(state.refetchInsights).toHaveBeenCalledTimes(1);
});
