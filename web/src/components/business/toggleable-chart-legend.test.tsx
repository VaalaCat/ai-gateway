import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { ChartConfig } from "@/components/ui/chart";
import { ToggleableChartLegend, useHiddenSeries } from "./toggleable-chart-legend";

const CONFIG: ChartConfig = {
  a: { label: "Alpha", color: "#111" },
  b: { label: "Beta", color: "#222" },
};
const PAYLOAD = [
  { value: "a", color: "#111" },
  { value: "b", color: "#222" },
];

describe("ToggleableChartLegend", () => {
  it("renders one toggle button per payload entry with config label", () => {
    render(
      <ToggleableChartLegend payload={PAYLOAD} config={CONFIG} hidden={new Set()} onToggle={() => {}} />,
    );
    expect(screen.getByRole("button", { name: "Alpha" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "Beta" })).toBeInTheDocument();
  });

  it("click calls onToggle with series key; hidden entry shows pressed=false + line-through", async () => {
    const onToggle = vi.fn();
    const user = userEvent.setup();
    render(
      <ToggleableChartLegend payload={PAYLOAD} config={CONFIG} hidden={new Set(["b"])} onToggle={onToggle} />,
    );
    await user.click(screen.getByRole("button", { name: "Alpha" }));
    expect(onToggle).toHaveBeenCalledWith("a");
    const hiddenBtn = screen.getByRole("button", { name: "Beta" });
    expect(hiddenBtn).toHaveAttribute("aria-pressed", "false");
    expect(hiddenBtn.querySelector(".line-through")).not.toBeNull();
  });

  it("boundary: empty/undefined payload renders nothing", () => {
    const { container } = render(
      <ToggleableChartLegend payload={[]} config={CONFIG} hidden={new Set()} onToggle={() => {}} />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});

function Harness({ order }: { order: string[] }) {
  const { hidden, toggle } = useHiddenSeries(order);
  const payload = order.map((k) => ({ value: k, color: "#000" }));
  const config = Object.fromEntries(order.map((k) => [k, { label: k, color: "#000" }]));
  return <ToggleableChartLegend payload={payload} config={config} hidden={hidden} onToggle={toggle} />;
}

describe("useHiddenSeries", () => {
  it("toggle hides and clicking again unhides", async () => {
    const user = userEvent.setup();
    render(<Harness order={["a", "b"]} />);
    await user.click(screen.getByRole("button", { name: "a" }));
    expect(screen.getByRole("button", { name: "a" })).toHaveAttribute("aria-pressed", "false");
    await user.click(screen.getByRole("button", { name: "a" }));
    expect(screen.getByRole("button", { name: "a" })).toHaveAttribute("aria-pressed", "true");
  });

  it("clears hidden set when seriesOrder content changes, keeps it for same content new ref", async () => {
    const user = userEvent.setup();
    function Wrapper() {
      const [n, setN] = useState(0);
      // 每次渲染都构造新数组引用:内容相同时隐藏集必须保留(防 ?? [] 新引用循环),内容变了才清空
      const order = n < 2 ? ["a", "b"] : ["c", "d"];
      return (
        <>
          <button type="button" onClick={() => setN((v) => v + 1)}>bump</button>
          <Harness order={[...order]} />
        </>
      );
    }
    render(<Wrapper />);
    await user.click(screen.getByRole("button", { name: "a" }));
    await user.click(screen.getByRole("button", { name: "bump" })); // n=1, 同内容新引用
    expect(screen.getByRole("button", { name: "a" })).toHaveAttribute("aria-pressed", "false");
    await user.click(screen.getByRole("button", { name: "bump" })); // n=2, 内容变为 c,d
    expect(screen.getByRole("button", { name: "c" })).toHaveAttribute("aria-pressed", "true");
  });
});
