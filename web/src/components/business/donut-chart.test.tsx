import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { DonutChart } from "./donut-chart";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("recharts", () => ({
  Cell: ({ opacity }: { opacity?: number }) => <span data-testid="cell" data-opacity={opacity} />,
  Pie: ({ data, children }: React.PropsWithChildren<{ data: Array<{ name: string; label: string }> }>) => (
    <div
      data-testid="pie"
      data-names={data.map((item) => item.name).join("|")}
      data-labels={data.map((item) => item.label).join("|")}
    >{children}</div>
  ),
  PieChart: ({ children }: React.PropsWithChildren) => <div>{children}</div>,
}));
vi.mock("@/components/ui/chart", () => ({
  ChartContainer: ({ children, config }: React.PropsWithChildren<{ config: Record<string, { label?: React.ReactNode }> }>) => (
    <div
      data-slot="chart"
      data-config-keys={Object.keys(config).join("|")}
      data-config-labels={Object.values(config).map((item) => String(item.label)).join("|")}
    >{children}</div>
  ),
  ChartTooltip: () => null,
  ChartTooltipContent: () => null,
}));

function names(slices: Array<{ name: string; value: number }>, topN = 2) {
  render(<DonutChart title="Models" slices={slices} topN={topN} othersLabel="Others" />);
  return screen.getByTestId("pie").getAttribute("data-names")?.split("|");
}

describe("DonutChart top n folding", () => {
  it("merges an existing reserved aggregate with the tail under the stable others identity", () => {
    expect(names([
      { name: "others", value: 100 },
      { name: "alpha", value: 30 },
      { name: "beta", value: 20 },
      { name: "gamma", value: 10 },
    ])).toEqual(["alpha", "beta", "others"]);
    expect(screen.getByTestId("pie")).toHaveAttribute("data-labels", "alpha|beta|Others");
  });

  it("reserves only exact others while keeping real other and 其他 entities independent", () => {
    expect(names([
      { name: "other", value: 30 },
      { name: "其他", value: 20 },
      { name: "alpha", value: 10 },
      { name: "others", value: 5 },
    ])).toEqual(["other", "其他", "others"]);
    expect(screen.getByRole("button", { name: /^other46\.2%$/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^其他30\.8%$/ })).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /Others/ })).toHaveLength(1);
  });

  it("sorts and folds raw entities when no aggregate is present", () => {
    expect(names([
      { name: "small", value: 1 },
      { name: "large", value: 9 },
      { name: "middle", value: 5 },
    ])).toEqual(["large", "middle", "others"]);
  });

  it.each([
    ["其他", "其他"],
    ["Others", "Others"],
  ] as const)("keeps real %s and the localized aggregate as independent identities", (entityName, othersLabel) => {
    render(<DonutChart
      title="Models"
      slices={[
        { name: entityName, value: 30 },
        { name: "alpha", value: 10 },
        { name: "others", value: 5 },
      ]}
      topN={2}
      othersLabel={othersLabel}
    />);

    expect(screen.getByTestId("pie")).toHaveAttribute("data-names", `${entityName}|alpha|others`);
    expect(screen.getByTestId("pie")).toHaveAttribute("data-labels", `${entityName}|alpha|${othersLabel}`);
    expect(screen.getByTestId("pie").closest("[data-slot=chart]")).toHaveAttribute("data-config-keys", `${entityName}|alpha|others`);
    expect(screen.getByTestId("pie").closest("[data-slot=chart]")).toHaveAttribute("data-config-labels", `${entityName}|alpha|${othersLabel}`);
    const sameLabelButtons = screen.getAllByRole("button", { name: new RegExp(`^${entityName}`) });
    expect(sameLabelButtons).toHaveLength(2);
    fireEvent.click(sameLabelButtons[0]);
    expect(sameLabelButtons[0]).toHaveAttribute("aria-pressed", "false");
    expect(sameLabelButtons[1]).toHaveAttribute("aria-pressed", "true");
    expect(screen.getAllByTestId("cell").map((cell) => cell.dataset.opacity)).toEqual(["0", "1", "1"]);
  });

  it("does not append a zero aggregate at the top n boundary", () => {
    expect(names([{ name: "a", value: 2 }, { name: "b", value: 1 }])).toEqual(["a", "b"]);
  });

  it("keeps loaded, loading, empty, and error in the same responsive-side plot geometry", () => {
    const props = { title: "Models", slices: [{ name: "a", value: 1 }], legendLabel: "Series" };
    const { rerender } = render(<DonutChart {...props} />);
    const style = screen.getByTestId("responsive-chart-plot").getAttribute("style");
    expect(screen.getByTestId("responsive-chart-plot")).toHaveStyle({ minHeight: "280px" });
    expect(screen.getByTestId("responsive-chart-frame")).toHaveClass("lg:grid");
    expect(screen.getByRole("region", { name: "Series" })).toHaveClass("overflow-y-auto");
    for (const state of [
      <DonutChart key="loading" {...props} loading />,
      <DonutChart key="empty" {...props} slices={[]} empty />,
      <DonutChart key="error" {...props} error="offline" />,
    ]) {
      rerender(state);
      expect(screen.getByTestId("responsive-chart-plot")).toHaveAttribute("style", style);
    }
  });
});
