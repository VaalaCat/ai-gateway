"use client";

import { Table } from "@tanstack/react-table";
import { Settings2 } from "lucide-react";
import { useTranslations } from "next-intl";

import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";

interface ColumnVisibilityProps<TData> {
  table: Table<TData>;
  mobileTouchSize?: "comfortable";
}

export function ColumnVisibility<TData>({ table, mobileTouchSize }: ColumnVisibilityProps<TData>) {
  const t = useTranslations("common");
  const columns = table.getAllLeafColumns().filter((col) => col.getCanHide());

  return (
    <TooltipProvider>
      <Popover>
        <Tooltip>
          <TooltipTrigger asChild>
            <PopoverTrigger asChild>
              <Button
                variant="ghost"
                size="sm"
                className="size-9 px-0 text-xs text-muted-foreground sm:h-8 sm:w-auto sm:px-2.5"
                aria-label={t("columns")}
              >
                <Settings2 className="size-3.5 sm:mr-1.5" />
                <span className="sr-only sm:not-sr-only">{t("columns")}</span>
              </Button>
            </PopoverTrigger>
          </TooltipTrigger>
          <TooltipContent>{t("columns")}</TooltipContent>
        </Tooltip>
        <PopoverContent align="end" className="w-56">
          <div className="space-y-2">
            {columns.map((column) => (
              <label
                key={column.id}
                className={cn(
                  "flex cursor-pointer items-center gap-2 text-sm",
                  mobileTouchSize === "comfortable" && "max-sm:min-h-11 max-sm:min-w-44",
                )}
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
    </TooltipProvider>
  );
}
