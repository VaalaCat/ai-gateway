import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { MarketShareChart } from "./market-share-chart";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

vi.mock("recharts", () => ({
  BarChart: ({ children, maxBarSize, barCategoryGap }: React.PropsWithChildren<{
    maxBarSize?: number;
    barCategoryGap?: string;
  }>) => (
    <div
      data-testid="bar-chart"
      data-max-bar-size={maxBarSize}
      data-bar-category-gap={barCategoryGap}
    >
      {children}
    </div>
  ),
  Bar: ({ dataKey, radius, fill }: { dataKey: string; radius?: number | number[]; fill?: string }) => (
    <div data-testid={`bar-${dataKey}`} data-key={dataKey} data-radius={JSON.stringify(radius)} data-fill={fill} />
  ),
  CartesianGrid: () => null,
  XAxis: () => null,
  YAxis: ({ tickFormatter }: { tickFormatter?: (value: number) => string }) => (
    <div
      data-testid="y-axis"
      data-formatted-ticks={[0, 50.00000000000001, 100.00000000000001]
        .map((value) => tickFormatter?.(value) ?? String(value))
        .join(",")}
    />
  ),
}));

vi.mock("@/components/business/chart-card", () => ({
  ChartCard: ({ children }: React.PropsWithChildren) => <section>{children}</section>,
}));

vi.mock("@/components/business/chart-option-select", () => ({
  ChartOptionSelect: () => null,
}));

vi.mock("@/components/ui/chart", () => ({
  ChartContainer: ({ children }: React.PropsWithChildren) => <div>{children}</div>,
  ChartLegend: () => null,
  ChartTooltip: () => null,
  ChartTooltipContent: () => null,
}));

const BUCKETS = [
  {
    ts: 1_753_228_800,
    label: "Jul 23",
    series: { alpha: 10, beta: 20, gamma: 30 },
  },
];

function renderChart(seriesOrder: string[]) {
  return render(
    <MarketShareChart
      buckets={BUCKETS}
      seriesOrder={seriesOrder}
      mode="absolute"
      onModeChange={() => {}}
      dim="model"
      onDimChange={() => {}}
    />,
  );
}

describe("MarketShareChart", () => {
  it("renders every stacked bar with square corners", () => {
    renderChart(["alpha", "beta", "gamma"]);

    const bars = screen.getAllByTestId(/^bar-(alpha|beta|gamma)$/);
    expect(bars).toHaveLength(3);
    for (const bar of bars) {
      expect(bar).toHaveAttribute("data-radius", "0");
    }
  });

  it("uses dense, readable market-share columns", () => {
    renderChart(["alpha", "beta"]);

    expect(screen.getByTestId("bar-chart")).toHaveAttribute("data-max-bar-size", "64");
    expect(screen.getByTestId("bar-chart")).toHaveAttribute("data-bar-category-gap", "8%");
  });

  it("keeps a single-series bar square", () => {
    renderChart(["alpha"]);

    expect(screen.getByTestId("bar-alpha")).toHaveAttribute("data-radius", "0");
  });

  it("keeps a series fill stable when ordering changes", () => {
    const { rerender } = renderChart(["alpha", "beta"]);
    const fill = screen.getByTestId("bar-alpha").getAttribute("data-fill");
    rerender(<MarketShareChart buckets={BUCKETS} seriesOrder={["beta", "alpha"]} mode="absolute" onModeChange={() => {}} dim="model" onDimChange={() => {}} />);
    expect(screen.getByTestId("bar-alpha")).toHaveAttribute("data-fill", fill);
  });

  it("renders stable compact percentage ticks at zero, midpoint, and upper bound", () => {
    render(
      <MarketShareChart
        buckets={BUCKETS}
        seriesOrder={["alpha", "beta"]}
        mode="percent"
        onModeChange={() => {}}
        dim="model"
        onDimChange={() => {}}
      />,
    );

    expect(screen.getByTestId("y-axis")).toHaveAttribute("data-formatted-ticks", "0%,50%,100%");
  });
});
