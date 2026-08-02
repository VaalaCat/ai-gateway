import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";

import { TrendChart } from "./trend-chart";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

vi.mock("recharts", () => ({
  CartesianGrid: () => null,
  LineChart: ({ children }: React.PropsWithChildren) => <div>{children}</div>,
  Line: ({ dot, activeDot }: { dot: boolean; activeDot?: unknown }) => (
    <span data-testid="trend-line" data-dot={JSON.stringify(dot)} data-active-dot={JSON.stringify(activeDot)} />
  ),
  XAxis: () => null,
  YAxis: () => null,
}));

vi.mock("@/components/business/chart-card", () => ({
  ChartCard: ({ children }: React.PropsWithChildren) => <section>{children}</section>,
}));

vi.mock("@/components/business/chart-option-select", () => ({
  ChartOptionSelect: () => null,
}));

vi.mock("@/components/ui/chart", () => ({
  ChartContainer: ({ children }: React.PropsWithChildren) => <div>{children}</div>,
  ChartTooltip: () => null,
}));

const bucket = {
  ts: 1,
  label: "2026-07-24",
  cost: 100,
  requests: 10,
  tokens: 20,
};

it("keeps the line clean when a trend contains one bucket", () => {
  render(<TrendChart title="Trend" buckets={[bucket]} />);

  expect(screen.getByTestId("trend-line")).toHaveAttribute("data-dot", "false");
  expect(screen.getByTestId("trend-line")).toHaveAttribute("data-active-dot", '{"r":4,"strokeWidth":2}');
});

it("keeps point markers hidden until hover", () => {
  render(<TrendChart title="Trend" buckets={[bucket, { ...bucket, ts: 2, label: "2026-07-25" }]} />);

  expect(screen.getByTestId("trend-line")).toHaveAttribute("data-dot", "false");
  expect(screen.getByTestId("trend-line")).toHaveAttribute("data-active-dot", '{"r":4,"strokeWidth":2}');
});
