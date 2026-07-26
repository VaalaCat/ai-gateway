import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import { ApiError } from "@/lib/api/client";
import LogsPage from "./page";

const state = vi.hoisted(() => ({
  logs: {} as Record<string, unknown>,
  insights: {} as Record<string, unknown>,
  refetchLogs: vi.fn(),
  refetchInsights: vi.fn(),
}));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ isAdmin: true }) }));
vi.mock("@/lib/api/channels", () => ({ useChannels: () => ({ data: { data: [] } }) }));
vi.mock("@/lib/api/byok-channels", () => ({ useBYOKChannels: () => ({ data: { data: [] } }) }));
vi.mock("@/lib/api/logs", () => ({ useLogs: () => state.logs }));
vi.mock("@/lib/api/logs-insights", () => ({ useLogsInsights: () => state.insights }));
vi.mock("@/components/data-table/use-filter-state", () => ({ useFilterState: () => [{}, vi.fn()] }));
vi.mock("@/components/data-table/use-pagination-state", () => ({ usePaginationState: () => [1, 20, vi.fn()] }));
vi.mock("@/hooks/use-user-pref", () => ({ useUserPref: () => [null, vi.fn()] }));
vi.mock("@/components/business/kpi-grid", () => ({ KpiGrid: () => <div>kpi-grid</div> }));
vi.mock("@/components/data-table/data-table", () => ({ DataTable: () => <div>data-table</div> }));
vi.mock("@/components/data-table/column-header", () => ({ DataTableColumnHeader: () => null }));
vi.mock("@/components/data-table/filterable-toolbar", () => ({ FilterableToolbar: () => null }));
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
  state.refetchLogs.mockReset();
  state.refetchInsights.mockReset();
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
