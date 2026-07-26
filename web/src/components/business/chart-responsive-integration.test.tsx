import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";

import { StackedAreaChart } from "./stacked-area-chart";
import { BoundedChartTooltip } from "./bounded-chart-tooltip";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

vi.mock("recharts", () => ({
  Area: ({ dataKey, strokeDasharray }: { dataKey: string; strokeDasharray?: string }) => <span data-testid={`area-${dataKey}`} data-dash={strokeDasharray ?? "solid"} />,
  AreaChart: ({ children }: React.PropsWithChildren) => <div>{children}</div>,
  CartesianGrid: () => null,
  XAxis: () => null,
  YAxis: () => null,
}));

vi.mock("@/components/ui/chart", () => {
  return {
    ChartContainer: ({ children }: React.PropsWithChildren) => <div data-slot="chart">{children}</div>,
    ChartLegend: () => null,
    ChartTooltip: ({ content }: { content: React.ReactNode }) => <>{content}</>,
    ChartTooltipContent: () => null,
  };
});

const SERIES = Array.from({ length: 20 }, (_, index) =>
  `provider/model-with-a-deliberately-long-name-${index + 1}`,
);

it("keeps a stable plot frame while a top 20 legend scrolls outside it", () => {
  const { rerender } = render(
    <StackedAreaChart
      title="Usage"
      buckets={[{ ts: 1, label: "00:00", series: Object.fromEntries(SERIES.map((key) => [key, 1])) }]}
      seriesOrder={SERIES}
    />,
  );

  const plot = screen.getByTestId("responsive-chart-plot");
  const initialStyle = plot.getAttribute("style");
  const legend = screen.getByRole("region", { name: "series" });
  expect(legend).toHaveClass("overflow-x-auto");
  expect(plot).not.toContainElement(legend);
  expect(screen.getAllByRole("button")).toHaveLength(20);
  for (const area of screen.getAllByTestId(/^area-/)) expect(area).toHaveAttribute("data-dash", "solid");

  rerender(
    <StackedAreaChart
      title="Usage"
      buckets={[{ ts: 1, label: "00:00", series: { [SERIES[0]]: 1 } }]}
      seriesOrder={[SERIES[0]]}
    />,
  );
  expect(screen.getByTestId("responsive-chart-plot")).toHaveAttribute("style", initialStyle);
});

it("uses the same responsive plot slot for empty chart data", () => {
  render(<StackedAreaChart title="Usage" buckets={[]} seriesOrder={SERIES} />);

  expect(screen.getByTestId("responsive-chart-plot")).toBeInTheDocument();
  expect(screen.getByText("noData")).toBeInTheDocument();
  expect(screen.queryByRole("region", { name: "series" })).not.toBeInTheDocument();
});

it("keeps long tooltip content inside a bounded scrollable surface", () => {
  render(
    <BoundedChartTooltip
      active
      payload={[{ name: SERIES[0], value: 123 }] as never}
    />,
  );

  expect(screen.getByRole("tooltip")).toHaveClass(
    "max-h-[min(50vh,9rem)]",
    "max-w-[min(20rem,calc(100vw-2rem),calc(100cqw-5rem))]",
    "overflow-y-auto",
  );
});
