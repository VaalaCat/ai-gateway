"use client";

import { useMemo } from "react";
import { useTranslations } from "next-intl";
import {
  CartesianGrid,
  Line,
  LineChart,
  XAxis,
  YAxis,
} from "recharts";

import { useIsMobile } from "@/hooks/use-mobile";

import { ChartCard } from "@/components/business/chart-card";
import {
  useHiddenSeries,
} from "@/components/business/toggleable-chart-legend";
import { BoundedChartTooltip } from "@/components/business/bounded-chart-tooltip";
import { ResponsiveChartFrame } from "@/components/business/responsive-chart-frame";
import { ScrollableChartLegend } from "@/components/business/scrollable-chart-legend";
import {
  ChartContainer,
  ChartTooltip,
  type ChartConfig,
} from "@/components/ui/chart";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@/components/ui/tabs";

import { formatMoneyCompact, formatMoneyExact } from "@/lib/utils/format";
import type { BillingDailySeriesItem } from "@/lib/api/byok-stats";
import {
  CHART_LINE_ACTIVE_DOT,
  chartColorForSeries,
} from "@/lib/chart-colors";

interface ChartProps {
  items: BillingDailySeriesItem[];
  loading: boolean;
}

function RequestsChart({ items, loading }: ChartProps) {
  const t = useTranslations("byok.stats");
  const config = {
    request_count: { label: t("tableRequests"), color: chartColorForSeries("request_count") },
  } satisfies ChartConfig;

  return (
    <ChartCard
      title={t("chartRequests")}
      sub="Requests"
      loading={loading}
      empty={items.length === 0}
      emptyHint={t("trendEmpty")}
      chartFrame={{}}
    >
      <ChartContainer config={config} className="h-full w-full">
        <LineChart data={items} accessibilityLayer>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="date" tickLine={false} axisLine={false} />
          <YAxis tickLine={false} axisLine={false} />
          <ChartTooltip content={<BoundedChartTooltip />} />
          <Line
            type="monotone"
            dataKey="request_count"
            stroke={chartColorForSeries("request_count")}
            strokeWidth={2}
            dot={false}
            activeDot={CHART_LINE_ACTIVE_DOT}
          />
        </LineChart>
      </ChartContainer>
    </ChartCard>
  );
}

const TOKEN_KEYS = [
  "prompt_tokens",
  "completion_tokens",
  "cache_read_tokens",
  "cache_write_tokens",
] as const;

export function TokensChart({ items, loading }: ChartProps) {
  const t = useTranslations("byok.stats");
  const tLegend = useTranslations("charts.legend");
  const { hidden, toggle } = useHiddenSeries(TOKEN_KEYS);
  const config = Object.fromEntries(TOKEN_KEYS.map((key) => [key, {
    label: t(key === "prompt_tokens" ? "breakdownPromptTokens"
      : key === "completion_tokens" ? "breakdownCompletionTokens"
        : key === "cache_read_tokens" ? "breakdownCacheRead" : "breakdownCacheWrite"),
    color: chartColorForSeries(key),
  }])) satisfies ChartConfig;
  const allHidden = TOKEN_KEYS.every((k) => hidden.has(k));

  return (
    <ChartCard
      title={t("chartTokens")}
      sub="Tokens"
      loading={loading}
      empty={items.length === 0}
      emptyHint={t("trendEmpty")}
    >
      <ResponsiveChartFrame
        legend={
          <div className="space-y-1">
            <ScrollableChartLegend
              ariaLabel={tLegend("series")}
              items={TOKEN_KEYS.map((key) => ({
                key,
                label: config[key].label,
                color: config[key].color!,
                hidden: hidden.has(key),
              }))}
              onToggle={toggle}
            />
            {allHidden && <p className="text-center text-xs text-muted-foreground">{t("chartAllHidden")}</p>}
          </div>
        }
      >
      <ChartContainer config={config} className="h-full w-full">
        <LineChart data={items} accessibilityLayer>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="date" tickLine={false} axisLine={false} />
          <YAxis tickLine={false} axisLine={false} />
          <ChartTooltip content={<BoundedChartTooltip />} />
          {TOKEN_KEYS.map((k) => (
            <Line
              key={k}
              type="monotone"
              dataKey={k}
              stroke={chartColorForSeries(k)}
              strokeWidth={2}
              dot={false}
              activeDot={CHART_LINE_ACTIVE_DOT}
              hide={hidden.has(k)}
            />
          ))}
        </LineChart>
      </ChartContainer>
      </ResponsiveChartFrame>
    </ChartCard>
  );
}

