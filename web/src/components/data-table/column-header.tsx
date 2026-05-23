"use client";

import { Column } from "@tanstack/react-table";
import { ArrowDown, ArrowUp, ArrowUpDown } from "lucide-react";

import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";

interface DataTableColumnHeaderProps<TData, TValue>
  extends React.HTMLAttributes<HTMLDivElement> {
  column: Column<TData, TValue>;
  title: string;
}

export function DataTableColumnHeader<TData, TValue>({
  column,
  title,
  className,
}: DataTableColumnHeaderProps<TData, TValue>) {
  if (!column.getCanSort()) {
    return <div className={cn("text-label", className)}>{title}</div>;
  }

  return (
    <div className={cn("flex items-center space-x-2", className)}>
      <Button
        variant="ghost"
        size="sm"
        className="-ml-3 h-8 data-[state=open]:bg-accent text-label"
        onClick={() => {
          const currentSort = column.getIsSorted();
          if (currentSort === false) {
            column.toggleSorting(false);
          } else if (currentSort === "asc") {
            column.toggleSorting(true);
          } else {
            column.clearSorting();
          }
        }}
      >
        <span>{title}</span>
        {column.getIsSorted() === "desc" ? (
          <ArrowDown className="size-4" />
        ) : column.getIsSorted() === "asc" ? (
          <ArrowUp className="size-4" />
        ) : (
          <ArrowUpDown className="size-4" />
        )}
      </Button>
    </div>
  );
}
