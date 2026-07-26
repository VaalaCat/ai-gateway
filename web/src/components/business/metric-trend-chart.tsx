"use client";

import { useMemo, useState, type JSX, type ReactNode } from "react";
import { useTranslations } from "next-intl";
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts";

import { ChartCard } from "@/components/business/chart-card";
import { StackedAreaBody } from "@/components/business/stacked-area-chart";
import { ChartOptionSelect } from "@/components/business/chart-option-select";
import {
  useHiddenSeries,
} from "@/components/business/toggleable-chart-legend";
import { BoundedChartTooltip } from "@/components/business/bounded-chart-tooltip";
import { ResponsiveChartFrame } from "@/components/business/responsive-chart-frame";
import { ChartLegendShell, ScrollableChartLegend } from "@/components/business/scrollable-chart-legend";
import {
  ChartContainer,
  ChartTooltip,
  type ChartConfig,
} from "@/components/ui/chart";
import { chartColorForSeries, chartDashForSeries } from "@/lib/chart-colors";
import {
  formatDuration,
  formatMoneyCompact,
  formatMoneyExact,
  formatRequestsCompact,
  formatRequestsExact,
  formatTokensCompact,
  formatTokensExact,
} from "@/lib/utils/format";
import type { StackedBucket, TimeBucket } from "@/lib/types/observability";
import type { MetricTrendResponse, TrendMetric } from "@/lib/api/dashboard";

export type { TrendMetric };

/**
 * dim(model/channel)分组的多系列数据由页面级 useMetricTrend 按 (metric, dim) 拉取,经
 * grouped prop 回传;grouped 有值时对所有 metric(含 ttft/tps/cache_hit_rate)都能出多线
 * 对比,不再局限于 tokens/cost。avg/比率型(ttft/tps/cache_hit_rate)语义上不适合堆叠求和,
 * 故 dim 激活时只放开 total/lines 两种视图,stacked 仅对求和型(cost/requests/tokens)开放。
 * dim==="off"(默认,未接线的调用方如 billing 页恒为此态)维持旧行为:tokens 内置拆 4 类
 * 分量、cost 用 costStacked、其余单线——是否出现在指标切换器由 availableMetrics(后端
 * trend.metrics 自描述)决定,避免某条聚合路径不产出该序列时误导用户。
 */
type ChartView = "total" | "stacked" | "lines";

/** dim 切换器三态:off=不分组(旧行为),model/channel=按维度拉多系列 */
export type TrendDim = "off" | "model" | "channel";

/** ttft/tps/cache_hit_rate 三个可选切换 tab,由 availableMetrics 控制是否渲染 */
const EXTRA_METRICS = ["ttft", "tps", "cache_hit_rate"] as const;

/** avg/比率型指标:堆叠求和没有意义,dim 激活时只允许 total/lines,不放开 stacked */
const AVG_METRICS = new Set<TrendMetric>(["ttft", "tps", "cache_hit_rate"]);
const LOG_BACKED_METRICS = new Set<TrendMetric>(["ttft", "tps", "cache_hit_rate"]);

/** 后端 top-N 折叠桶的字面量 key(与 market-share-chart 的 OTHERS_KEY 同源约定) */
const OTHERS_KEY = "others";
/** "others" 固定中性灰，避免与真实系列的稳定色撞色。 */
const OTHERS_COLOR = "var(--muted-foreground)";

/** TrendMetric → TimeBucket 字段名的映射(ttft 对应 bucket.ttft_ms,其余同名) */
const METRIC_FIELD: Record<TrendMetric, keyof TimeBucket> = {
  cost: "cost",
  requests: "requests",
  tokens: "tokens",
  ttft: "ttft_ms",
  tps: "tps",
  cache_hit_rate: "cache_hit_rate",
};

interface CostStacked {
  buckets: StackedBucket[];
  series_order: string[];
}

