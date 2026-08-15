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
  createTable,
  Row,
  type Table as TanStackTable,
  type TableOptions,
  type TableOptionsResolved,
  type RowSelectionState,
  type Updater,
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

function useDataTableInstance<TData>(options: TableOptions<TData>): TanStackTable<TData> {
  const resolvedOptions: TableOptionsResolved<TData> = {
    state: {},
    onStateChange: () => {},
    renderFallbackValue: null,
    ...options,
  };
  const [table] = useState(() => createTable(resolvedOptions));
  const [state, setState] = useState(() => table.initialState);

  table.setOptions((previous) => ({
    ...previous,
    ...options,
    state: { ...state, ...options.state },
    onStateChange: (updater) => {
      setState(updater);
      options.onStateChange?.(updater);
    },
  }));
  return table;
}

type DataTableToolbar<TData> =
  | React.ReactNode
  | ((table: TanStackTable<TData>) => React.ReactNode);

interface DataTableProps<TData, TValue> {
  columns: ColumnDef<TData, TValue>[];
  data: TData[];
  loading?: boolean;
  total?: number;
  page?: number;
  pageSize?: number;
  pageCount?: number;
  paginationDisabled?: boolean;
  onPaginationChange?: (page: number, pageSize: number) => void;
  toolbar?: DataTableToolbar<TData>;
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
  tableLayout?: "auto" | "fixed";
  expandedRowWidth?: "table" | "viewport";
  showFooter?: boolean;
}

function ownsRowInteraction(target: EventTarget | null) {
  return target instanceof Element && Boolean(target.closest(
    "button,a,input,textarea,select,[role=button],[role=menuitem],[data-row-interaction]",
  ));
}

function shouldToggleExpandedRow(event: React.MouseEvent) {
  if (ownsRowInteraction(event.target)) return false;
  const selection = window.getSelection();
  return !selection || selection.isCollapsed;
}

function shouldToggleExpandedRowFromKeyboard(event: React.KeyboardEvent) {
  return !ownsRowInteraction(event.target) && (event.key === "Enter" || event.key === " ");
}

export function DataTable<TData, TValue>({
  columns,
  data,
  loading = false,
  total,
  page = 1,
  pageSize = 10,
  pageCount = 1,
  paginationDisabled = false,
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
  tableLayout = "auto",
  expandedRowWidth = "table",
  showFooter = true,
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

  if (process.env.NODE_ENV !== "production" && renderExpandedRow && !getRowId) {
    // 展开态若按数组下标存,数据 reorder 后会展开到别的记录(logs 表曾踩坑)
    console.warn(
      "DataTable: renderExpandedRow provided without getRowId — expanded rows will misalign when data reorders. Pass getRowId with a stable record id.",
    );
  }

  const table = useDataTableInstance({
    data,
    columns,
    state: { sorting, columnVisibility, expanded, ...(onRowSelectionChange ? { rowSelection: rowSelection ?? {} } : {}) },
    onSortingChange: setSorting,
    onColumnVisibilityChange: handleColumnVisibilityChange,
    onExpandedChange: handleExpandedChange,
    enableRowSelection: !!onRowSelectionChange,
    onRowSelectionChange: onRowSelectionChange
      ? (updater: Updater<RowSelectionState>) => {
          const next = typeof updater === "function" ? updater(rowSelection ?? {}) : updater;
          onRowSelectionChange(next);
        }
      : undefined,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getExpandedRowModel: getExpandedRowModel(),
    ...(getRowId ? { getRowId } : {}),
  });
  const usesTableToolbar = typeof toolbar === "function";

  return (
    <TooltipProvider delayDuration={200}>
      <div className="min-w-0 space-y-4">
        {usesTableToolbar ? toolbar(table) : toolbar}
        {defaultColumnVisibility && !usesTableToolbar && (
          <div className="flex justify-end">
            <ColumnVisibility table={table} />
          </div>
        )}
        <div className="rounded-md border overflow-x-auto">
          <Table
            className={cn(tableLayout === "fixed" && "table-fixed")}
            containerProps={{
              "data-expanded-row-width": expandedRowWidth,
              className: cn(expandedRowWidth === "viewport" && "@container/data-table"),
            }}
          >
            {tableLayout === "fixed" ? (
              <colgroup>
                {table.getVisibleLeafColumns().map((column) => (
                  <col
                    key={column.id}
                    data-column-id={column.id}
                    style={column.columnDef.size ? { width: column.columnDef.size } : undefined}
                  />
                ))}
              </colgroup>
            ) : null}
            <TableHeader>
              {table.getHeaderGroups().map((headerGroup) => (
                <TableRow key={headerGroup.id}>
                  {headerGroup.headers.map((header) => (
                    <TableHead
                      key={header.id}
                      className={cn(
                        "whitespace-nowrap",
                        (header.column.columnDef.meta as { headerClassName?: string } | undefined)?.headerClassName,
                      )}
                    >
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
                  const toggleExpandedRow = () => row.toggleExpanded();
                  return (
                    <Fragment key={row.id}>
                      <TableRow
                        className={cn(
                          renderExpandedRow && "cursor-pointer hover:bg-muted/30",
                          renderExpandedRow && isExpanded && "bg-muted/40",
                        )}
                        data-state={isExpanded ? "expanded" : undefined}
                        tabIndex={renderExpandedRow ? 0 : undefined}
                        aria-expanded={renderExpandedRow ? isExpanded : undefined}
                        onClick={renderExpandedRow ? (event) => {
                          if (shouldToggleExpandedRow(event)) toggleExpandedRow();
                        } : undefined}
                        onKeyDown={renderExpandedRow ? (event) => {
                          if (!shouldToggleExpandedRowFromKeyboard(event)) return;
                          if (event.key === " ") event.preventDefault();
                          toggleExpandedRow();
                        } : undefined}
                      >
                        {row.getVisibleCells().map((cell) => (
                          <TableCell
                            key={cell.id}
                            className={cn(
                              "whitespace-nowrap",
                              (cell.column.columnDef.meta as { cellClassName?: string } | undefined)?.cellClassName,
                            )}
                          >
                            {flexRender(
                              cell.column.columnDef.cell,
                              cell.getContext()
                            )}
                          </TableCell>
                        ))}
                      </TableRow>
                      {isExpanded && renderExpandedRow && (
                        <TableRow>
                          <TableCell
                            colSpan={row.getVisibleCells().length}
                            className={cn("bg-muted/50", expandedRowWidth === "table" ? "p-4" : "p-0")}
                          >
                            {expandedRowWidth === "viewport" ? (
                              <div
                                data-slot="data-table-expanded-content"
                                className="sticky left-0 w-[100cqw] p-4"
                              >
                                {renderExpandedRow(row)}
                              </div>
                            ) : renderExpandedRow(row)}
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
        {showFooter ? <div className="flex items-center justify-between">
          <div className="text-sm text-muted-foreground">
            {total !== undefined && t("total", { count: total })}
          </div>
          {onPaginationChange && (
            <DataTablePagination
              page={page}
              pageSize={pageSize}
              pageCount={pageCount}
              disabled={paginationDisabled}
              onPaginationChange={onPaginationChange}
            />
          )}
        </div> : null}
      </div>
    </TooltipProvider>
  );
}
