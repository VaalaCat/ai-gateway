import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { FilterBar, FilterField } from "./filter-bar";

describe("FilterBar/FilterField", () => {
  it("renders label above control", () => {
    render(
      <FilterField label="令牌">
        <input aria-label="ctl" />
      </FilterField>,
    );
    expect(screen.getByText("令牌")).toBeInTheDocument();
    expect(screen.getByLabelText("ctl")).toBeInTheDocument();
  });

  it("FilterBar lays out multiple fields with wrap classes", () => {
    const { container } = render(
      <FilterBar>
        <FilterField label="a"><input /></FilterField>
        <FilterField label="b"><input /></FilterField>
      </FilterBar>,
    );
    const bar = container.firstElementChild!;
    expect(bar.className).toContain("flex-wrap");
    expect(bar.className).toContain("items-end");
    expect(screen.getByText("a")).toBeInTheDocument();
    expect(screen.getByText("b")).toBeInTheDocument();
  });

  it("boundary: className passthrough on both components", () => {
    const { container } = render(
      <FilterBar className="extra-bar">
        <FilterField label="x" className="extra-field"><input /></FilterField>
      </FilterBar>,
    );
    expect(container.querySelector(".extra-bar")).not.toBeNull();
    expect(container.querySelector(".extra-field")).not.toBeNull();
  });
});
