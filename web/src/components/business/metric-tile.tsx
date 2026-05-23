"use client";

import { type ReactNode } from "react";

import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { DataGlyph } from "@/components/business/data-glyph";
import { cn } from "@/lib/utils";

export type MetricTileViz =
  | { kind: "line"; values: number[] }     // values 必须 0-100 归一
  | { kind: "bar"; values: number[] }      // 同上
  | { kind: "ratio"; percent: number }     // 0-100
  | { kind: "progress"; percent: number }  // 0-100
  | { kind: "none" };

export interface MetricTileThreshold {
  warn: number;     // 0-100,高阈警示语义
  critical: number; // 0-100,高阈警示语义
}

interface MetricTileProps {
  label: string;
  value: string | number | ReactNode;
  sublabel?: string;
  viz?: MetricTileViz;
  threshold?: MetricTileThreshold;
  loading?: boolean;
  onClick?: () => void;
  className?: string;
}

function formatValue(v: string | number | ReactNode): ReactNode {
  if (typeof v === "number") return v.toLocaleString();
  if (typeof v === "string") return v;
  return v;
}

/**
 * 高阈警示语义: percent >= critical 红, >= warn 黄。
 * 反向语义(如 success rate)调用方传 100-rate 即可。
 */
function thresholdColor(
  percent: number | undefined,
  t: MetricTileThreshold | undefined,
): string {
  if (!t || percent === undefined || Number.isNaN(percent)) return "";
  if (percent >= t.critical) return "text-destructive";
  if (percent >= t.warn) return "text-amber-600 dark:text-amber-400";
  return "";
}

export function MetricTile({
  label,
  value,
  sublabel,
  viz,
  threshold,
  loading,
  onClick,
  className,
}: MetricTileProps) {
  if (loading) {
    return (
      <Card className={cn("py-3 gap-1", className)}>
        <div className="px-3 text-label text-muted-foreground">{label}</div>
        <div className="px-3"><Skeleton className="h-8 w-20" /></div>
        {sublabel && (
          <div className="px-3"><Skeleton className="h-3 w-16" /></div>
        )}
        <div className="px-3 min-h-[14px]">
          {viz && viz.kind !== "none" && (
            <Skeleton className="h-[14px] w-full" />
          )}
        </div>
      </Card>
    );
  }

  const interactive = Boolean(onClick);
  const checkPercent =
    viz?.kind === "ratio" || viz?.kind === "progress"
      ? viz.percent
      : undefined;
  const valueColor = thresholdColor(checkPercent, threshold);

  return (
    <Card
      onClick={onClick}
      onKeyDown={
        interactive
          ? (e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onClick?.();
              }
            }
          : undefined
      }
      role={interactive ? "button" : undefined}
      tabIndex={interactive ? 0 : undefined}
      className={cn(
        "py-3 gap-1",
        interactive && "cursor-pointer hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        className,
      )}
    >
      <div className="px-3 text-label text-muted-foreground">{label}</div>
      <div
        className={cn(
          "px-3 text-display tabular-nums",
          valueColor,
        )}
      >
        {formatValue(value)}
      </div>
      {sublabel && (
        <div className="px-3 text-meta text-muted-foreground">{sublabel}</div>
      )}
      <div className="px-3 min-h-[14px]">
        {viz?.kind === "line" && (
          <DataGlyph kind="line" values={viz.values} title={label}
            targetByBreakpoint={{ xs: 8, "sm-md": 12, "lg+": 20 }} />
        )}
        {viz?.kind === "bar" && (
          <DataGlyph kind="bar" values={viz.values} title={label}
            targetByBreakpoint={{ xs: 8, "sm-md": 12, "lg+": 20 }} />
        )}
        {viz?.kind === "ratio" && (
          <DataGlyph kind="pie" value={viz.percent} title={label} />
        )}
        {viz?.kind === "progress" && (
          <DataGlyph kind="bar" values={[viz.percent]} title={label} />
        )}
      </div>
    </Card>
  );
}
