import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";

import { DatePicker } from "./date-picker";

it("renders default size without h-8", () => {
  render(<DatePicker value="" onChange={() => {}} placeholder="pick" />);
  const btn = screen.getByRole("button");
  expect(btn.className).not.toContain("h-8");
  expect(btn.className).toContain("sm:w-[160px]");
});

it("renders sm size with h-8 and 150px width", () => {
  render(<DatePicker value="" onChange={() => {}} placeholder="pick" size="sm" />);
  const btn = screen.getByRole("button");
  expect(btn.className).toContain("h-8");
  expect(btn.className).toContain("sm:w-[150px]");
  expect(btn.className).not.toContain("sm:w-[160px]");
});

it("still renders the selected value text in sm size", () => {
  render(<DatePicker value="2026-07-01" onChange={() => {}} size="sm" />);
  expect(screen.getByRole("button", { name: /2026-07-01/ })).toBeInTheDocument();
});
