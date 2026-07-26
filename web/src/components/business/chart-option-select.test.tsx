import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { ChartOptionSelect } from "./chart-option-select";

// Radix Select 在 jsdom 缺这几个 API,本测试文件内本地补齐
beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false;
  Element.prototype.setPointerCapture ??= () => {};
  Element.prototype.releasePointerCapture ??= () => {};
  Element.prototype.scrollIntoView ??= () => {};
});

const OPTIONS = [
  { value: "cost", label: "成本" },
  { value: "tokens", label: "Tokens" },
] as const;

describe("ChartOptionSelect", () => {
  it("renders prefix label and current value in trigger", () => {
    render(
      <ChartOptionSelect value="cost" onValueChange={() => {}} options={OPTIONS} label="指标" />,
    );
    const trigger = screen.getByRole("combobox", { name: "指标" });
    expect(trigger).toHaveTextContent("指标");
    expect(trigger).toHaveTextContent("成本");
  });

  it("calls onValueChange with picked value", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(
      <ChartOptionSelect value="cost" onValueChange={onChange} options={OPTIONS} label="指标" />,
    );
    await user.click(screen.getByRole("combobox", { name: "指标" }));
    await user.click(await screen.findByRole("option", { name: "Tokens" }));
    expect(onChange).toHaveBeenCalledWith("tokens");
  });

  it("boundary: empty options renders trigger without crash", () => {
    render(
      <ChartOptionSelect value="x" onValueChange={() => {}} options={[]} label="视图" />,
    );
    expect(screen.getByRole("combobox", { name: "视图" })).toBeInTheDocument();
  });
});
