import type { ReactNode } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

import type { DashboardResponse } from "@/lib/api/dashboard";
import DashboardPage from "./page";

const dashboardFixture = {
  kpis: {
    requests: { value: 10, spark: [], delta: 0 },
    cost: { value: 20, spark: [], delta: 0 },
    tokens: { value: 30, spark: [], delta: 0 },
  },
  trend: {
    buckets: [{ ts: 1, label: "00:00", cost: 20, requests: 10, tokens: 30 }],
    metrics: ["cost", "requests", "tokens"],
  },
  log_metrics: {
    trend: {
      buckets: [{ ts: 1, label: "00:00", ttft_ms: 100, tps: 20, cache_hit_rate: 50 }],
      metrics: ["ttft", "tps", "cache_hit_rate"],
    },
    leaderboard: { users: [], models: [], channels: [], available_metrics: [] },
    speed_compare: {
      by_model: [{ name: "gpt-5", ttft_ms: 100, tps: 20, ttft_p95_ms: 150, tps_p5: 10 }],
      by_channel: [],
    },
  },
  data_status: { log_db: "available" },
} satisfies DashboardResponse;

const calls = vi.hoisted(() => ({
  query: "",
  dashboard: vi.fn(),
  metricTrend: vi.fn(),
  marketShare: vi.fn(),
  marketChart: vi.fn(),
  modelDistribution: vi.fn(),
  donut: vi.fn(),
  metricChart: vi.fn(),
  kpi: vi.fn(),
  metricResponse: undefined as undefined | Record<string, unknown>,
  metricLoading: false,
  dashboardResponse: undefined as DashboardResponse | undefined,
  isAdmin: true,
}));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: vi.fn() }),
  useSearchParams: () => new URLSearchParams(calls.query),
  usePathname: () => "/dashboard",
}));
vi.mock("@/lib/auth", () => ({
  useAuth: () => ({ user: { user_id: 7 }, isAdmin: calls.isAdmin }),
}));
vi.mock("@/lib/hooks/use-chart-top-n", () => ({
  useChartTopN: () => [10, vi.fn()],
}));
vi.mock("@/lib/hooks/use-obs-range", () => ({
  useObsRange: () => ({
    range: { start: 1_700_000_000, end: 1_700_604_800, gran: "day" },
    setRange: vi.fn(),
    refresh: vi.fn(),
    refreshKey: 0,
  }),
}));
vi.mock("@/lib/api/dashboard", () => ({
  useDashboard: (...args: unknown[]) => {
    calls.dashboard(...args);
    return { data: calls.dashboardResponse ?? dashboardFixture, isFetching: false, refetch: vi.fn() };
  },
  useMarketShare: (...args: unknown[]) => {
    calls.marketShare(...args);
    return { data: undefined, isLoading: false, refetch: vi.fn() };
  },
  useMetricTrend: (...args: unknown[]) => {
    calls.metricTrend(...args);
    return { data: calls.metricResponse, isLoading: calls.metricLoading, refetch: vi.fn() };
  },
}));
vi.mock("@/lib/api/stats", () => ({
  useModelDistribution: (...args: unknown[]) => {
    calls.modelDistribution(...args);
    return { data: { buckets: [{ name: "gpt-5", value: 10, ratio: 1 }], series_order: ["gpt-5"] } };
  },
}));
vi.mock("@/components/business/observability-header", () => ({
  ObservabilityHeader: ({ extraFilters }: { extraFilters?: ReactNode }) => <div>{extraFilters}</div>,
}));
vi.mock("@/components/business/kpi-grid", () => ({
  KpiGrid: (props: unknown) => {
    calls.kpi(props);
    return null;
  },
}));
vi.mock("@/components/business/donut-chart", () => ({
  DonutChart: (props: unknown) => {
    calls.donut(props);
    return null;
  },
}));
vi.mock("@/components/business/market-share-chart", () => ({ MarketShareChart: (props: unknown) => {
  calls.marketChart(props);
  return null;
} }));
vi.mock("@/components/business/leaderboard", () => ({ Leaderboard: () => null }));
vi.mock("@/components/business/speed-ranking", () => ({ SpeedRanking: () => null }));
vi.mock("@/components/business/model-name", () => ({
  ModelName: ({ name }: { name: string }) => <span>{name}</span>,
}));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({ EntityPicker: () => null }));
vi.mock("@/components/business/chart-option-select", () => ({
  ChartOptionSelect: ({ label, value, options, onValueChange }: {
    label: string;
    value: string;
    options: Array<{ value: string; label: string }>;
    onValueChange: (value: string) => void;
  }) => (
    <button
      type="button"
      aria-label={label}
      data-value={value}
      data-options={options.map((option) => option.value).join(",")}
      onClick={() => onValueChange(options[1]?.value ?? options[0]?.value)}
    >
      {value}
    </button>
  ),
}));
vi.mock("@/components/business/metric-trend-chart", () => ({
  MetricTrendChart: (props: {
    onMetricChange?: (metric: string) => void;
    headerExtra?: ReactNode;
    buckets?: Array<Record<string, unknown>>;
  }) => {
    calls.metricChart(props);
    return (
      <div>
        <button type="button" onClick={() => props.onMetricChange?.("ttft")}>ttft</button>
        <button type="button" onClick={() => props.onMetricChange?.("tps")}>tps</button>
        {props.headerExtra}
      </div>
    );
  },
}));

