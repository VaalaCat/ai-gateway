"use client";

import { useMemo } from "react";
import { useTranslations } from "next-intl";
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";

import { ChartCard } from "@/components/business/chart-card";
import { BoundedChartTooltip } from "@/components/business/bounded-chart-tooltip";
import { ResponsiveChartFrame } from "@/components/business/responsive-chart-frame";
import { ScrollableChartLegend } from "@/components/business/scrollable-chart-legend";
import {
  ChartContainer,
  ChartTooltip,
  type ChartConfig,
} from "@/components/ui/chart";
import {
  useHiddenSeries,
} from "@/components/business/toggleable-chart-legend";
import { chartColorForSeries } from "@/lib/chart-colors";
import type { StackedBucket } from "@/lib/types/observability";

export type { StackedBucket };

/** 后端 top-N 折叠桶的字面量 key(与 market-share-chart/metric-trend-chart 的 OTHERS_KEY 同源约定) */
const OTHERS_KEY = "others";
/** "others" 固定中性灰，避免与真实系列的稳定色撞色。 */
const OTHERS_COLOR = "var(--muted-foreground)";

interface StackedAreaBodyProps {
  buckets: StackedBucket[];
  seriesOrder: string[];
  axisFormatter?: (v: number) => string;
  tooltipFormatter?: (v: number) => string;
  /** "others" 折叠系列的本地化展示名;不传则回退到原始 key(英文字面量) */
  othersLabel?: string;
  legendLabel?: string;
  allHiddenLabel?: string;
}

interface StackedAreaChartProps extends StackedAreaBodyProps {
  title: string;
  loading?: boolean;
  empty?: boolean;
  className?: string;
  unitLabel?: string;
}

/** 仅渲染堆叠面积图体(不含 ChartCard),供需要自定义 header/action 的容器复用。 */
export function StackedAreaBody({
  buckets,
  seriesOrder,
  axisFormatter,
  tooltipFormatter,
  othersLabel,
  legendLabel,
  allHiddenLabel,
}: StackedAreaBodyProps) {
  const { hidden, toggle } = useHiddenSeries(seriesOrder);

  const config = useMemo<ChartConfig>(() => {
    const cfg: ChartConfig = {};
    // "others" 固定中性灰，真实系列颜色按名称稳定映射。
    // (同 market-share-chart / metric-trend-chart 的 LinesBody 处理)。
    seriesOrder.forEach((key) => {
      cfg[key] = {
        label: key === OTHERS_KEY ? othersLabel ?? key : key,
        color: key === OTHERS_KEY ? OTHERS_COLOR : chartColorForSeries(key),
      };
    });
    return cfg;
  }, [seriesOrder, othersLabel]);

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

  const tipFmt = tooltipFormatter ?? axisFormatter;
  const allHidden = seriesOrder.length > 0 && seriesOrder.every((k) => hidden.has(k));

  return (
    <ResponsiveChartFrame
      legend={
        <div className="space-y-1">
          <ScrollableChartLegend
            ariaLabel={legendLabel ?? ""}
            items={seriesOrder.map((key) => ({
              key,
              label: config[key]?.label ?? key,
              color: config[key]?.color ?? chartColorForSeries(key),
              hidden: hidden.has(key),
            }))}
            onToggle={toggle}
          />
          {allHidden && allHiddenLabel && <p className="text-center text-xs text-muted-foreground">{allHiddenLabel}</p>}
        </div>
      }
    >
      <ChartContainer config={config} className="h-full w-full">
        <AreaChart data={data} accessibilityLayer>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="label" tickLine={false} axisLine={false} />
          <YAxis tickLine={false} axisLine={false} tickFormatter={axisFormatter} />
          <ChartTooltip
            content={
              <BoundedChartTooltip
                formatter={
                  tipFmt
                    ? (value, name) => {
                        const label = config[String(name)]?.label ?? String(name);
                        return (
                          <div className="flex w-full items-center justify-between gap-3">
                            <span
                              className="max-w-[10rem] truncate text-muted-foreground"
                              title={String(label)}
                            >
                              {String(label)}
                            </span>
                            <span className="font-mono tabular-nums">
                              {tipFmt(Number(value))}
                            </span>
                          </div>
                        );
                      }
                    : undefined
                }
              />
            }
          />
          {seriesOrder.map((key, i) => (
            <Area
              key={key}
              dataKey={key}
              stackId="a"
              type="monotone"
              stroke={config[key]?.color ?? chartColorForSeries(`${key}-${i}`)}
              fill={config[key]?.color ?? chartColorForSeries(`${key}-${i}`)}
              fillOpacity={0.6}
              hide={hidden.has(key)}
            />
          ))}
        </AreaChart>
      </ChartContainer>
    </ResponsiveChartFrame>
  );
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
  othersLabel,
}: StackedAreaChartProps) {
  const t = useTranslations("charts.legend");
  const isEmpty = empty ?? buckets.length === 0;
  return (
    <ChartCard title={title} sub={unitLabel} loading={loading} empty={isEmpty} className={className}>
      <StackedAreaBody
        buckets={buckets}
        seriesOrder={seriesOrder}
        axisFormatter={axisFormatter}
        tooltipFormatter={tooltipFormatter}
        othersLabel={othersLabel}
        legendLabel={t("series")}
        allHiddenLabel={t("allHidden")}
      />
    </ChartCard>
  );
}
