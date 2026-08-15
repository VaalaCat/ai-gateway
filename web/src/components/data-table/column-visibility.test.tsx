import { fireEvent, render, screen } from "@testing-library/react";
import type { Table } from "@tanstack/react-table";
import { expect, it, vi } from "vitest";

import { ColumnVisibility } from "./column-visibility";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));

it("uses a 36px mobile trigger and a 32px desktop trigger", () => {
  const table = {
    getAllLeafColumns: () => [],
  } as unknown as Table<unknown>;

  render(<ColumnVisibility table={table} />);

  expect(screen.getByRole("button", { name: "columns" })).toHaveClass(
    "size-9",
    "sm:h-8",
    "sm:w-auto",
  );
});

it("opens portaled column labels with comfortable mobile touch sizing when requested", () => {
  const table = {
    getAllLeafColumns: () => [{
      id: "name",
      getCanHide: () => true,
      getIsVisible: () => true,
      toggleVisibility: vi.fn(),
      columnDef: { header: "Name" },
    }],
  } as unknown as Table<unknown>;

  render(<ColumnVisibility table={table} mobileTouchSize="comfortable" />);
  fireEvent.click(screen.getByRole("button", { name: "columns" }));

  const label = screen.getByRole("checkbox", { name: "Name" }).closest("label");
  expect(label).toHaveClass("max-sm:min-h-11", "max-sm:min-w-44");
  expect(document.querySelector('[data-slot="popover-content"]')).toContainElement(label);
});
