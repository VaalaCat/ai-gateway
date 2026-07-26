import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";

import { MetricTrendChart } from "./metric-trend-chart";
import { chartColorForSeries, chartDashForSeries } from "@/lib/chart-colors";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));
vi.mock("@/components/ui/chart", () => ({
  ChartContainer: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  ChartLegend: () => null,
  ChartTooltip: () => null,
  ChartTooltipContent: () => null,
}));
vi.mock("recharts", () => ({
  CartesianGrid: () => null,
  Line: ({ dataKey, stroke, strokeDasharray }: { dataKey: string; stroke?: string; strokeDasharray?: string }) => (
    <span data-testid={`line-${dataKey}`} data-stroke={stroke} data-dash={strokeDasharray ?? "solid"} />
  ),
  LineChart: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  XAxis: () => null,
  YAxis: () => null,
}));

it("keeps token breakdown unavailable when the API only returns total tokens", () => {
  render(
    <MetricTrendChart
      buckets={[{
        ts: 1,
        label: "00:00",
        cost: 20,
        requests: 10,
        tokens: 30,
      }]}
      availableMetrics={["cost", "requests", "tokens"]}
      defaultMetric="tokens"
      title="Usage"
    />,
  );

  expect(screen.queryByRole("combobox", { name: "prefix.view" })).not.toBeInTheDocument();
});

it("locks percentile rendering to grouped lines and never falls back to the avg total", () => {
  const { rerender } = render(
    <MetricTrendChart
      buckets={[{ ts: 1, label: "00:00", cost: 0, requests: 1, tokens: 1, ttft_ms: 100 }]}
      grouped={{
        metric: "ttft",
        stat: "p95",
        unit: "ms",
        estimated: true,
        buckets: [{ ts: 1, label: "00:00", series: { "gpt-5": 900 } }],
        series_order: ["gpt-5"],
      }}
      availableMetrics={["ttft"]}
      metric="ttft"
      dim="model"
      onDimChange={() => {}}
      groupedOnly
      title="TTFT"
    />,
  );

  expect(screen.getByTestId("line-gpt-5")).toBeInTheDocument();
  expect(screen.queryByTestId("line-ttft_ms")).not.toBeInTheDocument();
  expect(screen.queryByRole("combobox", { name: "prefix.view" })).not.toBeInTheDocument();

  rerender(
    <MetricTrendChart
      buckets={[{ ts: 1, label: "00:00", cost: 0, requests: 1, tokens: 1, ttft_ms: 100 }]}
      availableMetrics={["ttft"]}
      metric="ttft"
      dim="model"
      onDimChange={() => {}}
      groupedOnly
      loading
      title="TTFT"
    />,
  );
  expect(screen.queryByTestId("line-ttft_ms")).not.toBeInTheDocument();
  expect(screen.queryByTestId("line-gpt-5")).not.toBeInTheDocument();
});

it("reserves the grouped legend height while rendering the total trend", () => {
  const { rerender } = render(
    <MetricTrendChart
      buckets={[{ ts: 1, label: "00:00", cost: 0, requests: 1, tokens: 1, ttft_ms: 100 }]}
      availableMetrics={["ttft"]}
      metric="ttft"
      dim="model"
      onDimChange={() => {}}
      title="TTFT"
    />,
  );

  const totalLegendSlot = screen.getByTestId("responsive-chart-legend");
  expect(totalLegendSlot.firstElementChild).toHaveAttribute("aria-hidden", "true");
  expect(totalLegendSlot.firstElementChild).toHaveAttribute("data-slot", "chart-legend-shell");
  expect(totalLegendSlot.firstElementChild).toHaveClass("h-10");

  rerender(
    <MetricTrendChart
      buckets={[{ ts: 1, label: "00:00", cost: 0, requests: 1, tokens: 1, ttft_ms: 100 }]}
      grouped={{
        metric: "ttft",
        stat: "p95",
        unit: "ms",
        estimated: true,
        buckets: [{ ts: 1, label: "00:00", series: { "gpt-5": 900 } }],
        series_order: ["gpt-5"],
      }}
      availableMetrics={["ttft"]}
      metric="ttft"
      dim="model"
      onDimChange={() => {}}
      groupedOnly
      title="TTFT"
    />,
  );

  const groupedLegendSlot = screen.getByTestId("responsive-chart-legend");
  const groupedRegion = screen.getByRole("region", { name: "legend.series" });
  expect(groupedLegendSlot).toContainElement(groupedRegion);
  expect(groupedRegion.parentElement).toHaveAttribute("data-slot", "chart-legend-shell");
  expect(groupedRegion.parentElement).toHaveClass("h-10");
});

it.each(["ttft", "tps"] as const)("shows log unavailable instead of empty for grouped %s percentile", (metric) => {
  render(
    <MetricTrendChart
      buckets={[]}
      availableMetrics={[metric]}
      metric={metric}
      dim="model"
      onDimChange={() => {}}
      groupedOnly
      logUnavailable
      title={metric}
    />,
  );

  expect(screen.getByText("trend.logUnavailable")).toBeInTheDocument();
  expect(screen.queryByText("noData")).not.toBeInTheDocument();
});

it.each([
  ["ttft", "ttft_ms", 120],
  ["tps", "tps", 24],
  ["cache_hit_rate", "cache_hit_rate", 75],
] as const)("shows log unavailable for %s avg or ratio data", (metric, field, value) => {
  render(
    <MetricTrendChart
      buckets={[{ ts: 1, label: "00:00", cost: 1, requests: 2, tokens: 3, [field]: value }]}
      metric={metric}
      logUnavailable
      title={metric}
    />,
  );

  expect(screen.getByText("trend.logUnavailable")).toBeInTheDocument();
  expect(screen.queryByTestId(`line-${field}`)).not.toBeInTheDocument();
});

it("keeps core sum metrics rendered while log storage is unavailable", () => {
  render(
    <MetricTrendChart
      buckets={[{ ts: 1, label: "00:00", cost: 1, requests: 2, tokens: 3 }]}
      metric="tokens"
      logUnavailable
      title="tokens"
    />,
  );
  expect(screen.getByTestId("line-tokens")).toBeInTheDocument();
  expect(screen.queryByText("trend.logUnavailable")).not.toBeInTheDocument();
});

it("uses the same stable identity color and dash for grouped lines and their legend", () => {
  render(<MetricTrendChart
    buckets={[]}
    grouped={{ metric: "tokens", stat: "sum", unit: "tokens", estimated: false, buckets: [{ ts: 1, label: "x", series: { alpha: 1 } }], series_order: ["alpha"] }}
    metric="tokens"
    dim="model"
    onDimChange={() => {}}
    groupedOnly
    title="tokens"
  />);
  const line = screen.getByTestId("line-alpha");
  expect(line).toHaveAttribute("data-stroke", chartColorForSeries("alpha"));
  expect(line).toHaveAttribute("data-dash", chartDashForSeries("alpha") ?? "solid");
  const button = screen.getByRole("button", { name: "alpha" });
  expect(button.querySelector("span")?.style.backgroundColor).toBe(chartColorForSeries("alpha"));
});
