import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ScrollableChartLegend, type ScrollableChartLegendItem } from "./scrollable-chart-legend";

const longName = "provider/model-with-an-extremely-long-unbroken-name-that-must-never-resize-the-chart";

function makeItems(count: number): ScrollableChartLegendItem[] {
  return Array.from({ length: count }, (_, index) => ({
    key: `series-${index}`,
    label: index === 19 ? longName : `模型 ${index + 1}`,
    color: `var(--chart-${(index % 5) + 1})`,
  }));
}

describe("ScrollableChartLegend", () => {
  it("keeps 20 series in one independent horizontal scroll region", () => {
    render(<ScrollableChartLegend ariaLabel="Chart series" items={makeItems(20)} onToggle={() => {}} />);

    const region = screen.getByRole("region", { name: "Chart series" });
    expect(region).toHaveClass(
      "h-full",
      "max-w-full",
      "min-w-0",
      "overflow-x-auto",
      "overscroll-contain",
      "px-px",
      "pb-1",
    );
    expect(region.parentElement).toHaveAttribute("data-slot", "chart-legend-shell");
    expect(region.parentElement).toHaveClass("h-10", "min-w-0", "overflow-hidden");
    expect(screen.getByRole("list")).toHaveClass("w-max", "min-w-full", "flex-nowrap");
    const buttons = screen.getAllByRole("button");
    expect(buttons).toHaveLength(20);
    for (const button of buttons) {
      expect(button).toHaveClass("focus-visible:ring-inset");
    }
    expect(screen.getByRole("button", { name: longName })).toHaveClass("shrink-0");
    expect(screen.getByText(longName)).toHaveClass("truncate");
    expect(screen.getByText(longName)).toHaveAttribute("title", longName);
  });

  it("keeps long RTL and Chinese labels focusable and toggles by stable key", async () => {
    const user = userEvent.setup();
    const onToggle = vi.fn();
    render(
      <div dir="rtl">
        <ScrollableChartLegend
          ariaLabel="سلاسل المخطط"
          items={[
            { key: "rtl", label: "نموذج-طويل-جدا-بدون-فواصل", color: "var(--chart-1)" },
            { key: "zh", label: "中文模型", color: "var(--chart-2)" },
          ]}
          onToggle={onToggle}
        />
      </div>,
    );

    const rtlItem = screen.getByRole("button", { name: "نموذج-طويل-جدا-بدون-فواصل" });
    rtlItem.focus();
    expect(rtlItem).toHaveFocus();
    await user.keyboard("{Enter}");
    expect(onToggle).toHaveBeenCalledWith("rtl");
    expect(screen.getByText("中文模型")).toHaveClass("break-words");
  });

  it("renders an empty payload without a stray scroll region", () => {
    const { container } = render(<ScrollableChartLegend items={[]} onToggle={() => {}} />);
    expect(container).toBeEmptyDOMElement();
  });
});
