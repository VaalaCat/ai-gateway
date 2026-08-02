import { render, screen } from "@testing-library/react";
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
