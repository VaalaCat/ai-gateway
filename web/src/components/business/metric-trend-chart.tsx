"use client";

import { useMemo, useState, type JSX, type ReactNode } from "react";
import { useTranslations } from "next-intl";
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts";

import { ChartCard } from "@/components/business/chart-card";
import { StackedAreaBody, CHART_COLORS } from "@/components/business/stacked-area-chart";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  ChartContainer,
  ChartLegend,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import {
  formatMoneyCompact,
  formatMoneyExact,
  formatRequestsCompact,
  formatRequestsExact,
  formatTokensCompact,
  formatTokensExact,
} from "@/lib/utils/format";
import type { StackedBucket, TimeBucket } from "@/lib/types/observability";

export type TrendMetric = "cost" | "requests" | "tokens";
type ChartView = "total" | "stacked" | "lines";

interface CostStacked {
  buckets: StackedBucket[];
  series_order: string[];
}

export interface MetricTrendChartProps {
  buckets: TimeBucket[];
  costStacked?: CostStacked;
  defaultMetric?: TrendMetric;
  title: string;
  loading?: boolean;
  empty?: boolean;
  className?: string;
  headerExtra?: ReactNode;
}

const TOKEN_TYPE_FIELDS = [
  "prompt_tokens",
  "completion_tokens",
  "cache_read_tokens",
  "cache_write_tokens",
] as const;

const METRIC_FORMATTERS: Record<
  TrendMetric,
  { axis: (v: number) => string; tooltip: (v: number) => string }
> = {
  cost: { axis: formatMoneyCompact, tooltip: formatMoneyExact },
  requests: { axis: formatRequestsCompact, tooltip: formatRequestsExact },
  tokens: { axis: formatTokensCompact, tooltip: formatTokensExact },
};

interface Breakdown {
  buckets: StackedBucket[];
  seriesOrder: string[];
}

export function MetricTrendChart({
  buckets,
  costStacked,
  defaultMetric = "tokens",
  title,
  loading,
  empty,
  className,
  headerExtra,
}: MetricTrendChartProps) {
  const t = useTranslations("charts");
  const [metric, setMetric] = useState<TrendMetric>(defaultMetric);
  const [view, setView] = useState<ChartView>("total");

  const breakdown = useMemo<Breakdown | undefined>(() => {
    if (metric === "tokens") {
      const labels: Record<(typeof TOKEN_TYPE_FIELDS)[number], string> = {
        prompt_tokens: t("tokenType.prompt"),
        completion_tokens: t("tokenType.completion"),
        cache_read_tokens: t("tokenType.cacheRead"),
        cache_write_tokens: t("tokenType.cacheWrite"),
      };
      return {
        seriesOrder: TOKEN_TYPE_FIELDS.map((f) => labels[f]),
        buckets: buckets.map((b) => ({
          ts: b.ts,
          label: b.label,
          series: {
            [labels.prompt_tokens]: b.prompt_tokens,
            [labels.completion_tokens]: b.completion_tokens,
            [labels.cache_read_tokens]: b.cache_read_tokens,
            [labels.cache_write_tokens]: b.cache_write_tokens,
          },
        })),
      };
    }
    if (metric === "cost" && costStacked && costStacked.series_order.length > 0) {
      return { buckets: costStacked.buckets, seriesOrder: costStacked.series_order };
    }
    return undefined;
  }, [metric, buckets, costStacked, t]);

  const availableViews: ChartView[] = breakdown ? ["total", "stacked", "lines"] : ["total"];
  const effectiveView: ChartView = availableViews.includes(view) ? view : "total";

  const fmt = METRIC_FORMATTERS[metric];
  const isEmpty = empty ?? buckets.length === 0;

  const action = (
    <div className="flex flex-wrap items-center justify-end gap-2">
      <Tabs value={metric} onValueChange={(v) => setMetric(v as TrendMetric)}>
        <TabsList>
          <TabsTrigger value="cost">{t("metric.cost")}</TabsTrigger>
          <TabsTrigger value="requests">{t("metric.requests")}</TabsTrigger>
          <TabsTrigger value="tokens">{t("metric.tokens")}</TabsTrigger>
        </TabsList>
      </Tabs>
      {availableViews.length > 1 && (
        <Tabs value={effectiveView} onValueChange={(v) => setView(v as ChartView)}>
          <TabsList>
            <TabsTrigger value="total">{t("view.total")}</TabsTrigger>
            <TabsTrigger value="stacked">{t("view.stacked")}</TabsTrigger>
            <TabsTrigger value="lines">{t("view.lines")}</TabsTrigger>
          </TabsList>
        </Tabs>
      )}
      {headerExtra}
    </div>
  );

  let body: JSX.Element;
  if (effectiveView === "stacked" && breakdown) {
    body = (
      <StackedAreaBody
        buckets={breakdown.buckets}
        seriesOrder={breakdown.seriesOrder}
        axisFormatter={fmt.axis}
        tooltipFormatter={fmt.tooltip}
      />
    );
  } else if (effectiveView === "lines" && breakdown) {
    body = (
      <LinesBody
        buckets={breakdown.buckets}
        seriesOrder={breakdown.seriesOrder}
        axisFormatter={fmt.axis}
        tooltipFormatter={fmt.tooltip}
      />
    );
  } else {
    body = (
      <TotalBody
        buckets={buckets}
        metric={metric}
        axisFormatter={fmt.axis}
        tooltipFormatter={fmt.tooltip}
      />
    );
  }

  return (
    <ChartCard title={title} loading={loading} empty={isEmpty} action={action} className={className}>
      {body}
    </ChartCard>
  );
}

