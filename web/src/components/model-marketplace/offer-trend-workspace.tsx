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

import { BoundedChartTooltip } from "@/components/business/bounded-chart-tooltip";
import { ChartCard } from "@/components/business/chart-card";
import { ChartOptionSelect } from "@/components/business/chart-option-select";
import { ScrollableChartLegend } from "@/components/business/scrollable-chart-legend";
import { useHiddenSeries } from "@/components/business/toggleable-chart-legend";
import {
  ChartContainer,
  ChartTooltip,
  type ChartConfig,
} from "@/components/ui/chart";
import type {
  MarketplaceModelOfferDetail,
  MarketplacePerformanceTrendPoint,
} from "@/lib/api/model-marketplace";
import {
  CHART_LINE_ACTIVE_DOT,
  chartColorForSeries,
} from "@/lib/chart-colors";
import {
  nonNegativeFiniteNumber,
  percentValueOrNull,
  unixSecondsOrNull,
} from "@/lib/model-marketplace-values";
import {
  formatDuration,
  formatPercentAxis,
  formatPercentValue,
  formatTokensCompact,
  formatTokensExact,
  formatTpsAxis,
  formatTpsValue,
} from "@/lib/utils/format";

const MAX_TREND_OFFERS = 5;

export type TrendMetric =
  | "ttft_avg_ms"
  | "tps_avg"
  | "success_rate"
  | "total"
  | "input"
  | "cache_read"
  | "output"
  | "cache_write";

type MetricFormatter = Readonly<{
  axis: (value: number) => string;
  tooltip: (value: number) => string;
}>;

export const TREND_METRIC_FORMATTERS = {
  ttft_avg_ms: { axis: formatDuration, tooltip: formatDuration },
  tps_avg: { axis: formatTpsAxis, tooltip: formatTpsValue },
  success_rate: { axis: formatPercentAxis, tooltip: formatPercentValue },
  total: { axis: formatTokensCompact, tooltip: formatTokensExact },
  input: { axis: formatTokensCompact, tooltip: formatTokensExact },
  cache_read: { axis: formatTokensCompact, tooltip: formatTokensExact },
  output: { axis: formatTokensCompact, tooltip: formatTokensExact },
  cache_write: { axis: formatTokensCompact, tooltip: formatTokensExact },
} satisfies Record<TrendMetric, MetricFormatter>;

const TOKEN_TREND_METRICS = [
  "total",
  "input",
  "cache_read",
  "output",
  "cache_write",
] as const satisfies readonly TrendMetric[];

const PRIMARY_TREND_METRICS = [
  "ttft_avg_ms",
  "tps_avg",
  "success_rate",
  "token",
] as const;

type TokenTrendMetric = (typeof TOKEN_TREND_METRICS)[number];
type PrimaryTrendMetric = (typeof PRIMARY_TREND_METRICS)[number];

const PRIMARY_TREND_METRIC_LABEL_KEYS = {
  ttft_avg_ms: "trendMetric.ttft_avg_ms",
  tps_avg: "trendMetric.tps_avg",
  success_rate: "trendMetricSla",
  token: "trendMetricToken",
} as const satisfies Record<PrimaryTrendMetric, string>;

const METRIC_VALUE = {
  ttft_avg_ms: (point) => nonNegativeFiniteNumber(point.ttft_avg_ms),
  tps_avg: (point) => nonNegativeFiniteNumber(point.tps_avg),
  success_rate: (point) => percentValueOrNull(point.success_rate),
  total: (point) => nonNegativeFiniteNumber(point.token_units.total),
  input: (point) => nonNegativeFiniteNumber(point.token_units.input),
  cache_read: (point) => nonNegativeFiniteNumber(point.token_units.cache_read),
  output: (point) => nonNegativeFiniteNumber(point.token_units.output),
  cache_write: (point) => nonNegativeFiniteNumber(point.token_units.cache_write),
} satisfies Record<
  TrendMetric,
  (point: MarketplacePerformanceTrendPoint) => number | null
>;

interface TrendRow {
  startedAt: number;
  label: string;
  [series: string]: string | number | null;
}

function isTokenTrendMetric(metric: TrendMetric): metric is TokenTrendMetric {
  return (TOKEN_TREND_METRICS as readonly TrendMetric[]).includes(metric);
}

function isPrimaryTrendMetric(value: string): value is PrimaryTrendMetric {
  return (PRIMARY_TREND_METRICS as readonly string[]).includes(value);
}

function isTokenTrendMetricValue(value: string): value is TokenTrendMetric {
  return (TOKEN_TREND_METRICS as readonly string[]).includes(value);
}

