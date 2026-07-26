"use client";

import { useMemo } from "react";
import { useTranslations } from "next-intl";
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts";

import { ChartCard } from "@/components/business/chart-card";
import { BoundedChartTooltip } from "@/components/business/bounded-chart-tooltip";
import { ResponsiveChartFrame } from "@/components/business/responsive-chart-frame";
import { ScrollableChartLegend } from "@/components/business/scrollable-chart-legend";
import {
  ChartContainer,
  ChartTooltip,
  type ChartConfig,
} from "@/components/ui/chart";
import { ChartOptionSelect } from "@/components/business/chart-option-select";
import {
  useHiddenSeries,
} from "@/components/business/toggleable-chart-legend";
import { chartColorForSeries } from "@/lib/chart-colors";
import type { MarketShareBucket, MarketShareDim } from "@/lib/api/dashboard";
import {
  formatTokensCompact,
  formatTokensExact,
} from "@/lib/utils/format";

export type MarketShareMode = "percent" | "absolute";

/** 后端 top-N 折叠桶的字面量 key(见 internal/dao/stats.go 附近 assembleCostStacked) */
const OTHERS_KEY = "others";
/** "others" 固定中性灰，避免与真实系列的稳定色撞色。 */
const OTHERS_COLOR = "var(--muted-foreground)";

interface MarketShareChartProps {
  buckets: MarketShareBucket[];
  seriesOrder: string[];
  mode: MarketShareMode;
  onModeChange: (mode: MarketShareMode) => void;
  dim: MarketShareDim;
  onDimChange: (dim: MarketShareDim) => void;
  title?: string;
  loading?: boolean;
  empty?: boolean;
  error?: React.ReactNode;
}

/** 把 bucket.series 摊平成 recharts 行；percent 模式把每个 bucket 内的 series 值归一到 0-100 */
function toRows(
  buckets: MarketShareBucket[],
  seriesOrder: string[],
  mode: MarketShareMode,
): Array<Record<string, string | number>> {
  return buckets.map((b) => {
    const row: Record<string, string | number> = { label: b.label, ts: b.ts };
    if (mode === "percent") {
      const sum = seriesOrder.reduce((acc, key) => acc + (b.series[key] ?? 0), 0);
      for (const key of seriesOrder) {
        row[key] = sum > 0 ? ((b.series[key] ?? 0) / sum) * 100 : 0;
      }
    } else {
      for (const key of seriesOrder) {
        row[key] = b.series[key] ?? 0;
      }
    }
    return row;
  });
}

export function MarketShareChart({
  buckets,
  seriesOrder,
  mode,
  onModeChange,
  dim,
  onDimChange,
  title,
  loading,
  empty,
  error,
}: MarketShareChartProps) {
  const t = useTranslations("dashboard");
  const tcf = useTranslations("charts");

  const { hidden, toggle } = useHiddenSeries(seriesOrder);

  const config = useMemo<ChartConfig>(() => {
    const cfg: ChartConfig = {};
    // others 单独固定灰色，真实系列颜色按名称稳定映射。
    seriesOrder.forEach((key) => {
      if (key === OTHERS_KEY) {
        cfg[key] = { label: t("marketShare.others"), color: OTHERS_COLOR };
      } else {
        cfg[key] = { label: key, color: chartColorForSeries(key) };
      }
    });
    return cfg;
  }, [seriesOrder, t]);

  const data = useMemo(
    () => toRows(buckets, seriesOrder, mode),
    [buckets, seriesOrder, mode],
  );

  const isEmpty = empty ?? buckets.length === 0;

  const action = (
    <div className="flex flex-wrap items-center gap-2">
      <ChartOptionSelect
        value={dim}
        onValueChange={onDimChange}
        label={tcf("prefix.dim")}
        options={[
          { value: "model", label: t("marketShare.dimModel") },
          { value: "channel", label: t("marketShare.dimChannel") },
        ]}
      />
      <ChartOptionSelect
        value={mode}
        onValueChange={onModeChange}
        label={tcf("prefix.mode")}
        options={[
          { value: "percent", label: t("marketShare.percent") },
          { value: "absolute", label: t("marketShare.absolute") },
        ]}
      />
    </div>
  );

  return (
    <ChartCard
      title={title ?? t("marketShare.title")}
      action={action}
      loading={loading}
      empty={isEmpty}
      error={error}
    >
      <ResponsiveChartFrame
        legend={
          <ScrollableChartLegend
            ariaLabel={tcf("legend.series")}
            items={seriesOrder.map((key) => ({
              key,
              label: config[key]?.label ?? key,
              color: config[key]?.color ?? chartColorForSeries(key),
              hidden: hidden.has(key),
            }))}
            onToggle={toggle}
          />
        }
      >
      <ChartContainer config={config} className="h-full w-full">
        <BarChart
          data={data}
          accessibilityLayer
          maxBarSize={48}
          barCategoryGap="20%"
        >
          <CartesianGrid vertical={false} />
          <XAxis dataKey="label" tickLine={false} axisLine={false} />
          <YAxis
            tickLine={false}
            axisLine={false}
            domain={mode === "percent" ? [0, 100] : undefined}
            tickFormatter={mode === "percent"
              ? (v: number) => `${Math.round(v)}%`
              : formatTokensCompact}
          />
          <ChartTooltip
            content={
              <BoundedChartTooltip
                formatter={(value, name) => (
                  <div className="flex w-full items-center justify-between gap-3">
                    <span
                      className="max-w-[10rem] truncate text-muted-foreground"
                      title={String(name)}
                    >
                      {String(name)}
                    </span>
                    <span className="font-mono tabular-nums">
                      {mode === "percent"
                        ? `${Number(value).toFixed(1)}%`
                        : formatTokensExact(Number(value))}
                    </span>
                  </div>
                )}
              />
            }
          />
          {seriesOrder.map((key, i) => (
            <Bar
              key={key}
              dataKey={key}
              stackId="a"
              hide={hidden.has(key)}
              fill={config[key]?.color ?? chartColorForSeries(`${key}-${i}`)}
              radius={0}
            />
          ))}
        </BarChart>
      </ChartContainer>
      </ResponsiveChartFrame>
    </ChartCard>
  );
}
