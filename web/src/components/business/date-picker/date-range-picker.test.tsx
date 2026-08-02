import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import { DateRangePicker, isDateRangeValid } from "./date-range-picker";

let mobile = false;

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/hooks/use-mobile", () => ({ useIsMobile: () => mobile }));

beforeEach(() => {
  mobile = false;
});

function renderRange(
  overrides: Partial<React.ComponentProps<typeof DateRangePicker>> = {},
) {
  const onValueChange = vi.fn();
  const result = render(
    <DateRangePicker
      value={{ startDate: "2026-07-10", endDate: "2026-07-12" }}
      onValueChange={onValueChange}
      {...overrides}
    />,
  );
  return { ...result, onValueChange };
}

async function openCalendar(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: /2026-07-10.*2026-07-12/ }));
}

function dayButton(day: number) {
  return screen.getByRole("button", {
    name: new RegExp(`July ${day}(?:st|nd|rd|th), 2026`),
  });
}

it("emits a same-day range on the first click, then a sorted range and closes", async () => {
  const user = userEvent.setup();
  const { onValueChange } = renderRange();
  await openCalendar(user);

  await user.click(dayButton(20));
  expect(onValueChange).toHaveBeenNthCalledWith(1, {
    startDate: "2026-07-20",
    endDate: "2026-07-20",
  });
  expect(screen.getAllByRole("grid")).toHaveLength(2);

  await user.click(dayButton(15));
  expect(onValueChange).toHaveBeenNthCalledWith(2, {
    startDate: "2026-07-15",
    endDate: "2026-07-20",
  });
  expect(screen.queryByRole("grid")).not.toBeInTheDocument();
});

it("preserves the active selection when the parent echoes the first click", async () => {
  const user = userEvent.setup();
  const { onValueChange, rerender } = renderRange();
  await openCalendar(user);
  await user.click(dayButton(20));

  rerender(
    <DateRangePicker
      value={{ startDate: "2026-07-20", endDate: "2026-07-20" }}
      onValueChange={onValueChange}
    />,
  );
  await user.click(dayButton(15));

  expect(onValueChange).toHaveBeenNthCalledWith(2, {
    startDate: "2026-07-15",
    endDate: "2026-07-20",
  });
  expect(screen.queryByRole("grid")).not.toBeInTheDocument();
});

it("resets the active selection when the parent replaces the controlled range", async () => {
  const user = userEvent.setup();
  const { onValueChange, rerender } = renderRange();
  await openCalendar(user);
  await user.click(dayButton(20));

  rerender(
    <DateRangePicker
      value={{ startDate: "2026-07-05", endDate: "2026-07-06" }}
      onValueChange={onValueChange}
    />,
  );
  expect(screen.getByRole("button", { name: /2026-07-05.*2026-07-06/ })).toBeInTheDocument();
  await user.click(dayButton(25));

  expect(onValueChange).toHaveBeenNthCalledWith(2, {
    startDate: "2026-07-25",
    endDate: "2026-07-25",
  });
  expect(screen.getAllByRole("grid")).toHaveLength(2);
});

it("resets an echoed selection when the parent later clears the controlled range", async () => {
  const user = userEvent.setup();
  const { onValueChange, rerender } = renderRange();
  await openCalendar(user);
  await user.click(dayButton(20));

  rerender(
    <DateRangePicker
      value={{ startDate: "2026-07-20", endDate: "2026-07-20" }}
      onValueChange={onValueChange}
    />,
  );
  rerender(
    <DateRangePicker value={{ startDate: "", endDate: "" }} onValueChange={onValueChange} />,
  );
  expect(screen.getByRole("button", { name: "selectDate" })).toBeInTheDocument();
  await user.click(dayButton(15));

  expect(onValueChange).toHaveBeenNthCalledWith(2, {
    startDate: "2026-07-15",
    endDate: "2026-07-15",
  });
  expect(screen.getAllByRole("grid")).toHaveLength(2);
});

it("clears both dates once without opening the popover", async () => {
  const user = userEvent.setup();
  const { onValueChange } = renderRange();
  const clear = screen.getByRole("button", { name: "clearDateRange" });
  const trigger = screen.getByRole("button", { name: /2026-07-10.*2026-07-12/ });
  expect(clear.parentElement).toBe(trigger.parentElement);

  await user.click(clear);

  expect(onValueChange).toHaveBeenCalledOnce();
  expect(onValueChange).toHaveBeenCalledWith({ startDate: "", endDate: "" });
  expect(screen.queryByRole("grid")).not.toBeInTheDocument();
});

it("reserves text space for the absolute clear button", () => {
  renderRange();

  const trigger = screen.getByRole("button", { name: /2026-07-10.*2026-07-12/ });
  expect(trigger).toHaveAttribute("data-slot", "date-range-trigger");
  const text = trigger.querySelector("span");
  expect(text).toHaveClass("min-w-0", "flex-1", "truncate", "pr-7");
});

it("uses compact height and switches between desktop and mobile month counts", async () => {
  const user = userEvent.setup();
  const desktop = renderRange({ size: "sm" });
  expect(screen.getByRole("button", { name: /2026-07-10.*2026-07-12/ })).toHaveClass("h-8");
  await openCalendar(user);
  expect(screen.getAllByRole("grid")).toHaveLength(2);
  desktop.unmount();

  mobile = true;
  renderRange();
  await openCalendar(user);
  expect(screen.getAllByRole("grid")).toHaveLength(1);
});

it("shows invalid, reversed, and partial controlled ranges as empty without throwing", () => {
  expect(isDateRangeValid({ startDate: "", endDate: "" })).toBe(true);
  expect(isDateRangeValid({ startDate: "2026-02-31", endDate: "2026-03-01" })).toBe(false);
  expect(isDateRangeValid({ startDate: "2026-07-12", endDate: "2026-07-10" })).toBe(false);
  expect(isDateRangeValid({ startDate: "2026-07-10", endDate: "" })).toBe(false);

  expect(() =>
    renderRange({ value: { startDate: "2026-02-31", endDate: "2026-03-01" } }),
  ).not.toThrow();
  expect(screen.getByRole("button", { name: "selectDate" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "clearDateRange" })).not.toBeInTheDocument();
});

it("includes the anchor day when enforcing maxDays", async () => {
  const user = userEvent.setup();
  renderRange({ maxDays: 7 });
  await openCalendar(user);
  await user.click(dayButton(10));

  expect(dayButton(16)).toBeEnabled();
  expect(dayButton(17)).toBeDisabled();
  expect(dayButton(4)).toBeEnabled();
  expect(dayButton(3)).toBeDisabled();
});

it.each([1, 1.5, 0, -3])("normalizes a finite maxDays value of %s to at least one whole day", async (maxDays) => {
  const user = userEvent.setup();
  renderRange({ maxDays });
  await openCalendar(user);
  await user.click(dayButton(10));

  expect(dayButton(10)).toBeEnabled();
  expect(dayButton(9)).toBeDisabled();
  expect(dayButton(11)).toBeDisabled();
});

it.each([Number.NaN, Number.POSITIVE_INFINITY])(
  "does not limit selection for a non-finite maxDays value of %s",
  async (maxDays) => {
    const user = userEvent.setup();
    renderRange({ maxDays });
    await openCalendar(user);
    await user.click(dayButton(10));

    expect(dayButton(3)).toBeEnabled();
    expect(dayButton(17)).toBeEnabled();
  },
);