const COST_KEYS = ["input_cost", "output_cost"] as const;

function CostChart({ items, loading }: ChartProps) {
  const t = useTranslations("byok.stats");
  const tLegend = useTranslations("charts.legend");
  const { hidden, toggle } = useHiddenSeries(COST_KEYS);
  const config = {
    input_cost: { label: t("chartInputCost"), color: chartColorForSeries("input_cost") },
    output_cost: { label: t("chartOutputCost"), color: chartColorForSeries("output_cost") },
  } satisfies ChartConfig;
  const allHidden = COST_KEYS.every((k) => hidden.has(k));

  return (
    <ChartCard
      title={t("chartCost")}
      sub="Cost (USD)"
      loading={loading}
      empty={items.length === 0}
      emptyHint={t("trendEmpty")}
    >
      <ResponsiveChartFrame
        legend={
          <div className="space-y-1">
            <ScrollableChartLegend
              ariaLabel={tLegend("series")}
              items={COST_KEYS.map((key) => ({ key, label: config[key].label, color: config[key].color, hidden: hidden.has(key) }))}
              onToggle={toggle}
            />
            {allHidden && <p className="text-center text-xs text-muted-foreground">{t("chartAllHidden")}</p>}
          </div>
        }
      >
      <ChartContainer config={config} className="h-full w-full">
        <LineChart data={items} accessibilityLayer>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="date" tickLine={false} axisLine={false} />
          <YAxis
            tickLine={false}
            axisLine={false}
            tickFormatter={formatMoneyCompact}
          />
          <ChartTooltip
            content={<BoundedChartTooltip formatter={(v) => formatMoneyExact(Number(v))} />}
          />
          {COST_KEYS.map((k) => (
            <Line
              key={k}
              type="monotone"
              dataKey={k}
              stroke={chartColorForSeries(k)}
              strokeWidth={2}
              dot={false}
              activeDot={CHART_LINE_ACTIVE_DOT}
              hide={hidden.has(k)}
            />
          ))}
        </LineChart>
      </ChartContainer>
      </ResponsiveChartFrame>
    </ChartCard>
  );
}

export function BYOKTrendCharts({ items, loading }: ChartProps) {
  const isMobile = useIsMobile();
  const t = useTranslations("byok.stats");

  const charts = useMemo(
    () => [
      { key: "requests", title: t("chartRequests"), Comp: RequestsChart },
      { key: "tokens", title: t("chartTokens"), Comp: TokensChart },
      { key: "cost", title: t("chartCost"), Comp: CostChart },
    ],
    [t],
  );

  if (isMobile) {
    return (
      <Tabs defaultValue="tokens">
        <TabsList className="grid w-full grid-cols-3">
          {charts.map((c) => (
            <TabsTrigger key={c.key} value={c.key}>
              {c.title}
            </TabsTrigger>
          ))}
        </TabsList>
        {charts.map((c) => (
          <TabsContent key={c.key} value={c.key} className="mt-4">
            <c.Comp items={items} loading={loading} />
          </TabsContent>
        ))}
      </Tabs>
    );
  }

  return (
    <div className="grid grid-cols-1 gap-4 xl:grid-cols-3">
      {charts.map((c) => (
        <c.Comp key={c.key} items={items} loading={loading} />
      ))}
    </div>
  );
}
