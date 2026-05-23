"use client";

import { Fragment, useState, useEffect } from "react";
import {
  ColumnDef,
  SortingState,
  VisibilityState,
  ExpandedState,
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  getExpandedRowModel,
  useReactTable,
  Row,
} from "@tanstack/react-table";
import { useTranslations } from "next-intl";

import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Skeleton } from "@/components/ui/skeleton";
import { TooltipProvider } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { DataTablePagination } from "./pagination";
import { ColumnVisibility } from "./column-visibility";

interface DataTableProps<TData, TValue> {
  columns: ColumnDef<TData, TValue>[];
  data: TData[];
  loading?: boolean;
  total?: number;
  page?: number;
  pageSize?: number;
  pageCount?: number;
  onPaginationChange?: (page: number, pageSize: number) => void;
  toolbar?: React.ReactNode;
  defaultColumnVisibility?: VisibilityState;
  columnVisibilityState?: VisibilityState;
  onColumnVisibilityChange?: (state: VisibilityState) => void;
  storageKey?: string;
  renderExpandedRow?: (row: Row<TData>) => React.ReactNode;
  rowSelection?: Record<string, boolean>;
  onRowSelectionChange?: (selection: Record<string, boolean>) => void;
  expandedState?: ExpandedState;
  onExpandedStateChange?: (state: ExpandedState) => void;
  getRowId?: (row: TData, index: number) => string;
}