function TotalBody({
  buckets,
  metric,
  axisFormatter,
  tooltipFormatter,
}: {
  buckets: TimeBucket[];
  metric: TrendMetric;
  axisFormatter: (v: number) => string;
  tooltipFormatter: (v: number) => string;
}) {
  const config = useMemo<ChartConfig>(
    () => ({ [metric]: { label: metric, color: "var(--chart-1)" } }),
    [metric],
  );
  return (
    <ChartContainer config={config} className="h-[260px] w-full">
      <LineChart data={buckets} accessibilityLayer>
        <CartesianGrid vertical={false} />
        <XAxis dataKey="label" tickLine={false} axisLine={false} />
        <YAxis tickLine={false} axisLine={false} tickFormatter={axisFormatter} />
        <ChartTooltip
          content={
            <ChartTooltipContent
              formatter={(value) => (
                <span className="font-mono tabular-nums">{tooltipFormatter(Number(value))}</span>
              )}
            />
          }
        />
        <Line
          type="monotone"
          dataKey={metric}
          stroke={`var(--color-${metric})`}
          strokeWidth={2}
          dot={false}
        />
      </LineChart>
    </ChartContainer>
  );
}

function LinesBody({
  buckets,
  seriesOrder,
  axisFormatter,
  tooltipFormatter,
}: {
  buckets: StackedBucket[];
  seriesOrder: string[];
  axisFormatter: (v: number) => string;
  tooltipFormatter: (v: number) => string;
}) {
  const config = useMemo<ChartConfig>(() => {
    const cfg: ChartConfig = {};
    seriesOrder.forEach((key, i) => {
      cfg[key] = { label: key, color: CHART_COLORS[i % CHART_COLORS.length] };
    });
    return cfg;
  }, [seriesOrder]);

  const data = useMemo(
    () =>
      buckets.map((b) => {
        const row: Record<string, string | number> = { label: b.label };
        for (const key of seriesOrder) row[key] = b.series[key] ?? 0;
        return row;
      }),
    [buckets, seriesOrder],
  );

  return (
    <ChartContainer config={config} className="h-[260px] w-full">
      <LineChart data={data} accessibilityLayer>
        <CartesianGrid vertical={false} />
        <XAxis dataKey="label" tickLine={false} axisLine={false} />
        <YAxis tickLine={false} axisLine={false} tickFormatter={axisFormatter} />
        <ChartTooltip
          content={
            <ChartTooltipContent
              formatter={(value, name) => (
                <div className="flex w-full items-center justify-between gap-3">
                  <span
                    className="max-w-[10rem] truncate text-muted-foreground"
                    title={String(name)}
                  >
                    {String(name)}
                  </span>
                  <span className="font-mono tabular-nums">{tooltipFormatter(Number(value))}</span>
                </div>
              )}
            />
          }
        />
        <ChartLegend
          content={({ payload }) => (
            <ul className="flex flex-wrap items-center justify-center gap-3 pt-3 text-xs">
              {payload?.map((entry) => (
                <li key={String(entry.value)} className="flex items-center gap-1.5">
                  <span
                    className="inline-block h-2.5 w-2.5 shrink-0 rounded-[2px]"
                    style={{ backgroundColor: entry.color }}
                  />
                  <span
                    className="max-w-[10rem] truncate text-muted-foreground"
                    title={String(entry.value)}
                  >
                    {String(entry.value)}
                  </span>
                </li>
              ))}
            </ul>
          )}
        />
        {seriesOrder.map((key, i) => (
          <Line
            key={key}
            type="monotone"
            dataKey={key}
            stroke={CHART_COLORS[i % CHART_COLORS.length]}
            strokeWidth={2}
            dot={false}
          />
        ))}
      </LineChart>
    </ChartContainer>
  );
}
