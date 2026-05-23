"use client";

import { useEffect, useMemo, useState } from "react";

import { Card } from "@/components/ui/card";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { Skeleton } from "@/components/ui/skeleton";
import { useBreakpoint } from "@/lib/hooks/use-breakpoint";
import { cn } from "@/lib/utils";

export interface StageBucket {
  stage: string;
  count: number;
  /** 显示用,默认 = stage(英文枚举);消费方传 i18n 后的中文标签 */
  label?: string;
}

interface StageDistributionBarProps {
  data: StageBucket[];
  title: string;
  /**
   * 流程顺序;data 按这个顺序重排,不在 order 里的 stage 追加在末尾.
   * 不传则按 data 原顺序(后端 count DESC,失去"流程从左到右"语义).
   */
  order?: readonly string[];
  loading?: boolean;
  emptyText?: string;
  className?: string;
}

/**
 * "按阶段分布" 卡片;按视口切两种排版,信息保留一致.
 *
 *   sm+ (≥640): horizontal stacked bar
 *     段宽 ∝ count / total,左→右流程顺序,单色 destructive + 白细 divider;
 *     下方 N 等宽 grid 排 label.~90px 紧凑.
 *
 *   xs (<640): vertical list
 *     每 stage 一行 "label | bar | count";bar 长度 ∝ count / max(count),
 *     按 order 流程顺序排;label 中文完整不 truncate.~200-220px 自适应.
 *
 * 入场:bar 宽度 0→target stagger 60ms ease-out.
 */
export function StageDistributionBar({
  data,
  title,
  order,
  loading,
  emptyText = "—",
  className,
}: StageDistributionBarProps) {
  const bp = useBreakpoint();
  const ordered = useMemo(() => {
    if (!order || order.length === 0) return data;
    const byStage = new Map(data.map((d) => [d.stage, d]));
    const head = order
      .map((s) => byStage.get(s))
      .filter((d): d is StageBucket => d != null);
    const known = new Set(order);
    const tail = data.filter((d) => !known.has(d.stage));
    return [...head, ...tail];
  }, [data, order]);
  const total = ordered.reduce((acc, d) => acc + d.count, 0);
  const maxCount = ordered.reduce((acc, d) => Math.max(acc, d.count), 0);
  const [mounted, setMounted] = useState(false);
  useEffect(() => {
    const id = requestAnimationFrame(() => setMounted(true));
    return () => cancelAnimationFrame(id);
  }, []);

  const titleRow = (
    <div className="mb-3 flex items-baseline justify-between">
      <span className="text-xs uppercase tracking-wider text-muted-foreground">
        {title}
      </span>
      <span
        className={cn(
          "text-2xl font-bold tabular-nums",
          total > 0 ? "text-destructive" : "text-muted-foreground",
        )}
      >
        {total > 0 ? total : emptyText}
      </span>
    </div>
  );

  if (loading) {
    return (
      <Card className={cn("p-3 sm:p-4", className)}>
        <div className="mb-3 flex items-baseline justify-between">
          <span className="text-xs uppercase tracking-wider text-muted-foreground">
            {title}
          </span>
          <Skeleton className="h-7 w-10" />
        </div>
        <Skeleton className="h-6 w-full rounded-sm" />
        <Skeleton className="mt-1.5 h-3 w-3/4" />
      </Card>
    );
  }

  if (total === 0) {
    return <Card className={cn("p-3 sm:p-4", className)}>{titleRow}</Card>;
  }

  return (
    <Card className={cn("p-3 sm:p-4", className)}>
      {titleRow}
      {bp === "xs" ? (
        <VerticalList
          data={ordered}
          maxCount={maxCount}
          total={total}
          mounted={mounted}
        />
      ) : (
        <HorizontalStackedBar
          data={ordered}
          total={total}
          mounted={mounted}
        />
      )}
    </Card>
  );
}

interface RenderProps {
  data: StageBucket[];
  total: number;
  mounted: boolean;
}

function HorizontalStackedBar({ data, total, mounted }: RenderProps) {
  return (
    <TooltipProvider delayDuration={100}>
      <div className="flex h-6 w-full overflow-hidden rounded-sm bg-muted">
        {data.map((d, i) => {
          const pct = (d.count / total) * 100;
          return (
            <Tooltip key={`${d.stage}-${i}`}>
              <TooltipTrigger asChild>
                <div
                  className={cn(
                    "h-full cursor-help bg-destructive transition-[width] duration-700 ease-out hover:brightness-110",
                    i > 0 && "border-l border-background",
                  )}
                  style={{
                    width: mounted ? `${pct}%` : "0%",
                    transitionDelay: `${i * 60}ms`,
                  }}
                />
              </TooltipTrigger>
              <TooltipContent>
                <span className="font-medium">{d.label ?? d.stage}</span>:{" "}
                <span className="tabular-nums">{d.count}</span> (
                {pct.toFixed(1)}%)
              </TooltipContent>
            </Tooltip>
          );
        })}
      </div>
      <div
        className="mt-1.5 grid gap-1 text-2xs uppercase tracking-wider text-muted-foreground"
        style={{
          gridTemplateColumns: `repeat(${data.length}, minmax(0, 1fr))`,
        }}
      >
        {data.map((d, i) => (
          <span
            key={`${d.stage}-label-${i}`}
            className="truncate text-center"
            title={d.label ?? d.stage}
          >
            {d.label ?? d.stage}
          </span>
        ))}
      </div>
    </TooltipProvider>
  );
}

function VerticalList({
  data,
  maxCount,
  total,
  mounted,
}: RenderProps & { maxCount: number }) {
  return (
    <div className="space-y-1.5">
      {data.map((d, i) => {
        const pctOfMax = maxCount > 0 ? (d.count / maxCount) * 100 : 0;
        const pctOfTotal = total > 0 ? (d.count / total) * 100 : 0;
        return (
          <div
            key={`${d.stage}-row-${i}`}
            className="flex items-center gap-2"
            title={`${d.label ?? d.stage}: ${d.count} (${pctOfTotal.toFixed(1)}%)`}
          >
            <span className="w-16 shrink-0 truncate text-xs text-muted-foreground">
              {d.label ?? d.stage}
            </span>
            <div className="relative h-3 flex-1 overflow-hidden rounded-sm bg-muted">
              <div
                className="absolute inset-y-0 left-0 bg-destructive transition-[width] duration-700 ease-out"
                style={{
                  width: mounted ? `${pctOfMax}%` : "0%",
                  transitionDelay: `${i * 60}ms`,
                }}
              />
            </div>
            <span className="w-6 shrink-0 text-right text-xs tabular-nums text-muted-foreground">
              {d.count}
            </span>
          </div>
        );
      })}
    </div>
  );
}
