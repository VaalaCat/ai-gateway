import { useState } from "react";
import { fireEvent, render, screen, within } from "@testing-library/react";
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

interface ExpandableRowData {
  id: number;
  name: string;
}

const nameColumn: ColumnDef<RowData> = {
  accessorKey: "name",
  header: "Name",
};

const expandableNameColumn: ColumnDef<ExpandableRowData> = {
  accessorKey: "name",
  header: "Name",
};

function tableFor(data: ExpandableRowData[]) {
  return (
    <DataTable
      columns={[expandableNameColumn]}
      data={data}
      getRowId={(row) => String(row.id)}
      renderExpandedRow={(row) => <div>details-{row.original.id}</div>}
    />
  );
}

function renderExpandableTable(data: ExpandableRowData[]) {
  return render(tableFor(data));
}

function renderInteractiveExpandableTable() {
  const columns: ColumnDef<ExpandableRowData>[] = [
    expandableNameColumn,
    {
      id: "actions",
      header: "Actions",
      cell: () => (
        <div>
          <button type="button"><span data-testid="copy-icon">copy</span></button>
          <a href="#edit">edit</a>
          <input aria-label="filter" />
          <div data-row-interaction>
            <span data-testid="custom-interaction-child">custom interaction</span>
          </div>
        </div>
      ),
    },
  ];

  return render(
    <DataTable
      columns={columns}
      data={[{ id: 9, name: "forecast" }]}
      getRowId={(row) => String(row.id)}
      renderExpandedRow={(row) => <div>details-{row.original.id}</div>}
    />,
  );
}

describe("DataTable", () => {
  it("exposes expandable rows as native focusable table rows with keyboard toggles", async () => {
    const user = userEvent.setup();
    renderExpandableTable([{ id: 9, name: "forecast" }]);

    const row = screen.getByText("forecast").closest("tr");
    expect(row).toHaveAttribute("tabindex", "0");
    expect(row).toHaveAttribute("aria-expanded", "false");

    row?.focus();
    await user.keyboard("{Enter}");
    expect(screen.getByText("details-9")).toBeVisible();
    expect(row).toHaveAttribute("aria-expanded", "true");

    expect(fireEvent.keyDown(row!, { key: " " })).toBe(false);
    expect(screen.queryByText("details-9")).not.toBeInTheDocument();
  });

  it("expands one stable row at a time when the row is clicked", async () => {
    const user = userEvent.setup();
    renderExpandableTable([{ id: 9, name: "forecast" }, { id: 10, name: "radar" }]);

    await user.click(screen.getByText("forecast"));
    expect(screen.getByText("details-9")).toBeVisible();
    await user.click(screen.getByText("radar"));
    expect(screen.queryByText("details-9")).not.toBeInTheDocument();
    expect(screen.getByText("details-10")).toBeVisible();
  });

  it("does not toggle a row from buttons links inputs or selected text", async () => {
    const user = userEvent.setup();
    renderInteractiveExpandableTable();

    await user.click(screen.getByTestId("copy-icon"));
    expect(screen.queryByText("details-9")).not.toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "edit" }));
    expect(screen.queryByText("details-9")).not.toBeInTheDocument();
    await user.click(screen.getByRole("textbox", { name: "filter" }));
    expect(screen.queryByText("details-9")).not.toBeInTheDocument();
    await user.click(screen.getByTestId("custom-interaction-child"));
    expect(screen.queryByText("details-9")).not.toBeInTheDocument();

    const getSelection = vi.spyOn(window, "getSelection").mockReturnValue({ isCollapsed: false } as Selection);
    await user.click(screen.getByText("forecast"));
    expect(screen.queryByText("details-9")).not.toBeInTheDocument();
    getSelection.mockRestore();
  });

  it("does not toggle a row from keyboard events bubbling from child controls", () => {
    renderInteractiveExpandableTable();

    fireEvent.keyDown(screen.getByRole("button", { name: "copy" }), { key: "Enter" });
    expect(screen.queryByText("details-9")).not.toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole("link", { name: "edit" }), { key: "Enter" });
    expect(screen.queryByText("details-9")).not.toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole("textbox", { name: "filter" }), { key: " " });
    expect(screen.queryByText("details-9")).not.toBeInTheDocument();
  });

  it("keeps expansion aligned after rows reorder by stable id", async () => {
    const user = userEvent.setup();
    const view = renderExpandableTable([{ id: 9, name: "forecast" }, { id: 10, name: "radar" }]);

    await user.click(screen.getByText("forecast"));
    view.rerender(tableFor([{ id: 10, name: "radar" }, { id: 9, name: "forecast" }]));
    expect(screen.getByText("details-9")).toBeVisible();
  });

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

  it("anchors opt-in expanded content to the horizontal scroller viewport", () => {
    const { container } = render(
      <DataTable
        columns={[expandableNameColumn]}
        data={[{ id: 9, name: "forecast" }]}
        getRowId={(row) => String(row.id)}
        expandedState={{ "9": true }}
        onExpandedStateChange={() => {}}
        renderExpandedRow={(row) => <div>details-{row.original.id}</div>}
        expandedRowWidth="viewport"
      />,
    );

    const scroller = container.querySelector<HTMLElement>('[data-slot="table-container"]');
    const expandedContent = container.querySelector<HTMLElement>('[data-slot="data-table-expanded-content"]');
    expect(scroller).toHaveAttribute("data-expanded-row-width", "viewport");
    expect(scroller).toHaveClass("@container/data-table");
    expect(scroller).toContainElement(expandedContent);
    expect(expandedContent).toHaveClass("sticky", "left-0", "w-[100cqw]", "p-4");
    expect(expandedContent?.parentElement).toHaveRole("cell");

    Object.defineProperties(scroller!, {
      clientWidth: { configurable: true, value: 320 },
      scrollWidth: { configurable: true, value: 720 },
    });
    scroller!.scrollLeft = 400;
    fireEvent.scroll(scroller!);

    expect(scroller).toHaveProperty("scrollLeft", 400);
    expect(scroller).toContainElement(screen.getByText("details-9"));
  });

  it("keeps expanded content table-width by default", async () => {
    const user = userEvent.setup();
    const { container } = renderExpandableTable([{ id: 9, name: "forecast" }]);

    await user.click(screen.getByText("forecast"));

    const scroller = container.querySelector('[data-slot="table-container"]');
    expect(scroller).toHaveAttribute("data-expanded-row-width", "table");
    expect(scroller).not.toHaveClass("@container/data-table");
    expect(container.querySelector('[data-slot="data-table-expanded-content"]')).toBeNull();
    expect(screen.getByText("details-9").parentElement).toHaveRole("cell");
  });

  it("applies responsive classes from column metadata to both headers and cells", () => {
    const columns: ColumnDef<RowData>[] = [{
      ...nameColumn,
      meta: { headerClassName: "sm:hidden", cellClassName: "sm:hidden" },
    }];

    render(<DataTable columns={columns} data={[{ id: "a", name: "Alpha" }]} getRowId={(row) => row.id} />);

    expect(screen.getByRole("columnheader", { name: "Name" })).toHaveClass("sm:hidden");
    expect(screen.getByRole("cell", { name: "Alpha" })).toHaveClass("sm:hidden");
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
