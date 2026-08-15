import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { DataTablePagination } from "./pagination";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));

describe("DataTablePagination accessibility", () => {
  it("names both direction buttons and disables them on the only page", () => {
    render(
      <DataTablePagination
        page={1}
        pageSize={20}
        pageCount={1}
        onPaginationChange={() => {}}
      />,
    );

    expect(screen.getByRole("button", { name: "previousPage" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "nextPage" })).toBeDisabled();
  });

  it("keeps both named buttons enabled on a middle page", () => {
    const onPaginationChange = vi.fn();
    render(
      <DataTablePagination
        page={2}
        pageSize={20}
        pageCount={3}
        onPaginationChange={onPaginationChange}
      />,
    );

    expect(screen.getByRole("button", { name: "previousPage" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "nextPage" })).toBeEnabled();
    screen.getByRole("button", { name: "previousPage" }).click();
    screen.getByRole("button", { name: "nextPage" }).click();
    expect(onPaginationChange).toHaveBeenNthCalledWith(1, 1, 20);
    expect(onPaginationChange).toHaveBeenNthCalledWith(2, 3, 20);
  });
});
