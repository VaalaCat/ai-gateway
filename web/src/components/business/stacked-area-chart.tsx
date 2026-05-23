"use client";

import { useCallback, useMemo, useState } from "react";
import {
  Area,
  AreaChart,
  CartesianGrid,
  XAxis,
  YAxis,
} from "recharts";

import { ChartCard } from "@/components/business/chart-card";
import {
  ChartContainer,
  ChartLegend,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import { cn } from "@/lib/utils";
import type { StackedBucket } from "@/lib/types/observability";

export type { StackedBucket };

interface StackedAreaChartProps {
  buckets: StackedBucket[];
  seriesOrder: string[];
  title: string;
  loading?: boolean;
  empty?: boolean;
  className?: string;
  /** Y 轴 tick + legend 用紧凑值 */
  axisFormatter?: (v: number) => string;
  /** tooltip 内值用精确值。默认 = axisFormatter */
  tooltipFormatter?: (v: number) => string;
  /** ChartCard 副标题, 通常显示单位如 "Cost (USD)" */
  unitLabel?: string;
}

const CHART_COLORS = [
  "var(--chart-1)",
  "var(--chart-2)",
  "var(--chart-3)",
  "var(--chart-4)",
  "var(--chart-5)",
];

function useToggleSet<K extends string>() {
  const [hidden, setHidden] = useState<Set<K>>(new Set());
  const toggle = useCallback((k: K) => {
    setHidden((prev) => {
      const next = new Set(prev);
      if (next.has(k)) next.delete(k);
      else next.add(k);
      return next;
    });
  }, []);
  const isHidden = useCallback((k: K) => hidden.has(k), [hidden]);
  return { isHidden, toggle };
}

export function StackedAreaChart({
  buckets,
  seriesOrder,
  title,
  loading,
  empty,
  className,
  axisFormatter,
  tooltipFormatter,
  unitLabel,
}: StackedAreaChartProps) {
  const series = useToggleSet<string>();

  const config = useMemo<ChartConfig>(() => {
    const cfg: ChartConfig = {};
    seriesOrder.forEach((key, i) => {
      cfg[key] = {
        label: key,
        color: CHART_COLORS[i % CHART_COLORS.length],
      };
    });
    return cfg;
  }, [seriesOrder]);

  // 补 0: 后端某 bucket 缺某 model series 时, recharts 会把该点视为 NaN/undefined
  // 导致连线断开。这里对每个 bucket × 每个 series 都填 0 占位, 保证图连续。
  const data = useMemo(
    () =>
      buckets.map((b) => {
        const row: Record<string, string | number> = { label: b.label, ts: b.ts };
        for (const key of seriesOrder) {
          row[key] = b.series[key] ?? 0;
        }
        return row;
      }),
    [buckets, seriesOrder],
  );

  const isEmpty = empty ?? buckets.length === 0;
  const tipFmt = tooltipFormatter ?? axisFormatter;
  const allHidden = seriesOrder.length > 0 && seriesOrder.every((k) => series.isHidden(k));

  return (
    <ChartCard
      title={title}
      sub={unitLabel}
      loading={loading}
      empty={isEmpty}
      className={className}
    >
      <ChartContainer config={config} className="h-[260px] w-full">
        <AreaChart data={data} accessibilityLayer>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="label" tickLine={false} axisLine={false} />
          <YAxis
            tickLine={false}
            axisLine={false}
            tickFormatter={axisFormatter}
          />
          <ChartTooltip
            content={
              <ChartTooltipContent
                formatter={
                  tipFmt
                    ? (value, name) => (
                        <div className="flex w-full items-center justify-between gap-3">
                          <span
                            className="max-w-[10rem] truncate text-muted-foreground"
                            title={String(name)}
                          >
                            {String(name)}
                          </span>
                          <span className="font-mono tabular-nums">
                            {tipFmt(Number(value))}
                          </span>
                        </div>
                      )
                    : undefined
                }
              />
            }
          />
          <ChartLegend
            content={({ payload }) => (
              <ul className="flex flex-wrap items-center justify-center gap-3 pt-3 text-xs">
                {payload?.map((entry) => {
                  const key = String(entry.value);
                  const hidden = series.isHidden(key);
                  return (
                    <li key={key}>
                      <button
                        type="button"
                        onClick={() => series.toggle(key)}
                        className={cn(
                          "flex items-center gap-1.5 cursor-pointer transition-opacity",
                          hidden && "opacity-40 line-through",
                        )}
                      >
                        <span
                          className="inline-block h-2.5 w-2.5 shrink-0 rounded-[2px]"
                          style={{ backgroundColor: entry.color }}
                        />
                        <span
                          className="max-w-[10rem] truncate text-muted-foreground"
                          title={key}
                        >
                          {key}
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
          />
          {seriesOrder.map((key, i) => (
            <Area
              key={key}
              dataKey={key}
              stackId="a"
              type="monotone"
              stroke={CHART_COLORS[i % CHART_COLORS.length]}
              fill={CHART_COLORS[i % CHART_COLORS.length]}
              fillOpacity={0.6}
              hide={series.isHidden(key)}
            />
          ))}
        </AreaChart>
      </ChartContainer>
      {allHidden && (
        <p className="mt-2 text-center text-xs text-muted-foreground">
          all series hidden
        </p>
      )}
    </ChartCard>
  );
}
