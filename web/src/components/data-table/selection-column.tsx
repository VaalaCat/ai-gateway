"use client";

import type { ColumnDef } from "@tanstack/react-table";

import { Checkbox } from "@/components/ui/checkbox";

interface SelectionColumnLabels {
  selectAll: string;
  selectRow: string;
}
export function createSelectionColumn<T>(labels: SelectionColumnLabels): ColumnDef<T> {
  return {
    id: "select",
    size: 44,
    enableHiding: false,
    enableSorting: false,
    header: ({ table }) => (
      <Checkbox
        aria-label={labels.selectAll}
        checked={table.getIsAllPageRowsSelected() || (table.getIsSomePageRowsSelected() && "indeterminate")}
        onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
      />
    ),
    cell: ({ row }) => (
      <Checkbox
        aria-label={labels.selectRow}
        checked={row.getIsSelected()}
        onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
        onClick={(event) => event.stopPropagation()}
      />
    ),
  };
}
