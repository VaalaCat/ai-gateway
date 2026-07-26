import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { TokensChart } from "./byok-trend-charts";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

vi.mock("recharts", () => ({
  CartesianGrid: () => null,
  LineChart: ({ children }: React.PropsWithChildren) => <div>{children}</div>,
  Line: ({ dataKey, hide }: { dataKey: string; hide?: boolean }) => (
    <div data-testid={`line-${dataKey}`} data-hidden={String(Boolean(hide))} />
  ),
  XAxis: () => null,
  YAxis: () => null,
}));

vi.mock("@/components/business/chart-card", () => ({
  ChartCard: ({ title, children }: React.PropsWithChildren<{ title: string }>) => (
    <section aria-label={title}>{children}</section>
  ),
}));

const TOKEN_PAYLOAD = [
  { value: "prompt_tokens", color: "var(--chart-1)" },
  { value: "completion_tokens", color: "var(--chart-2)" },
  { value: "cache_read_tokens", color: "var(--chart-3)" },
  { value: "cache_write_tokens", color: "var(--chart-4)" },
];

vi.mock("@/components/ui/chart", () => ({
  ChartContainer: ({ children }: React.PropsWithChildren) => <div>{children}</div>,
  ChartLegend: ({
    content,
  }: {
    content: (props: { payload: typeof TOKEN_PAYLOAD }) => React.ReactNode;
  }) => <>{content({ payload: TOKEN_PAYLOAD })}</>,
  ChartTooltip: () => null,
  ChartTooltipContent: () => null,
}));

const ITEM = {
  date: "2026-07-23",
  request_count: 1,
  success_count: 1,
  failed_count: 0,
  prompt_tokens: 10,
  completion_tokens: 20,
  cache_read_tokens: 30,
  cache_write_tokens: 40,
  input_cost: 0.1,
  output_cost: 0.2,
  total_cost: 0.3,
};

function renderTokensChart() {
  render(<TokensChart items={[ITEM]} loading={false} />);
  return within(screen.getByRole("region", { name: "chartTokens" }));
}

describe("TokensChart", () => {
  it("renders four shared legend buttons as initially visible", () => {
    const chart = renderTokensChart();

    const buttons = chart.getAllByRole("button");
    expect(buttons).toHaveLength(4);
    for (const button of buttons) {
      expect(button).toHaveAttribute("aria-pressed", "true");
    }
  });

  it("hides a series on click and restores it on the next click", async () => {
    const user = userEvent.setup();
    const chart = renderTokensChart();
    const button = chart.getByRole("button", { name: "breakdownPromptTokens" });
    const line = chart.getByTestId("line-prompt_tokens");

    await user.click(button);
    expect(button).toHaveAttribute("aria-pressed", "false");
    expect(line).toHaveAttribute("data-hidden", "true");

    await user.click(button);
    expect(button).toHaveAttribute("aria-pressed", "true");
    expect(line).toHaveAttribute("data-hidden", "false");
  });

  it("shows the all-hidden hint only after every series is hidden", async () => {
    const user = userEvent.setup();
    const chart = renderTokensChart();
    const buttons = chart.getAllByRole("button");

    expect(chart.queryByText("chartAllHidden")).not.toBeInTheDocument();
    for (const button of buttons.slice(0, -1)) {
      await user.click(button);
      expect(chart.queryByText("chartAllHidden")).not.toBeInTheDocument();
    }

    await user.click(buttons.at(-1)!);
    expect(chart.getByText("chartAllHidden")).toBeInTheDocument();
  });
});
