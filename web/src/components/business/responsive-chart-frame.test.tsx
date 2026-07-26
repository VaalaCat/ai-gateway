import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { Button } from "@/components/ui/button";
import { ChartContainer } from "@/components/ui/chart";
import { ChartCard } from "./chart-card";
import { ResponsiveChartFrame } from "./responsive-chart-frame";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

describe("ResponsiveChartFrame", () => {
  it("places a responsive-side legend beside the plot only at desktop", () => {
    render(<ResponsiveChartFrame legendPlacement="responsive-side" legend={<div>Ranked legend</div>}><div>Plot</div></ResponsiveChartFrame>);
    expect(screen.getByTestId("responsive-chart-frame")).toHaveClass("lg:grid", "lg:grid-cols-[minmax(0,1fr)_minmax(12rem,0.9fr)]");
    expect(screen.getByTestId("responsive-chart-legend")).toHaveClass("lg:max-h-[17.5rem]", "lg:overflow-y-auto");
  });
  it.each(["loading", "empty", "error", "chart"])(
    "keeps the %s state inside the same stable plot geometry",
    (state) => {
      render(
        <ResponsiveChartFrame minHeight={224} aspect="16/9">
          <div>{state}</div>
        </ResponsiveChartFrame>,
      );

      const plot = screen.getByTestId("responsive-chart-plot");
      expect(plot).toHaveStyle({ minHeight: "224px", aspectRatio: "16 / 9" });
      expect(plot).toHaveStyle({ containerType: "inline-size" });
      expect(plot).toHaveClass("w-full", "min-w-0", "max-h-[17.5rem]", "overflow-hidden");
      expect(plot).not.toHaveClass("overflow-visible");
      expect(screen.getByText(state)).toBeInTheDocument();
    },
  );

  it("keeps the legend outside the bounded plot region", () => {
    render(
      <ResponsiveChartFrame legend={<div>Series identities</div>}>
        <div>Plot</div>
      </ResponsiveChartFrame>,
    );

    const plot = screen.getByTestId("responsive-chart-plot");
    const legend = screen.getByTestId("responsive-chart-legend");
    expect(plot).not.toContainElement(legend);
    expect(plot.nextElementSibling).toBe(legend);
  });

  it("supports a 375px container without viewport-sized typography", () => {
    render(
      <div style={{ width: 375 }}>
        <ResponsiveChartFrame minHeight={232} aspect="4/3">
          <div>移动端图表</div>
        </ResponsiveChartFrame>
      </div>,
    );

    const frame = screen.getByTestId("responsive-chart-frame");
    expect(frame.parentElement).toHaveStyle({ width: "375px" });
    expect(screen.getByTestId("responsive-chart-plot")).toHaveStyle({
      minHeight: "232px",
      aspectRatio: "4 / 3",
    });
    expect(screen.getByTestId("responsive-chart-plot")).toHaveClass("w-full", "min-w-0", "overflow-hidden");
    expect(frame.className).not.toMatch(/\btext-\[[^\]]*(vw|cqw)/);
  });

  it("keeps a real 280px ChartContainer and every ChartCard state in one frame geometry", () => {
    const { rerender } = render(
      <ChartCard title="Usage" chartFrame={{}}>
        <ChartContainer config={{}} className="h-[280px] w-full">
          <div>Data</div>
        </ChartContainer>
      </ChartCard>,
    );

    const loadedPlot = screen.getByTestId("responsive-chart-plot");
    expect(loadedPlot).toHaveStyle({ minHeight: "224px", aspectRatio: "16 / 9" });
    expect(loadedPlot.querySelector('[data-slot="chart"]')).toHaveClass("h-[280px]");

    for (const state of ["loading", "empty", "error"] as const) {
      rerender(
        <ChartCard
          title="Usage"
          chartFrame={{}}
          loading={state === "loading"}
          empty={state === "empty"}
          error={state === "error" ? "Unavailable" : undefined}
        >
          <ChartContainer config={{}} className="h-[280px] w-full">
            <div>Data</div>
          </ChartContainer>
        </ChartCard>,
      );
      expect(screen.getByTestId("responsive-chart-plot")).toHaveStyle({
        minHeight: "224px",
        aspectRatio: "16 / 9",
      });
    }
  });

  it("keeps wrapping header controls outside the plot", () => {
    render(
      <ChartCard title="A very long chart title" action={<Button>Filter</Button>} chartFrame={{}}>
        <span>Plot</span>
      </ChartCard>,
    );

    const action = screen.getByRole("button", { name: "Filter" }).parentElement;
    expect(action).toHaveClass("flex-wrap", "min-w-0");
    expect(screen.getByTestId("responsive-chart-plot")).not.toContainElement(action);
  });

  it("places a ChartCard legend below its single production frame", () => {
    render(
      <ChartCard title="Usage" chartFrame={{ legend: <div>Top 20 series</div> }}>
        <ChartContainer config={{}} className="h-[260px] w-full">
          <div>Data</div>
        </ChartContainer>
      </ChartCard>,
    );

    expect(screen.getAllByTestId("responsive-chart-frame")).toHaveLength(1);
    const plot = screen.getByTestId("responsive-chart-plot");
    const legend = screen.getByTestId("responsive-chart-legend");
    expect(plot.nextElementSibling).toBe(legend);
    expect(legend).toHaveTextContent("Top 20 series");
  });

  it("keeps a legacy ChartContainer and all-hidden message in normal vertical document flow", () => {
    const { container } = render(
      <ChartCard title="Tokens">
        <ChartContainer config={{}} className="h-[260px] w-full">
          <div>Data</div>
        </ChartContainer>
        <p>all series hidden</p>
      </ChartCard>,
    );

    const content = container.querySelector('[data-slot="card-content"]');
    const chart = container.querySelector('[data-slot="chart"]');
    const message = screen.getByText("all series hidden");
    expect(screen.queryByTestId("responsive-chart-plot")).not.toBeInTheDocument();
    expect(chart?.parentElement).toBe(content);
    expect(message.parentElement).toBe(content);
  });

  it("does not force a legacy mobile donut composite layout into a fixed plot", () => {
    const { container } = render(
      <div style={{ width: 375 }}>
        <ChartCard title="Distribution">
          <div data-testid="donut-composite" className="flex flex-col items-center gap-4">
            <div style={{ height: 240 }}>Donut</div>
            <div>Legend rows</div>
          </div>
        </ChartCard>
      </div>,
    );

    const content = container.querySelector('[data-slot="card-content"]');
    const composite = screen.getByTestId("donut-composite");
    expect(screen.queryByTestId("responsive-chart-plot")).not.toBeInTheDocument();
    expect(composite.parentElement).toBe(content);
    expect(content).not.toHaveClass("overflow-visible", "max-h-[17.5rem]");
  });
});
