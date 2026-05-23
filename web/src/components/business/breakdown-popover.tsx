"use client";

import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";

export interface BreakdownRow {
  label: string;
  value: string;
  muted?: boolean;
  accent?: "info" | "success";
}

export interface BreakdownPopoverProps {
  trigger: string;
  rows: BreakdownRow[];
  total?: { label: string; value: string };
  triggerClassName?: string;
  align?: "start" | "center" | "end";
}

export function BreakdownPopover({
  trigger,
  rows,
  total,
  triggerClassName,
  align = "center",
}: BreakdownPopoverProps) {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className={cn(
            "border-b border-dotted border-muted-foreground/50 cursor-pointer text-left",
            triggerClassName,
          )}
        >
          {trigger}
        </button>
      </PopoverTrigger>
      <PopoverContent align={align} className="w-60 text-xs space-y-1">
        {rows.map((r, i) => (
          <div
            key={i}
            className={cn(
              "flex items-center justify-between gap-4",
              r.muted && "text-muted-foreground",
              r.accent === "info" && "text-blue-500",
              r.accent === "success" && "text-green-500",
            )}
          >
            <span>{r.label}</span>
            <span className="font-mono">{r.value}</span>
          </div>
        ))}
        {total && (
          <div className="flex items-center justify-between gap-4 font-medium border-t pt-1 mt-1">
            <span>{total.label}</span>
            <span className="font-mono">{total.value}</span>
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