function formatUtcTick(timestamp: number) {
  return new Intl.DateTimeFormat("en-GB", {
    timeZone: "UTC",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(new Date(timestamp * 1_000));
}

function trendRows(
  offers: readonly MarketplaceModelOfferDetail[],
  metric: TrendMetric,
) {
  const byTimestamp = new Map<number, TrendRow>();
  for (const offer of offers) {
    for (const point of offer.trend_series) {
      const startedAt = unixSecondsOrNull(point.started_at);
      if (startedAt === null) continue;

      const row = byTimestamp.get(startedAt) ?? {
        startedAt,
        label: formatUtcTick(startedAt),
      };
      row[offer.offer_ref] = METRIC_VALUE[metric](point);
      byTimestamp.set(startedAt, row);
    }
  }
  return [...byTimestamp.values()]
    .sort((left, right) => left.startedAt - right.startedAt)
    .map((row) => {
      for (const offer of offers) {
        if (!(offer.offer_ref in row)) row[offer.offer_ref] = null;
      }
      return row;
    });
}

function TrendMetricControls({
  metric,
  onChange,
}: {
  metric: TrendMetric;
  onChange: (metric: TrendMetric) => void;
}) {
  const t = useTranslations("modelMarketplace.detail");
  const primaryMetric: PrimaryTrendMetric = isTokenTrendMetric(metric) ? "token" : metric;
  const primaryOptions = PRIMARY_TREND_METRICS.map((value) => ({
    value,
    label: t(PRIMARY_TREND_METRIC_LABEL_KEYS[value]),
  }));
  const tokenOptions = TOKEN_TREND_METRICS.map((value) => ({
    value,
    label: t(`trendMetric.${value}`),
  }));

  return (
    <div className="flex flex-wrap items-center justify-end gap-2">
      <ChartOptionSelect
        label={t("trendPrimaryMetricLabel")}
        value={primaryMetric}
        options={primaryOptions}
        onValueChange={(value) => onChange(value === "token"
          ? (isTokenTrendMetric(metric) ? metric : "total")
          : value)}
      />
      {isTokenTrendMetric(metric) ? (
        <ChartOptionSelect
          label={t("trendTokenMetricLabel")}
          value={metric}
          options={tokenOptions}
          onValueChange={(value) => {
            if (isTokenTrendMetricValue(value)) onChange(value);
          }}
        />
      ) : null}
    </div>
  );
}

function TrendLineChart({
  config,
  data,
  formatter,
  hidden,
  offers,
}: {
  config: ChartConfig;
  data: TrendRow[];
  formatter: MetricFormatter;
  hidden: ReadonlySet<string>;
  offers: readonly MarketplaceModelOfferDetail[];
}) {
  return (
    <ChartContainer config={config} className="h-full w-full">
      <LineChart data={data} accessibilityLayer>
        <CartesianGrid vertical={false} />
        <XAxis dataKey="label" tickLine={false} axisLine={false} minTickGap={24} />
        <YAxis
          tickLine={false}
          axisLine={false}
          width={56}
          tickFormatter={formatter.axis}
        />
        <ChartTooltip
          content={
            <BoundedChartTooltip
              formatter={(value, name) => {
                const label = config[String(name)]?.label ?? String(name);
                return (
                  <div className="flex w-full items-center justify-between gap-3">
                    <span
                      className="max-w-32 truncate text-muted-foreground"
                      title={String(label)}
                    >
                      {String(label)}
                    </span>
                    <span className="font-mono tabular-nums">
                      {formatter.tooltip(Number(value))}
                    </span>
                  </div>
                );
              }}
            />
          }
        />
        {offers.map((offer) => (
          <Line
            key={offer.offer_ref}
            type="monotone"
            dataKey={offer.offer_ref}
            stroke={chartColorForSeries(offer.offer_ref)}
            strokeWidth={2}
            dot={false}
            activeDot={CHART_LINE_ACTIVE_DOT}
            hide={hidden.has(offer.offer_ref)}
            connectNulls={false}
          />
        ))}
      </LineChart>
    </ChartContainer>
  );
}

function OfferTrendChart({
  metric,
  offers,
  onMetricChange,
}: {
  metric: TrendMetric;
  offers: readonly MarketplaceModelOfferDetail[];
  onMetricChange: (metric: TrendMetric) => void;
}) {
  const t = useTranslations("modelMarketplace.detail");
  const seriesOrder = offers.map((offer) => offer.offer_ref);
  const { hidden, toggle } = useHiddenSeries(seriesOrder);
  const config = useMemo<ChartConfig>(() => Object.fromEntries(
    offers.map((offer) => [offer.offer_ref, {
      label: offer.display_name,
      color: chartColorForSeries(offer.offer_ref),
    }]),
  ), [offers]);
  const data = useMemo(() => trendRows(offers, metric), [offers, metric]);
  const formatter = TREND_METRIC_FORMATTERS[metric];
  const hasData = data.some((row) => seriesOrder.some((ref) =>
    typeof row[ref] === "number" && Number.isFinite(row[ref]),
  ));

  return (
    <ChartCard
      title={t("trendTitle")}
      action={<TrendMetricControls metric={metric} onChange={onMetricChange} />}
      empty={!hasData}
      emptyHint={t("trendEmpty")}
      chartFrame={{
        minHeight: 260,
        legend: (
          <ScrollableChartLegend
            ariaLabel={t("trendLegendLabel")}
            items={offers.map((offer) => ({
              key: offer.offer_ref,
              label: offer.display_name,
              color: chartColorForSeries(offer.offer_ref),
              hidden: hidden.has(offer.offer_ref),
            }))}
            onToggle={toggle}
          />
        ),
      }}
    >
      <TrendLineChart
        config={config}
        data={data}
        formatter={formatter}
        hidden={hidden}
        offers={offers}
      />
    </ChartCard>
  );
}

export function OfferTrendWorkspace({ offers }: { offers: MarketplaceModelOfferDetail[] }) {
  const t = useTranslations("modelMarketplace.detail");
  const [metric, setMetric] = useState<TrendMetric>("ttft_avg_ms");
  const comparedOffers = useMemo(() => offers.slice(0, MAX_TREND_OFFERS), [offers]);
  if (comparedOffers.length === 0) return null;

  return (
    <section className="min-w-0" aria-label={t("trendTitle")}>
      <OfferTrendChart
        metric={metric}
        offers={comparedOffers}
        onMetricChange={setMetric}
      />
    </section>
  );
}