beforeEach(() => {
  calls.query = "";
  calls.dashboard.mockReset();
  calls.metricTrend.mockReset();
  calls.marketShare.mockReset();
  calls.marketChart.mockReset();
  calls.modelDistribution.mockReset();
  calls.donut.mockReset();
  calls.metricChart.mockReset();
  calls.kpi.mockReset();
  calls.metricResponse = undefined;
  calls.metricLoading = false;
  calls.dashboardResponse = undefined;
  calls.isAdmin = true;
});

it("passes one page-level top n to every ranked dashboard chart query", () => {
  render(<DashboardPage />);

  expect(calls.metricTrend.mock.calls.at(-1)?.[4]).toEqual(expect.objectContaining({ top_n: 10 }));
  expect(calls.marketShare.mock.calls.at(-1)?.[3]).toEqual(expect.objectContaining({ top_n: 10 }));
  expect(calls.modelDistribution.mock.calls.at(-1)?.[0]).toEqual(
    expect.objectContaining({ top_n: 10 }),
  );
  expect(calls.donut).toHaveBeenCalledWith(expect.objectContaining({
    topN: 10,
    othersLabel: "trend.others",
  }));
  expect(screen.getByRole("button", { name: "prefix.topN" })).toHaveAttribute("data-options", "5,10,20");
});

it("offers avg and p95 for ttft and sends the selected statistic", () => {
  render(<DashboardPage />);
  fireEvent.click(screen.getByRole("button", { name: "ttft" }));

  const stat = screen.getByRole("button", { name: "prefix.stat" });
  expect(stat).toHaveAttribute("data-options", "avg,p95");
  fireEvent.click(stat);

  expect(calls.metricTrend.mock.calls.at(-1)?.[4]).toEqual(expect.objectContaining({ stat: "p95" }));
  expect(calls.metricChart.mock.calls.at(-1)?.[0]).toEqual(expect.objectContaining({
    dim: "model",
    groupedOnly: true,
  }));
});

it.each([
  ["ttft", "p95"],
  ["tps", "p5"],
] as const)("loads %s %s from the ordinary user's model scope", (metric, stat) => {
  calls.isAdmin = false;
  calls.query = "user_id=42";
  render(<DashboardPage />);
  fireEvent.click(screen.getByRole("button", { name: metric }));
  fireEvent.click(screen.getByRole("button", { name: "prefix.stat" }));

  const options = calls.metricTrend.mock.calls.at(-1)?.[4];
  expect(options).toEqual(expect.objectContaining({
    stat,
    enabled: true,
  }));
  expect(options).not.toHaveProperty("user_id");
  expect(calls.dashboard.mock.calls.at(-1)?.[0]).not.toHaveProperty("user_id");
  expect(calls.modelDistribution.mock.calls.at(-1)?.[0]).not.toHaveProperty("user_id");
  expect(calls.metricChart.mock.calls.at(-1)?.[0]).toEqual(expect.objectContaining({
    dim: "model",
    groupedOnly: true,
  }));
});

