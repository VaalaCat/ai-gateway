"use client";

import type { ComponentProps } from "react";

import { ChartTooltipContent } from "@/components/ui/chart";
import { cn } from "@/lib/utils";

type BoundedChartTooltipProps = ComponentProps<typeof ChartTooltipContent>;

function sortableTooltipValue(value: unknown): number {
  if (typeof value === "number") return Number.isFinite(value) ? value : Number.NEGATIVE_INFINITY;
  if (typeof value !== "string" || value.trim() === "") return Number.NEGATIVE_INFINITY;
  const numericValue = Number(value);
  return Number.isFinite(numericValue) ? numericValue : Number.NEGATIVE_INFINITY;
}

export function BoundedChartTooltip({
  active,
  payload,
  className,
  formatter,
  ...props
}: BoundedChartTooltipProps) {
  if (!active || !payload?.length) return null;
  const sortedPayload = payload
    .map((item, index) => ({ item, index }))
    .sort((a, b) => {
      const aValue = sortableTooltipValue(a.item.value);
      const bValue = sortableTooltipValue(b.item.value);
      return bValue - aValue || a.index - b.index;
    })
    .map(({ item }) => item);
  const formatterWithIndicator: typeof formatter = formatter
    ? (value, name, item, index, itemPayload) => (
        <>
          <span
            data-testid="tooltip-color-indicator"
            aria-hidden="true"
            className="size-2.5 shrink-0 rounded-[2px]"
            style={{ backgroundColor: item.payload?.fill ?? item.color ?? "var(--muted-foreground)" }}
          />
          <span className="min-w-0 flex-1">{formatter(value, name, item, index, itemPayload)}</span>
        </>
      )
    : undefined;

  return (
    <div
      role="tooltip"
      tabIndex={0}
      className="pointer-events-auto max-h-[min(40vh,7rem)] min-w-[8rem] max-w-[min(12rem,calc(100vw-4rem),calc(100cqw-4rem))] touch-pan-y overflow-y-auto overscroll-contain rounded-lg border border-border/50 bg-background px-2.5 py-1.5 text-xs shadow-md sm:max-h-[min(50vh,9rem)] sm:max-w-[min(20rem,calc(100vw-2rem),calc(100cqw-5rem))]"
    >
      <ChartTooltipContent
        {...props}
        active={active}
        payload={sortedPayload}
        formatter={formatterWithIndicator}
        className={cn("min-w-0 border-0 bg-transparent p-0 shadow-none", className)}
      />
    </div>
  );
}
