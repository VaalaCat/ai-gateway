"use client";

import { useMemo } from "react";
import { Cell, Pie, PieChart } from "recharts";

import { ChartCard } from "@/components/business/chart-card";
import { BoundedChartTooltip } from "@/components/business/bounded-chart-tooltip";
import { Button } from "@/components/ui/button";
import { useHiddenSeries } from "@/components/business/toggleable-chart-legend";
import {
  ChartContainer,
  ChartTooltip,
  type ChartConfig,
} from "@/components/ui/chart";
import { chartColorForSeries } from "@/lib/chart-colors";

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
  error?: React.ReactNode;
  /** 超过 topN 后续 slice 合并为 "others"。默认 5。 */
  topN?: number;
  /** 折叠 slice 的显示名。默认 "others"。 */
  othersLabel?: string;
  legendLabel?: string;
}

interface DisplayDonutSlice extends DonutSlice {
  label: string;
}

const OTHERS_KEY = "others";

function isReservedAggregate(name: string) {
  return name === OTHERS_KEY;
}

export function DonutChart({
  slices,
  title,
  loading,
  empty,
  centerLabel,
  centerSublabel,
  className,
  error,
  topN = 5,
  othersLabel = "others",
  legendLabel = title,
}: DonutChartProps) {
  const displaySlices = useMemo<DisplayDonutSlice[]>(() => {
    const limit = Math.max(0, Math.floor(topN));
    const indexedSlices = slices.map((slice, index) => ({ slice, index }));
    const entities = indexedSlices
      .filter(({ slice }) => !isReservedAggregate(slice.name))
      .sort((a, b) => b.slice.value - a.slice.value || a.index - b.index);
    const head = entities.slice(0, limit).map(({ slice }) => ({ ...slice, label: slice.name }));
    const aggregateValue = indexedSlices
      .filter(({ slice }) => isReservedAggregate(slice.name))
      .reduce((sum, { slice }) => sum + slice.value, 0)
      + entities.slice(limit).reduce((sum, { slice }) => sum + slice.value, 0);
    return aggregateValue > 0
      ? [...head, { name: OTHERS_KEY, label: othersLabel, value: aggregateValue }]
      : head;
  }, [slices, topN, othersLabel]);

  const total = useMemo(
    () => displaySlices.reduce((acc, s) => acc + s.value, 0),
    [displaySlices],
  );
  const { hidden, toggle } = useHiddenSeries(displaySlices.map((slice) => slice.name));

  const config = useMemo<ChartConfig>(() => {
    const cfg: ChartConfig = {};
    displaySlices.forEach((s) => {
      cfg[s.name] = {
        label: s.label,
        color: chartColorForSeries(s.name),
      };
    });
    return cfg;
  }, [displaySlices]);

  const isEmpty = empty ?? displaySlices.length === 0;
  const legend = total > 0 ? (
    <div
      role="region"
      aria-label={legendLabel}
      className="max-h-44 min-w-0 overflow-y-auto overscroll-contain pr-1 lg:max-h-[17.5rem]"
    >
      <ul className="space-y-1" role="list">
        {displaySlices.map((slice) => (
          <li key={slice.name}>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              aria-pressed={!hidden.has(slice.name)}
              onClick={() => toggle(slice.name)}
              className="grid h-auto w-full grid-cols-[auto_minmax(0,1fr)_auto] gap-2 px-2 py-1.5 text-muted-foreground"
            >
              <span className="size-2.5 rounded-[2px]" style={{ backgroundColor: chartColorForSeries(slice.name) }} />
              <span className="truncate text-left" title={slice.label}>{slice.label}</span>
              <span className="tabular-nums text-foreground">{((slice.value / total) * 100).toFixed(1)}%</span>
            </Button>
          </li>
        ))}
      </ul>
    </div>
  ) : undefined;

  return (
    <ChartCard
      title={title}
      loading={loading}
      empty={isEmpty}
      error={error}
      className={className}
      chartFrame={{ minHeight: 280, aspect: "1/1", legendPlacement: "responsive-side", legend }}
    >
        <div className="relative h-full w-full max-w-[240px]">
          <ChartContainer
            config={config}
            className="h-full w-full"
          >
            <PieChart>
              <ChartTooltip content={<BoundedChartTooltip hideLabel />} />
              <Pie
                data={displaySlices}
                dataKey="value"
                nameKey="name"
                innerRadius="55%"
                outerRadius="80%"
                strokeWidth={2}
              >
                {displaySlices.map((s) => (
                  <Cell
                    key={s.name}
                    fill={chartColorForSeries(s.name)}
                    opacity={hidden.has(s.name) ? 0 : 1}
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
    </ChartCard>
  );
}
