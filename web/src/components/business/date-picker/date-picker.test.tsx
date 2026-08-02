import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";

import { DatePicker, parseDateStr } from "./date-picker";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));

it("renders default size without h-8", () => {
  const { container } = render(<DatePicker value="" onChange={() => {}} placeholder="pick" />);
  const btn = screen.getByRole("button");
  expect(btn.className).not.toContain("h-8");
  expect(container.firstElementChild).toHaveClass("relative", "w-full", "sm:w-[160px]");
});

it("renders sm size with h-8 and 150px width", () => {
  const { container } = render(
    <DatePicker value="" onChange={() => {}} placeholder="pick" size="sm" />,
  );
  const btn = screen.getByRole("button");
  expect(btn.className).toContain("h-8");
  expect(container.firstElementChild).toHaveClass("sm:w-[150px]");
  expect(container.firstElementChild).not.toHaveClass("sm:w-[160px]");
});

it("still renders the selected value text in sm size", () => {
  render(<DatePicker value="2026-07-01" onChange={() => {}} size="sm" />);
  expect(screen.getByRole("button", { name: /2026-07-01/ })).toHaveClass("w-full");
});

it("keeps the same fixed wrapper width and adds an in-control clear button", () => {
  const empty = render(<DatePicker value="" onChange={() => {}} />);
  expect(empty.container.firstElementChild).toHaveClass("w-full", "sm:w-[160px]");
  empty.unmount();

  const selected = render(<DatePicker value="2026-07-01" onChange={() => {}} />);
  expect(selected.container.firstElementChild).toHaveClass("w-full", "sm:w-[160px]");
  expect(screen.getByRole("button", { name: "clearDate" })).toBeInTheDocument();
});

it("clears once without opening the calendar", async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  render(<DatePicker value="2026-07-01" onChange={onChange} />);

  const clear = screen.getByRole("button", { name: "clearDate" });
  const trigger = screen.getByRole("button", { name: /2026-07-01/ });
  expect(clear.parentElement).toBe(trigger.parentElement);
  await user.click(clear);

  expect(onChange).toHaveBeenCalledOnce();
  expect(onChange).toHaveBeenCalledWith("");
  expect(screen.queryByRole("grid")).not.toBeInTheDocument();
});

it("treats impossible and malformed dates as empty without throwing", () => {
  expect(parseDateStr("2026-02-31")).toBeUndefined();
  expect(parseDateStr("2026-7-1")).toBeUndefined();
  expect(parseDateStr("")).toBeUndefined();
  expect(() => render(<DatePicker value="2026-02-31" onChange={() => {}} placeholder="pick" />)).not.toThrow();
  expect(screen.getByRole("button", { name: "pick" })).toBeInTheDocument();
});
