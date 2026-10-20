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
  warn: number;     // 0-100
  critical: number; // 0-100
  semantic?: "high-bad" | "low-bad";
}

interface MetricTileProps {
  label: string;
  value: string | number | ReactNode;
  valueClassName?: string;
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
 * 默认高阈值告警；low-bad 表示剩余量降低时告警，充足时显示绿色。
 */
function thresholdColor(
  percent: number | undefined,
  t: MetricTileThreshold | undefined,
): string {
  if (!t || percent === undefined || Number.isNaN(percent)) return "";
  if (t.semantic === "low-bad") {
    if (percent <= t.critical) return "text-destructive";
    if (percent <= t.warn) return "text-amber-600 dark:text-amber-400";
    return "text-emerald-600 dark:text-emerald-400";
  }
  if (percent >= t.critical) return "text-destructive";
  if (percent >= t.warn) return "text-amber-600 dark:text-amber-400";
  return "";
}

export function MetricTile({
  label,
  value,
  valueClassName,
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
          "px-3 font-mono tabular-nums",
          valueClassName ?? "text-display",
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
          <div
            role="progressbar"
            aria-label={label}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={Math.max(0, Math.min(100, viz.percent))}
            className="h-1.5 w-full overflow-hidden rounded-full bg-muted"
          >
            <div
              className={cn("h-full rounded-full bg-current", valueColor || "text-primary")}
              style={{ width: `${Math.max(0, Math.min(100, viz.percent))}%` }}
            />
          </div>
        )}
      </div>
    </Card>
  );
}