export function DataTable<TData, TValue>({
  columns,
  data,
  loading = false,
  total,
  page = 1,
  pageSize = 10,
  pageCount = 1,
  onPaginationChange,
  toolbar,
  defaultColumnVisibility,
  storageKey,
  renderExpandedRow,
  rowSelection,
  onRowSelectionChange,
  expandedState,
  onExpandedStateChange,
  columnVisibilityState,
  onColumnVisibilityChange,
  getRowId,
}: DataTableProps<TData, TValue>) {
  const t = useTranslations("common");
  const [sorting, setSorting] = useState<SortingState>([]);
  const [internalExpanded, setInternalExpanded] = useState<ExpandedState>({});
  const isExpandedControlled = expandedState !== undefined && onExpandedStateChange !== undefined;
  const expanded = isExpandedControlled ? expandedState! : internalExpanded;

  const handleExpandedChange = (updaterOrValue: ExpandedState | ((old: ExpandedState) => ExpandedState)) => {
    const oldState = expanded;
    const nextRaw = typeof updaterOrValue === "function" ? updaterOrValue(oldState) : updaterOrValue;
    // 单展开模式：只在 renderExpandedRow 提供时启用，找新打开的那个保留
    let next = nextRaw;
    if (renderExpandedRow && typeof nextRaw === "object" && nextRaw !== null) {
      const oldOpen = new Set(
        Object.entries(oldState as Record<string, boolean>).filter(([, v]) => v).map(([k]) => k),
      );
      const openRows = Object.entries(nextRaw as Record<string, boolean>).filter(([, v]) => v);
      if (openRows.length > 1) {
        const newlyOpened = openRows.find(([k]) => !oldOpen.has(k));
        next = newlyOpened ? { [newlyOpened[0]]: true } : (nextRaw as ExpandedState);
      }
    }
    if (isExpandedControlled) {
      onExpandedStateChange!(next as ExpandedState);
    } else {
      setInternalExpanded(next as ExpandedState);
    }
  };

  const isVisibilityControlled =
    columnVisibilityState !== undefined && onColumnVisibilityChange !== undefined;
  const [internalColumnVisibility, setInternalColumnVisibility] = useState<VisibilityState>(() => {
    if (storageKey && typeof window !== "undefined") {
      const saved = localStorage.getItem(`col-vis-${storageKey}`);
      if (saved) {
        try { return JSON.parse(saved); } catch { /* ignore */ }
      }
    }
    return defaultColumnVisibility ?? {};
  });
  const columnVisibility = isVisibilityControlled
    ? columnVisibilityState!
    : internalColumnVisibility;

  useEffect(() => {
    if (isVisibilityControlled) return;
    if (storageKey && typeof window !== "undefined") {
      localStorage.setItem(`col-vis-${storageKey}`, JSON.stringify(internalColumnVisibility));
    }
  }, [internalColumnVisibility, storageKey, isVisibilityControlled]);

  const handleColumnVisibilityChange = (
    updaterOrValue: VisibilityState | ((old: VisibilityState) => VisibilityState),
  ) => {
    const next = typeof updaterOrValue === "function"
      ? (updaterOrValue as (old: VisibilityState) => VisibilityState)(columnVisibility)
      : updaterOrValue;
    if (isVisibilityControlled) {
      onColumnVisibilityChange!(next);
    } else {
      setInternalColumnVisibility(next);
    }
  };

  const table = useReactTable({
    data,
    columns,
    state: { sorting, columnVisibility, expanded, ...(onRowSelectionChange ? { rowSelection: rowSelection ?? {} } : {}) },
    onSortingChange: setSorting,
    onColumnVisibilityChange: handleColumnVisibilityChange,
    onExpandedChange: handleExpandedChange,
    enableRowSelection: !!onRowSelectionChange,
    onRowSelectionChange: onRowSelectionChange
      ? (updater: any) => {
          const next = typeof updater === "function" ? updater(rowSelection ?? {}) : updater;
          onRowSelectionChange(next);
        }
      : undefined,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getExpandedRowModel: getExpandedRowModel(),
    ...(getRowId ? { getRowId } : {}),
  });

  return (
    <TooltipProvider delayDuration={200}>
      <div className="min-w-0 space-y-4">
        {toolbar}
        {defaultColumnVisibility && (
          <div className="flex justify-end">
            <ColumnVisibility table={table} />
          </div>
        )}
        <div className="rounded-md border overflow-x-auto">
          <Table className="text-body">
            <TableHeader>
              {table.getHeaderGroups().map((headerGroup) => (
                <TableRow key={headerGroup.id}>
                  {headerGroup.headers.map((header) => (
                    <TableHead key={header.id} className="whitespace-nowrap">
                      {header.isPlaceholder
                        ? null
                        : flexRender(
                            header.column.columnDef.header,
                            header.getContext()
                          )}
                    </TableHead>
                  ))}
                </TableRow>
              ))}
            </TableHeader>
            <TableBody>
              {loading ? (
                Array.from({ length: 5 }).map((_, i) => (
                  <TableRow key={`skeleton-${i}`}>
                    {columns.map((_, j) => (
                      <TableCell key={`skeleton-${i}-${j}`}>
                        <Skeleton className="h-5 w-full" />
                      </TableCell>
                    ))}
                  </TableRow>
                ))
              ) : table.getRowModel().rows.length > 0 ? (
                table.getRowModel().rows.map((row) => {
                  const isExpanded = row.getIsExpanded();
                  return (
                    <Fragment key={row.id}>
                      <TableRow
                        className={cn(
                          renderExpandedRow && "cursor-pointer hover:bg-muted/30",
                          renderExpandedRow && isExpanded && "bg-muted/40",
                        )}
                        data-state={isExpanded ? "expanded" : undefined}
                        onClick={renderExpandedRow ? () => row.toggleExpanded() : undefined}
                      >
                        {row.getVisibleCells().map((cell) => (
                          <TableCell key={cell.id} className="whitespace-nowrap">
                            {flexRender(
                              cell.column.columnDef.cell,
                              cell.getContext()
                            )}
                          </TableCell>
                        ))}
                      </TableRow>
                      {isExpanded && renderExpandedRow && (
                        <TableRow>
                          <TableCell colSpan={row.getVisibleCells().length} className="bg-muted/50 p-4">
                            {renderExpandedRow(row)}
                          </TableCell>
                        </TableRow>
                      )}
                    </Fragment>
                  );
                })
              ) : (
                <TableRow>
                  <TableCell
                    colSpan={columns.length}
                    className="h-24 text-center text-muted-foreground"
                  >
                    {t("noData")}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
        <div className="flex items-center justify-between">
          <div className="text-sm text-muted-foreground">
            {total !== undefined && t("total", { count: total })}
          </div>
          {onPaginationChange && (
            <DataTablePagination
              page={page}
              pageSize={pageSize}
              pageCount={pageCount}
              onPaginationChange={onPaginationChange}
            />
          )}
        </div>
      </div>
    </TooltipProvider>
  );
}
