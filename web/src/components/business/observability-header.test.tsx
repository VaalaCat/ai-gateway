import { render } from "@testing-library/react";
import { expect, it, vi } from "vitest";

import { ObservabilityHeader } from "./observability-header";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

vi.mock("@/components/business/date-range-inputs", () => ({
  DateRangeInputs: () => <div data-testid="date-range" />,
}));

it("keeps the title and controls stacked until the large breakpoint", () => {
  const { container } = render(
    <ObservabilityHeader
      title="Dashboard"
      subtitle="Overview"
      range={{ start: 100, end: 200, gran: "day" }}
      onRangeChange={() => {}}
      onRefresh={() => {}}
    />,
  );

  const headingRow = container.querySelector("h1")?.parentElement?.parentElement;
  expect(headingRow).toHaveClass("lg:flex-row", "lg:items-start", "lg:justify-between");
  expect(headingRow).not.toHaveClass("md:flex-row", "md:items-start", "md:justify-between");
});
