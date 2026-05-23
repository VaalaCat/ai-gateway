"use client";

import { useMemo, useState } from "react";
import {
  CartesianGrid,
  Line,
  LineChart,
  XAxis,
  YAxis,
} from "recharts";

import { ChartCard } from "@/components/business/chart-card";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  ChartContainer,
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
import type { TimeBucket } from "@/lib/types/observability";

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

const METRIC_LABELS: Record<TrendMetric, string> = {
  cost: "Cost",
  requests: "Requests",
  tokens: "Tokens",
};

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
      [metric]: { label: METRIC_LABELS[metric], color: "var(--chart-1)" },
    }),
    [metric],
  );

  const isEmpty = empty ?? buckets.length === 0;
  const fmt = METRIC_FORMATTERS[metric];

  const action = (
    <Tabs value={metric} onValueChange={handleChange}>
      <TabsList>
        <TabsTrigger value="cost">Cost</TabsTrigger>
        <TabsTrigger value="requests">Requests</TabsTrigger>
        <TabsTrigger value="tokens">Tokens</TabsTrigger>
      </TabsList>
    </Tabs>
  );

  return (
    <ChartCard
      title={title}
      sub={fmt.unit}
      loading={loading}
      empty={isEmpty}
      action={action}
      className={className}
    >
      <ChartContainer config={config} className="h-[260px] w-full">
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
              <ChartTooltipContent
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
            dot={false}
          />
        </LineChart>
      </ChartContainer>
    </ChartCard>
  );
}
