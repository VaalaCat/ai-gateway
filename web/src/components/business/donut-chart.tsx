"use client";

import { useMemo } from "react";
import { Cell, Pie, PieChart } from "recharts";

import { ChartCard } from "@/components/business/chart-card";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";

export interface DonutSlice {
  name: string;
  value: number;
  ratio?: number;
}

interface DonutChartProps {
  slices: DonutSlice[];
  title: string;
  loading?: boolean;
  empty?: boolean;
  centerLabel?: string;
  centerSublabel?: string;
  className?: string;
  /** 超过 topN 后续 slice 合并为 "others"。默认 5。 */
  topN?: number;
  /** 折叠 slice 的显示名。默认 "others"。 */
  othersLabel?: string;
}

const CHART_COLORS = [
  "var(--chart-1)",
  "var(--chart-2)",
  "var(--chart-3)",
  "var(--chart-4)",
  "var(--chart-5)",
  "var(--muted-foreground)", // others 用 muted 色, 与 top-N 区分
];

function DonutLegend({
  slices,
  total,
}: {
  slices: DonutSlice[];
  total: number;
}) {
  if (total === 0) return null;
  return (
    <ul className="flex min-w-0 flex-col gap-2">
      {slices.map((s, i) => {
        const pct = (s.value / total) * 100;
        return (
          <li
            key={s.name}
            className="flex min-w-0 items-center gap-2 text-meta"
          >
            <span
              className="size-2.5 shrink-0 rounded-[2px]"
              style={{ backgroundColor: CHART_COLORS[i % CHART_COLORS.length] }}
            />
            <span className="truncate text-muted-foreground" title={s.name}>
              {s.name}
            </span>
            <span className="ml-auto shrink-0 tabular-nums text-foreground">
              {pct.toFixed(1)}%
            </span>
          </li>
        );
      })}
    </ul>
  );
}

export function DonutChart({
  slices,
  title,
  loading,
  empty,
  centerLabel,
  centerSublabel,
  className,
  topN = 5,
  othersLabel = "others",
}: DonutChartProps) {
  // top-N 收敛: 排序后前 N 直接出, 剩余合并为 others
  const displaySlices = useMemo<DonutSlice[]>(() => {
    if (slices.length <= topN) return slices;
    const sorted = [...slices].sort((a, b) => b.value - a.value);
    const head = sorted.slice(0, topN);
    const rest = sorted.slice(topN);
    const sumRest = rest.reduce((acc, s) => acc + s.value, 0);
    return [...head, { name: othersLabel, value: sumRest }];
  }, [slices, topN, othersLabel]);

  const total = useMemo(
    () => displaySlices.reduce((acc, s) => acc + s.value, 0),
    [displaySlices],
  );

  const config = useMemo<ChartConfig>(() => {
    const cfg: ChartConfig = {};
    displaySlices.forEach((s, i) => {
      cfg[s.name] = {
        label: s.name,
        color: CHART_COLORS[i % CHART_COLORS.length],
      };
    });
    return cfg;
  }, [displaySlices]);

  const isEmpty = empty ?? displaySlices.length === 0;

  return (
    <ChartCard
      title={title}
      loading={loading}
      empty={isEmpty}
      className={className}
    >
      <div className="flex flex-col items-center gap-4 lg:grid lg:grid-cols-2 lg:items-center lg:gap-6">
        <div className="relative w-full max-w-[240px]">
          <ChartContainer
            config={config}
            className="aspect-square w-full"
          >
            <PieChart>
              <ChartTooltip content={<ChartTooltipContent hideLabel />} />
              <Pie
                data={displaySlices}
                dataKey="value"
                nameKey="name"
                innerRadius="55%"
                outerRadius="80%"
                strokeWidth={2}
              >
                {displaySlices.map((s, i) => (
                  <Cell
                    key={s.name}
                    fill={CHART_COLORS[i % CHART_COLORS.length]}
                  />
                ))}
              </Pie>
            </PieChart>
          </ChartContainer>
          {(centerLabel || centerSublabel) && (
            <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
              {centerLabel && (
                <div className="text-display">{centerLabel}</div>
              )}
              {centerSublabel && (
                <div className="text-meta text-muted-foreground">
                  {centerSublabel}
                </div>
              )}
            </div>
          )}
        </div>

        <div className="w-full">
          <DonutLegend slices={displaySlices} total={total} />
        </div>
      </div>
    </ChartCard>
  );
}