export interface MetricTrendChartProps {
  buckets: TimeBucket[];
  costStacked?: CostStacked;
  /**
   * dim(model/channel)分组的多系列数据,由页面据当前 (metric, dim) 调 useMetricTrend 拿到后
   * 回传;dim==="off" 或未加载完成时可省略/undefined,chart 优雅回退到旧的单线/内置拆分。
   */
  grouped?: MetricTrendResponse;
  /** 后端 trend.metrics 自描述数组,决定 ttft/tps/cache_hit_rate 是否出现在切换器里 */
  availableMetrics?: string[];
  defaultMetric?: TrendMetric;
  /** 受控 metric;不传则组件内部自管理(旧调用方如 billing 页维持现状) */
  metric?: TrendMetric;
  onMetricChange?: (m: TrendMetric) => void;
  /** 受控 dim;同时传入 dim + onDimChange 才会渲染「关闭/按模型/按渠道」切换器 */
  dim?: TrendDim;
  onDimChange?: (d: TrendDim) => void;
  title: string;
  loading?: boolean;
  empty?: boolean;
  className?: string;
  headerExtra?: ReactNode;
  /** percentile 等只存在于 grouped response 的统计禁止回退为 total 平均曲线。 */
  groupedOnly?: boolean;
  /** 日志库降级时保留 core 趋势，同时在卡内说明性能指标暂不可用。 */
  logUnavailable?: boolean;
}

const TOKEN_TYPE_FIELDS = [
  "prompt_tokens",
  "completion_tokens",
  "cache_read_tokens",
  "cache_write_tokens",
] as const;

const formatTpsAxis = (v: number) => v.toFixed(0);
const formatTpsTooltip = (v: number) => `${v.toFixed(1)} tok/s`;
const formatPercentAxis = (v: number) => `${v.toFixed(0)}%`;
const formatPercentTooltip = (v: number) => `${v.toFixed(1)}%`;

const METRIC_FORMATTERS: Record<
  TrendMetric,
  { axis: (v: number) => string; tooltip: (v: number) => string }
> = {
  cost: { axis: formatMoneyCompact, tooltip: formatMoneyExact },
  requests: { axis: formatRequestsCompact, tooltip: formatRequestsExact },
  tokens: { axis: formatTokensCompact, tooltip: formatTokensExact },
  ttft: { axis: formatDuration, tooltip: formatDuration },
  tps: { axis: formatTpsAxis, tooltip: formatTpsTooltip },
  cache_hit_rate: { axis: formatPercentAxis, tooltip: formatPercentTooltip },
};

interface Breakdown {
  buckets: StackedBucket[];
  seriesOrder: string[];
}

