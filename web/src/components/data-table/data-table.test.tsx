import { useState } from "react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ColumnDef } from "@tanstack/react-table";
import { describe, expect, it, vi } from "vitest";

import { DataTable } from "./data-table";

vi.mock("./column-visibility", () => ({
  ColumnVisibility: ({ table }: { table: unknown }) => (
    <div data-testid="column-visibility" data-has-table={String(Boolean(table))} />
  ),
}));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: { from?: number; to?: number; total?: number }) => {
    if (key === "paginationInfo") return `${values?.from}-${values?.to}/${values?.total}`;
    return key;
  },
}));

interface RowData {
  id: string;
  name: string;
}

const nameColumn: ColumnDef<RowData> = {
  accessorKey: "name",
  header: "Name",
};

describe("DataTable", () => {
  it("renders rows through the TanStack row model", () => {
    render(<DataTable columns={[nameColumn]} data={[{ id: "a", name: "Alpha" }]} getRowId={(row) => row.id} />);

    expect(screen.getByRole("columnheader", { name: "Name" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "Alpha" })).toBeInTheDocument();
  });

  it("applies controlled row-selection updaters without changing the public API", async () => {
    const user = userEvent.setup();
    const columns: ColumnDef<RowData>[] = [{
      id: "select",
      header: "Select",
      cell: ({ row }) => (
        <button type="button" onClick={row.getToggleSelectedHandler()}>
          {row.getIsSelected() ? "Selected" : "Select row"}
        </button>
      ),
    }, nameColumn];

    function Harness() {
      const [selection, setSelection] = useState<Record<string, boolean>>({});
      return (
        <DataTable
          columns={columns}
          data={[{ id: "a", name: "Alpha" }]}
          getRowId={(row) => row.id}
          rowSelection={selection}
          onRowSelectionChange={setSelection}
        />
      );
    }

    render(<Harness />);
    await user.click(screen.getByRole("button", { name: "Select row" }));
    expect(screen.getByRole("button", { name: "Selected" })).toBeInTheDocument();
  });

  it("keeps the table instance and internal sorting state stable across rerenders", async () => {
    const user = userEvent.setup();
    const columns: ColumnDef<RowData>[] = [{
      accessorKey: "name",
      header: ({ column }) => (
        <button type="button" onClick={() => column.toggleSorting(false)}>Name</button>
      ),
    }];
    const { rerender } = render(
      <DataTable columns={columns} data={[{ id: "b", name: "Beta" }, { id: "a", name: "Alpha" }]} getRowId={(row) => row.id} />,
    );
    await user.click(screen.getByRole("button", { name: "Name" }));
    let rows = screen.getAllByRole("row").slice(1);
    expect(within(rows[0]).getByText("Alpha")).toBeInTheDocument();

    rerender(
      <DataTable columns={columns} data={[{ id: "c", name: "Charlie" }, { id: "b", name: "Beta" }]} getRowId={(row) => row.id} />,
    );
    rows = screen.getAllByRole("row").slice(1);
    expect(within(rows[0]).getByText("Beta")).toBeInTheDocument();
  });

  it("uses fixed layout and declared column widths when requested", () => {
    const columns: ColumnDef<RowData>[] = [
      nameColumn,
      { id: "actions", size: 48, header: "Actions", cell: () => "Menu" },
    ];
    const { container } = render(
      <DataTable
        columns={columns}
        data={[{ id: "a", name: "Alpha" }]}
        getRowId={(row) => row.id}
        tableLayout="fixed"
      />,
    );

    expect(container.querySelector("[data-slot=table]")).toHaveClass("table-fixed");
    expect(container.querySelector("col[data-column-id=actions]")).toHaveStyle({ width: "48px" });
  });

  it("keeps automatic table layout by default", () => {
    const { container } = render(
      <DataTable columns={[nameColumn]} data={[{ id: "a", name: "Alpha" }]} getRowId={(row) => row.id} />,
    );

    expect(container.querySelector("[data-slot=table]")).not.toHaveClass("table-fixed");
    expect(container.querySelector("colgroup")).not.toBeInTheDocument();
  });

  it("keeps a standalone column visibility control for a ReactNode toolbar", () => {
    render(
      <DataTable
        columns={[nameColumn]}
        data={[]}
        toolbar={<div>plain-toolbar</div>}
        defaultColumnVisibility={{}}
      />,
    );

    expect(screen.getByText("plain-toolbar")).toBeInTheDocument();
    expect(screen.getByTestId("column-visibility")).toHaveAttribute("data-has-table", "true");
  });

  it("passes the TanStack table to a render-function toolbar", () => {
    const renderToolbar = vi.fn((_table: unknown) => <div>render-toolbar</div>);

    render(
      <DataTable
        columns={[nameColumn]}
        data={[]}
        toolbar={renderToolbar}
        defaultColumnVisibility={{}}
      />,
    );

    expect(screen.getByText("render-toolbar")).toBeInTheDocument();
    expect(renderToolbar).toHaveBeenCalledOnce();
    expect(renderToolbar.mock.calls[0][0]).toMatchObject({ getVisibleLeafColumns: expect.any(Function) });
  });

  it("does not duplicate column visibility for a render-function toolbar", () => {
    render(
      <DataTable
        columns={[nameColumn]}
        data={[]}
        toolbar={(table) => <div data-testid="custom-column-visibility">{String(Boolean(table))}</div>}
        defaultColumnVisibility={{}}
      />,
    );

    expect(screen.getByTestId("custom-column-visibility")).toHaveTextContent("true");
    expect(screen.queryByTestId("column-visibility")).not.toBeInTheDocument();
  });
});
