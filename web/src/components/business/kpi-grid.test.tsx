import { render, screen, cleanup } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";

import { KpiGrid } from "./kpi-grid";

afterEach(cleanup);

it.each([
  [90, "text-emerald-600"],
  [50, "text-emerald-600"],
  [20, "text-amber-600"],
  [5, "text-destructive"],
  [0, "text-destructive"],
])("renders %s%% remaining with the matching bar length and color", (progress, color) => {
  render(<KpiGrid items={[{
    key: "quota", label: "Remaining / total", value: `${progress} / 100`, progress,
    threshold: { warn: 20, critical: 5, semantic: "low-bad" },
  }]} />);
  const bar = screen.getByRole("progressbar", { name: "Remaining / total" });
  expect(bar).toHaveAttribute("aria-valuenow", String(progress));
  expect(bar.firstElementChild).toHaveStyle({ width: `${progress}%` });
  expect(bar.firstElementChild).toHaveClass(color);
  expect(screen.getByText(`${progress} / 100`)).toHaveClass(color);
});