export function MetricTrendChart({
  buckets,
  costStacked,
  grouped,
  availableMetrics,
  defaultMetric = "tokens",
  metric: metricProp,
  onMetricChange,
  dim: dimProp,
  onDimChange,
  title,
  loading,
  empty,
  className,
  headerExtra,
  groupedOnly = false,
  logUnavailable = false,
}: MetricTrendChartProps) {
  const t = useTranslations("charts");
  const [internalMetric, setInternalMetric] = useState<TrendMetric>(metricProp ?? defaultMetric);
  const metric = metricProp ?? internalMetric;
  const changeMetric = (m: TrendMetric) => {
    if (onMetricChange) onMetricChange(m);
    else setInternalMetric(m);
  };

  const [view, setView] = useState<ChartView>("total");
  const [internalDim, setInternalDim] = useState<TrendDim>(dimProp ?? "off");
  const dim = dimProp ?? internalDim;
  const changeDim = (d: TrendDim) => {
    if (onDimChange) onDimChange(d);
    else setInternalDim(d);
    setView(d === "off" ? "total" : "lines");
  };
  // dim 切换器只在调用方显式接线(同时传 dim + onDimChange)时渲染,未接线的旧调用方(billing 页)
  // 不显示、也恒为 "off",维持现状。
  const showDimToggle = dimProp !== undefined && onDimChange !== undefined;

  const breakdown = useMemo<Breakdown | undefined>(() => {
    // dim 激活时只认当前请求的分组数据，不回退到 token 拆分或旧 metric 数据。
    if (dim !== "off") {
      return grouped && grouped.series_order.length > 0
        ? { seriesOrder: grouped.series_order, buckets: grouped.buckets }
        : undefined;
    }
    const hasTokenComponents = buckets.length > 0 && buckets.every((bucket) =>
      TOKEN_TYPE_FIELDS.every((field) => typeof bucket[field] === "number"),
    );
    if (metric === "tokens" && hasTokenComponents) {
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
            [labels.prompt_tokens]: b.prompt_tokens!,
            [labels.completion_tokens]: b.completion_tokens!,
            [labels.cache_read_tokens]: b.cache_read_tokens!,
            [labels.cache_write_tokens]: b.cache_write_tokens!,
          },
        })),
      };
    }
    if (metric === "cost" && costStacked && costStacked.series_order.length > 0) {
      return { buckets: costStacked.buckets, seriesOrder: costStacked.series_order };
    }
    return undefined;
  }, [dim, grouped, metric, buckets, costStacked, t]);

  // avg/比率型指标堆叠求和无意义,dim 激活时只放开 total/lines;求和型 total/stacked/lines 都可。
  const availableViews: ChartView[] = groupedOnly
    ? breakdown ? ["lines"] : []
    : breakdown
      ? AVG_METRICS.has(metric)
        ? ["total", "lines"]
        : ["total", "stacked", "lines"]
      : ["total"];
  const effectiveView: ChartView = groupedOnly
    ? "lines"
    : availableViews.includes(view) ? view : "total";

  const fmt = METRIC_FORMATTERS[metric];
  const isEmpty = empty ?? (groupedOnly ? !loading && !breakdown : buckets.length === 0);
  const performanceUnavailable = logUnavailable && LOG_BACKED_METRICS.has(metric);

  const action = (
    <div className="flex flex-wrap items-center justify-end gap-2">
      {showDimToggle && (
        <ChartOptionSelect
          value={dim}
          onValueChange={changeDim}
          label={t("prefix.dim")}
          options={[
            { value: "off", label: t("trend.dimOff") },
            { value: "model", label: t("trend.dimModel") },
            { value: "channel", label: t("trend.dimChannel") },
          ]}
        />
      )}
      <ChartOptionSelect
        value={metric}
        onValueChange={changeMetric}
        label={t("prefix.metric")}
        options={[
          { value: "cost", label: t("metric.cost") },
          { value: "requests", label: t("metric.requests") },
          { value: "tokens", label: t("metric.tokens") },
          ...EXTRA_METRICS.filter((m) => availableMetrics?.includes(m)).map((m) => ({
            value: m,
            label: t(`metric.${m}`),
          })),
        ]}
      />
      {!groupedOnly && availableViews.length > 1 && (
        <ChartOptionSelect
          value={effectiveView}
          onValueChange={(v) => setView(v)}
          label={t("prefix.view")}
          options={availableViews.map((v) => ({ value: v, label: t(`view.${v}`) }))}
        />
      )}
      {headerExtra}
    </div>
  );

  let body: JSX.Element;
  if (groupedOnly) {
    body = breakdown ? (
      <LinesBody
        buckets={breakdown.buckets}
        seriesOrder={breakdown.seriesOrder}
        axisFormatter={fmt.axis}
        tooltipFormatter={fmt.tooltip}
        othersLabel={t("trend.others")}
        legendLabel={t("legend.series")}
      />
    ) : <ResponsiveChartFrame><div /></ResponsiveChartFrame>;
  } else if (effectiveView === "stacked" && breakdown) {
    body = (
      <StackedAreaBody
        buckets={breakdown.buckets}
        seriesOrder={breakdown.seriesOrder}
        axisFormatter={fmt.axis}
        tooltipFormatter={fmt.tooltip}
        othersLabel={t("trend.others")}
        legendLabel={t("legend.series")}
        allHiddenLabel={t("legend.allHidden")}
      />
    );
  } else if (effectiveView === "lines" && breakdown) {
    body = (
      <LinesBody
        buckets={breakdown.buckets}
        seriesOrder={breakdown.seriesOrder}
        axisFormatter={fmt.axis}
        tooltipFormatter={fmt.tooltip}
        othersLabel={t("trend.others")}
        legendLabel={t("legend.series")}
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
    <ChartCard
      title={title}
      loading={loading}
      empty={isEmpty}
      error={performanceUnavailable ? t("trend.logUnavailable") : undefined}
      action={action}
      className={className}
    >
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
    () => ({ [metric]: { label: metric, color: chartColorForSeries(metric) } }),
    [metric],
  );
  return (
    <ResponsiveChartFrame
      // behavior change: keep avg total and percentile grouped frames the same height.
      legend={<ChartLegendShell placeholder />}
    >
    <ChartContainer config={config} className="h-full w-full">
      <LineChart data={buckets} accessibilityLayer>
        <CartesianGrid vertical={false} />
        <XAxis dataKey="label" tickLine={false} axisLine={false} />
        <YAxis tickLine={false} axisLine={false} tickFormatter={axisFormatter} />
        <ChartTooltip
          content={
            <BoundedChartTooltip
              formatter={(value) => (
                <span className="font-mono tabular-nums">{tooltipFormatter(Number(value))}</span>
              )}
            />
          }
        />
        <Line
          type="monotone"
          dataKey={METRIC_FIELD[metric]}
          stroke={`var(--color-${metric})`}
          strokeWidth={2}
          strokeDasharray={chartDashForSeries(metric)}
          dot={false}
        />
      </LineChart>
    </ChartContainer>
    </ResponsiveChartFrame>
  );
}

