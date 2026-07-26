"use client";

import { useMemo, useState } from "react";
import { useTranslations } from "next-intl";
import {
  CartesianGrid,
  Line,
  LineChart,
  XAxis,
  YAxis,
} from "recharts";

import { ChartCard } from "@/components/business/chart-card";
import { BoundedChartTooltip } from "@/components/business/bounded-chart-tooltip";
import { ChartOptionSelect } from "@/components/business/chart-option-select";
import {
  ChartContainer,
  ChartTooltip,
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
import type { TimeBucket } from "@/lib/types/observability";
import { chartColorForSeries } from "@/lib/chart-colors";

export type { TimeBucket };
export type TrendMetric = "cost" | "requests" | "tokens";

interface TrendChartProps {
  buckets: TimeBucket[];
  title: string;
  loading?: boolean;
  empty?: boolean;
  metric?: TrendMetric;
  onMetricChange?: (m: TrendMetric) => void;
  className?: string;
}

// 策略表: metric → (axis formatter, tooltip formatter, ChartCard 副标单位)
const METRIC_FORMATTERS: Record<TrendMetric, {
  axis: (v: number) => string;
  tooltip: (v: number) => string;
  unit: string;
}> = {
  cost:     { axis: formatMoneyCompact,    tooltip: formatMoneyExact,    unit: "Cost (USD)" },
  requests: { axis: formatRequestsCompact, tooltip: formatRequestsExact, unit: "Requests" },
  tokens:   { axis: formatTokensCompact,   tooltip: formatTokensExact,   unit: "Tokens" },
};

function isTrendMetric(v: string): v is TrendMetric {
  return v === "cost" || v === "requests" || v === "tokens";
}

export function TrendChart({
  buckets,
  title,
  loading,
  empty,
  metric: metricProp,
  onMetricChange,
  className,
}: TrendChartProps) {
  const t = useTranslations("charts");
  const [internalMetric, setInternalMetric] = useState<TrendMetric>(
    metricProp ?? "requests",
  );
  const metric: TrendMetric = metricProp ?? internalMetric;

  const handleChange = (v: string) => {
    if (!isTrendMetric(v)) return;
    if (onMetricChange) onMetricChange(v);
    else setInternalMetric(v);
  };

  const config = useMemo<ChartConfig>(
    () => ({
      [metric]: { label: t(`metric.${metric}`), color: chartColorForSeries(metric) },
    }),
    [metric, t],
  );

  const isEmpty = empty ?? buckets.length === 0;
  const fmt = METRIC_FORMATTERS[metric];
  // behavior change: keep a one-bucket trend visible when no line path can be drawn.
  const showSinglePoint = buckets.length === 1;

  const action = (
    <ChartOptionSelect
      value={metric}
      onValueChange={handleChange}
      label={t("prefix.metric")}
      options={[
        { value: "cost", label: t("metric.cost") },
        { value: "requests", label: t("metric.requests") },
        { value: "tokens", label: t("metric.tokens") },
      ]}
    />
  );

  return (
    <ChartCard
      title={title}
      sub={fmt.unit}
      loading={loading}
      empty={isEmpty}
      action={action}
      className={className}
      chartFrame={{}}
    >
      <ChartContainer config={config} className="h-full w-full">
        <LineChart data={buckets} accessibilityLayer>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="label" tickLine={false} axisLine={false} />
          <YAxis
            tickLine={false}
            axisLine={false}
            tickFormatter={fmt.axis}
          />
          <ChartTooltip
            content={
              <BoundedChartTooltip
                formatter={(value) => (
                  <span className="font-mono tabular-nums">
                    {fmt.tooltip(Number(value))}
                  </span>
                )}
              />
            }
          />
          <Line
            type="monotone"
            dataKey={metric}
            stroke={`var(--color-${metric})`}
            strokeWidth={2}
            dot={showSinglePoint}
          />
        </LineChart>
      </ChartContainer>
    </ChartCard>
  );
}
