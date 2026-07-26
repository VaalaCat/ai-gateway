import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ChartTooltip } from "@/components/ui/chart";
import { BoundedChartTooltip } from "./bounded-chart-tooltip";

vi.mock("recharts", async (importOriginal) => {
  const actual = await importOriginal<typeof import("recharts")>();
  return {
    ...actual,
    Tooltip: ({ wrapperStyle, allowEscapeViewBox, offset }: {
      wrapperStyle?: React.CSSProperties;
      allowEscapeViewBox?: unknown;
      offset?: unknown;
    }) => (
      <div
        data-testid="recharts-tooltip-props"
        data-wrapper-style={JSON.stringify(wrapperStyle)}
        data-allow-escape={JSON.stringify(allowEscapeViewBox)}
        data-offset={JSON.stringify(offset)}
      />
    ),
  };
});

const longName = "provider/model-with-an-extremely-long-unbroken-name-that-cannot-cover-nearby-controls";

function renderTooltip(label: string, value: number | string = 12345) {
  return render(
    <BoundedChartTooltip
      active
      label="2026-07-24"
      payload={[
        {
          dataKey: "series",
          graphicalItemId: "series",
          name: label,
          value,
          color: "var(--chart-1)",
          payload: {},
        },
      ]}
    />,
  );
}

describe("BoundedChartTooltip", () => {
  it("bounds width and height within a 375px viewport collision margin", () => {
    renderTooltip(longName);
    const tooltip = screen.getByRole("tooltip");
    expect(tooltip).toHaveClass(
      "max-w-[min(20rem,calc(100vw-2rem),calc(100cqw-5rem))]",
      "max-h-[min(50vh,9rem)]",
      "overflow-y-auto",
      "overscroll-contain",
      "touch-pan-y",
    );
    expect(tooltip).not.toHaveAttribute("data-collision-margin");
  });

  it("breaks long English, Chinese, and RTL labels while keeping values aligned", () => {
    const { rerender } = renderTooltip(longName);
    expect(screen.getByText(longName)).toHaveClass("break-words", "min-w-0");
    expect(screen.getByText("12,345")).toHaveClass("tabular-nums", "text-right");

    rerender(
      <BoundedChartTooltip
        active
        payload={[
          {
            dataKey: "series",
            graphicalItemId: "series",
            name: "中文模型نموذج",
            value: 1,
            payload: {},
          },
        ]}
      />,
    );
    expect(screen.getByText("中文模型نموذج")).toHaveClass("break-words");
  });

  it("renders nothing for inactive or empty payloads", () => {
    const { container } = render(
      <BoundedChartTooltip active={false} payload={[]} />,
    );
    expect(container.querySelector('[role="tooltip"]')).toBeNull();
  });

  it("passes a real interactive 16px collision box to Recharts without escaping the plot", () => {
    render(<ChartTooltip />);

    const tooltip = screen.getByTestId("recharts-tooltip-props");
    expect(JSON.parse(tooltip.dataset.wrapperStyle ?? "{}")).toMatchObject({
      boxSizing: "border-box",
      maxWidth: "100%",
      paddingBlock: "16px",
      paddingInline: "16px",
      pointerEvents: "auto",
    });
    expect(JSON.parse(tooltip.dataset.allowEscape ?? "null")).toEqual({ x: false, y: false });
  });

  it("keeps a Top 20 payload in one scrollable tooltip surface", () => {
    const payload = Array.from({ length: 20 }, (_, index) => ({
      dataKey: `series-${index}`,
      graphicalItemId: `series-${index}`,
      name: `模型 ${index + 1}`,
      value: index,
      payload: {},
    }));
    render(<BoundedChartTooltip active payload={payload} />);

    const tooltip = screen.getByRole("tooltip");
    expect(tooltip).toHaveClass("pointer-events-auto", "overflow-y-auto", "touch-pan-y");
    expect(screen.getByText("模型 20")).toBeInTheDocument();
  });

  it("sorts numeric values descending and keeps equal values in payload order", () => {
    render(<BoundedChartTooltip active payload={[
      { name: "first-equal", graphicalItemId: "first-equal", value: 5, color: "red", payload: {} },
      { name: "largest", graphicalItemId: "largest", value: 9, color: "blue", payload: {} },
      { name: "second-equal", graphicalItemId: "second-equal", value: 5, color: "green", payload: {} },
    ]} />);
    const labels = screen.getAllByText(/largest|first-equal|second-equal/).map((node) => node.textContent);
    expect(labels).toEqual(["largest", "first-equal", "second-equal"]);
  });

  it("sorts finite numeric strings as numbers and keeps non-numeric values stable at the end", () => {
    render(<BoundedChartTooltip active payload={[
      { name: "first-invalid", graphicalItemId: "first-invalid", value: "n/a", color: "red", payload: {} },
      { name: "numeric-number", graphicalItemId: "numeric-number", value: 9, color: "blue", payload: {} },
      { name: "numeric-string", graphicalItemId: "numeric-string", value: "12.5", color: "green", payload: {} },
      { name: "second-invalid", graphicalItemId: "second-invalid", value: "", color: "orange", payload: {} },
    ]} />);
    const labels = screen.getAllByText(/numeric-string|numeric-number|first-invalid|second-invalid/).map((node) => node.textContent);
    expect(labels).toEqual(["numeric-string", "numeric-number", "first-invalid", "second-invalid"]);
  });

  it("retains the item color indicator when a custom formatter is used", () => {
    render(<BoundedChartTooltip
      active
      payload={[{ name: "alpha", graphicalItemId: "alpha", value: 7, color: "rgb(1, 2, 3)", payload: {} }]}
      formatter={(value) => <span>custom {String(value)}</span>}
    />);
    expect(screen.getByText("custom 7")).toBeInTheDocument();
    expect(screen.getByTestId("tooltip-color-indicator")).toHaveStyle({ backgroundColor: "rgb(1, 2, 3)" });
  });
});