it("forwards the selected user scope for administrators", () => {
  calls.query = "user_id=42";
  render(<DashboardPage />);

  expect(calls.dashboard.mock.calls.at(-1)?.[0]).toEqual(expect.objectContaining({ user_id: 42 }));
  expect(calls.modelDistribution.mock.calls.at(-1)?.[0]).toEqual(
    expect.objectContaining({ user_id: 42 }),
  );
  expect(calls.metricTrend.mock.calls.at(-1)?.[4]).toEqual(
    expect.objectContaining({ user_id: 42 }),
  );
});

it("offers avg and p5 for tps without exposing the ttft percentile", () => {
  render(<DashboardPage />);
  fireEvent.click(screen.getByRole("button", { name: "tps" }));

  expect(screen.getByRole("button", { name: "prefix.stat" })).toHaveAttribute("data-options", "avg,p5");
  expect(calls.metricTrend.mock.calls.at(-1)?.[4]).toEqual(expect.objectContaining({ stat: "avg" }));
});

it("resets percentile statistics when grouping is turned off", () => {
  render(<DashboardPage />);
  fireEvent.click(screen.getByRole("button", { name: "ttft" }));
  fireEvent.click(screen.getByRole("button", { name: "prefix.stat" }));

  const chartProps = calls.metricChart.mock.calls.at(-1)?.[0];
  act(() => chartProps.onDimChange("off"));

  expect(calls.metricTrend.mock.calls.at(-1)?.[4]).toEqual(expect.objectContaining({ stat: "avg" }));
});

it("does not expose a stale grouped response under a new percentile label", () => {
  calls.metricResponse = {
    metric: "ttft",
    stat: "avg",
    unit: "ms",
    estimated: false,
    buckets: [{ ts: 1, label: "00:00", series: { old: 100 } }],
    series_order: ["old"],
  };
  render(<DashboardPage />);
  fireEvent.click(screen.getByRole("button", { name: "ttft" }));
  fireEvent.click(screen.getByRole("button", { name: "prefix.stat" }));

  expect(calls.metricChart.mock.calls.at(-1)?.[0].grouped).toBeUndefined();
});

it("omits log-derived KPI cards and does not turn missing success rate into zero", () => {
  render(<DashboardPage />);

  const items = calls.kpi.mock.calls.at(-1)?.[0].items as Array<Record<string, unknown>>;
  expect(items.map((item) => item.key)).not.toEqual(expect.arrayContaining([
    "cacheHitRate", "ttftAvg", "tpsAvg",
  ]));
  const success = items.find((item) => item.key === "successRate");
  expect(success).toEqual(expect.objectContaining({ value: "—" }));
  expect(success).not.toHaveProperty("ratio");
  expect(success).not.toHaveProperty("threshold");
});

it("does not manufacture missing token component values while joining dashboard trends", () => {
  render(<DashboardPage />);

  const bucket = calls.metricChart.mock.calls.at(-1)?.[0].buckets[0];
  expect(bucket).toEqual(expect.objectContaining({ tokens: 30, ttft_ms: 100, tps: 20 }));
  expect(bucket).not.toHaveProperty("prompt_tokens");
  expect(bucket).not.toHaveProperty("completion_tokens");
  expect(bucket).not.toHaveProperty("cache_read_tokens");
  expect(bucket).not.toHaveProperty("cache_write_tokens");
});

it("keeps core-backed distribution and market share available when only log storage degrades", () => {
  calls.dashboardResponse = {
    ...dashboardFixture,
    log_metrics: null,
    data_status: { log_db: "unavailable" },
  };
  render(<DashboardPage />);

  const items = calls.kpi.mock.calls.at(-1)?.[0].items as Array<Record<string, unknown>>;
  expect(items.find((item) => item.key === "requests")).toEqual(expect.objectContaining({ value: 10 }));
  expect(calls.metricChart.mock.calls.at(-1)?.[0]).toEqual(expect.objectContaining({
    buckets: [expect.objectContaining({ requests: 10, cost: 20, tokens: 30 })],
    availableMetrics: expect.arrayContaining(["ttft", "tps", "cache_hit_rate"]),
    logUnavailable: true,
  }));
  expect(calls.donut.mock.calls.at(-1)?.[0]).not.toHaveProperty("error");
  expect(calls.marketChart.mock.calls.at(-1)?.[0]).not.toHaveProperty("error");
});