function LinesBody({
  buckets,
  seriesOrder,
  axisFormatter,
  tooltipFormatter,
  othersLabel,
  legendLabel,
}: {
  buckets: StackedBucket[];
  seriesOrder: string[];
  axisFormatter: (v: number) => string;
  tooltipFormatter: (v: number) => string;
  /** dim(model/channel)分组数据里 "others" 折叠系列的展示名;非 dim 分组场景(tokens/cost 内置
   * 拆分)不含 "others" key,这个 label 不会被用到 */
  othersLabel?: string;
  legendLabel: string;
}) {
  const { hidden, toggle } = useHiddenSeries(seriesOrder);

  const config = useMemo<ChartConfig>(() => {
    const cfg: ChartConfig = {};
    // "others" 固定中性灰，真实系列颜色按名称稳定映射。
    // (同 market-share-chart 的处理)；轮转超出调色板长度时取模复用。
    seriesOrder.forEach((key) => {
      if (key === OTHERS_KEY) {
        cfg[key] = { label: othersLabel ?? key, color: OTHERS_COLOR };
      } else {
        cfg[key] = { label: key, color: chartColorForSeries(key) };
      }
    });
    return cfg;
  }, [seriesOrder, othersLabel]);

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
    <ResponsiveChartFrame
      legend={
        <ScrollableChartLegend
          ariaLabel={legendLabel}
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
      <LineChart data={data} accessibilityLayer>
        <CartesianGrid vertical={false} />
        <XAxis dataKey="label" tickLine={false} axisLine={false} />
        <YAxis tickLine={false} axisLine={false} tickFormatter={axisFormatter} />
        <ChartTooltip
          content={
            <BoundedChartTooltip
              formatter={(value, name) => {
                const label = config[String(name)]?.label ?? String(name);
                return (
                  <div className="flex w-full items-center justify-between gap-3">
                    <span
                      className="max-w-[10rem] truncate text-muted-foreground"
                      title={String(label)}
                    >
                      {String(label)}
                    </span>
                    <span className="font-mono tabular-nums">{tooltipFormatter(Number(value))}</span>
                  </div>
                );
              }}
            />
          }
        />
        {seriesOrder.map((key, i) => (
          <Line
            key={key}
            type="monotone"
            dataKey={key}
            stroke={config[key]?.color ?? chartColorForSeries(`${key}-${i}`)}
            strokeWidth={2}
            strokeDasharray={chartDashForSeries(key)}
            dot={false}
            hide={hidden.has(key)}
          />
        ))}
      </LineChart>
    </ChartContainer>
    </ResponsiveChartFrame>
  );
}
