"use client";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export interface ScrollableChartLegendItem {
  key: string;
  label: React.ReactNode;
  color: string;
  hidden?: boolean;
}

interface ScrollableChartLegendProps {
  items: readonly ScrollableChartLegendItem[];
  onToggle: (key: string) => void;
  ariaLabel?: string;
  className?: string;
}

interface ChartLegendShellProps {
  children?: React.ReactNode;
  placeholder?: boolean;
  className?: string;
}

export function ChartLegendShell({ children, placeholder = false, className }: ChartLegendShellProps) {
  return (
    <div
      data-slot="chart-legend-shell"
      aria-hidden={placeholder || undefined}
      className={cn("h-10 min-w-0 overflow-hidden", className)}
    >
      {children}
    </div>
  );
}

export function ScrollableChartLegend({
  items,
  onToggle,
  ariaLabel,
  className,
}: ScrollableChartLegendProps) {
  if (!items.length) return null;

  return (
    <ChartLegendShell>
      <div
        role="region"
        aria-label={ariaLabel}
        className={cn("h-full max-w-full min-w-0 overflow-x-auto overscroll-contain px-px pb-1", className)}
      >
        <ul className="flex h-6 w-max min-w-full flex-nowrap items-center gap-1.5" role="list">
          {items.map((item) => (
            <li key={item.key} className="shrink-0">
              <Button
                type="button"
                variant="ghost"
                size="xs"
                onClick={() => onToggle(item.key)}
                aria-pressed={!item.hidden}
                className="max-w-[15rem] shrink-0 gap-1.5 px-2 text-muted-foreground focus-visible:ring-inset motion-reduce:transition-none"
              >
                <span
                  aria-hidden="true"
                  className="h-2.5 w-2.5 shrink-0 rounded-[2px]"
                  style={{ backgroundColor: item.color, opacity: item.hidden ? 0.3 : 1 }}
                />
                <span
                  className={cn(
                    "min-w-0 truncate break-words text-start",
                    item.hidden && "text-muted-foreground/50 line-through",
                  )}
                  title={typeof item.label === "string" ? item.label : undefined}
                >
                  {item.label}
                </span>
              </Button>
            </li>
          ))}
        </ul>
      </div>
    </ChartLegendShell>
  );
}
