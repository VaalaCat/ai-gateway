"use client";

import * as React from "react";

import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";

export interface LeaderColumn<T> {
  key: keyof T;
  label: string;
  render?: (row: T) => React.ReactNode;
  className?: string;
}

export interface LeaderboardSortOption {
  value: string;
  label: string;
}

export interface LeaderboardProps<T> {
  title: string;
  rows: T[];
  columns: LeaderColumn<T>[];
  sortBy?: string;
  onSortChange?: (by: string) => void;
  sortOptions?: LeaderboardSortOption[];
  onRowClick?: (row: T) => void;
  emptyText?: string;
  className?: string;
}

function renderCell<T>(row: T, col: LeaderColumn<T>): React.ReactNode {
  if (col.render) return col.render(row);
  const v = row[col.key];
  if (v == null) return "";
  if (typeof v === "string" || typeof v === "number") return v;
  return String(v);
}

export function Leaderboard<T>({
  title,
  rows,
  columns,
  sortBy,
  onSortChange,
  sortOptions,
  onRowClick,
  emptyText,
  className,
}: LeaderboardProps<T>) {
  const showSort =
    sortOptions && sortOptions.length > 0 && Boolean(onSortChange);

  return (
    <Card className={className}>
      <CardHeader className="flex flex-row items-center justify-between gap-2 space-y-0">
        <CardTitle>{title}</CardTitle>
        {showSort && (
          <Select value={sortBy} onValueChange={onSortChange}>
            <SelectTrigger size="sm" className="w-[160px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {sortOptions?.map((opt) => (
                <SelectItem key={opt.value} value={opt.value}>
                  {opt.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
      </CardHeader>
      <CardContent>
        {rows.length === 0 ? (
          <p className="text-muted-foreground">
            {emptyText ?? "No data"}
          </p>
        ) : (
          <Table className="text-body">
            <TableHeader>
              <TableRow>
                {columns.map((col) => (
                  <TableHead
                    key={String(col.key)}
                    className={col.className}
                  >
                    {col.label}
                  </TableHead>
                ))}
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row, i) => (
                <TableRow
                  key={i}
                  onClick={onRowClick ? () => onRowClick(row) : undefined}
                  className={cn(onRowClick && "cursor-pointer")}
                >
                  {columns.map((col) => (
                    <TableCell
                      key={String(col.key)}
                      className={col.className}
                    >
                      {renderCell(row, col)}
                    </TableCell>
                  ))}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}
