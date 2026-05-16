"use client";

import { Table } from "@tanstack/react-table";
import { Settings2 } from "lucide-react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { Checkbox } from "@/components/ui/checkbox";

interface ColumnVisibilityProps<TData> {
  table: Table<TData>;
}

export function ColumnVisibility<TData>({ table }: ColumnVisibilityProps<TData>) {
  const t = useTranslations("common");
  const columns = table.getAllLeafColumns().filter((col) => col.getCanHide());

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="ghost" size="sm" className="h-7 text-xs text-muted-foreground">
          <Settings2 className="mr-1.5 size-3.5" />
          {t("columns")}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-56">
        <div className="space-y-2">
          {columns.map((column) => (
            <label
              key={column.id}
              className="flex items-center gap-2 text-sm cursor-pointer"
            >
              <Checkbox
                checked={column.getIsVisible()}
                onCheckedChange={(v) => column.toggleVisibility(!!v)}
              />
              <span>{typeof column.columnDef.header === "string" ? column.columnDef.header : column.id}</span>
            </label>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  );
}
