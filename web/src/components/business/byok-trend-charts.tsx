"use client";

import { useCallback, useMemo, useState } from "react";
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
  ChartContainer,
  ChartLegend,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@/components/ui/tabs";

import { cn } from "@/lib/utils";
import { formatMoneyCompact, formatMoneyExact } from "@/lib/utils/format";
import type { BillingDailySeriesItem } from "@/lib/api/byok-stats";

interface ChartProps {
  items: BillingDailySeriesItem[];
  loading: boolean;
}

function RequestsChart({ items, loading }: ChartProps) {
  const t = useTranslations("byok.stats");
  const config = {
    request_count: { label: t("tableRequests"), color: "var(--chart-1)" },
  } satisfies ChartConfig;

  return (
    <ChartCard
      title={t("chartRequests")}
      sub="Requests"
      loading={loading}
      empty={items.length === 0}
      emptyHint={t("trendEmpty")}
    >
      <ChartContainer config={config} className="h-[260px] w-full">
        <LineChart data={items} accessibilityLayer>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="date" tickLine={false} axisLine={false} />
          <YAxis tickLine={false} axisLine={false} />
          <ChartTooltip content={<ChartTooltipContent />} />
          <Line
            type="monotone"
            dataKey="request_count"
            stroke="var(--color-request_count)"
            strokeWidth={2}
            dot={false}
          />
        </LineChart>
      </ChartContainer>
    </ChartCard>
  );
}

function useToggleSet<K extends string>(_initial: readonly K[]) {
  const [hidden, setHidden] = useState<Set<K>>(new Set());
  const toggle = useCallback((k: K) => {
    setHidden((prev) => {
      const next = new Set(prev);
      if (next.has(k)) next.delete(k);
      else next.add(k);
      return next;
    });
  }, []);
  const isHidden = useCallback((k: K) => hidden.has(k), [hidden]);
  return { isHidden, toggle };
}

const TOKEN_KEYS = [
  "prompt_tokens",
  "completion_tokens",
  "cache_read_tokens",
  "cache_write_tokens",
] as const;
type TokenKey = (typeof TOKEN_KEYS)[number];

function TokensChart({ items, loading }: ChartProps) {
  const t = useTranslations("byok.stats");
  const series = useToggleSet(TOKEN_KEYS);
  const config = {
    prompt_tokens: { label: t("breakdownPromptTokens"), color: "var(--chart-1)" },
    completion_tokens: { label: t("breakdownCompletionTokens"), color: "var(--chart-2)" },
    cache_read_tokens: { label: t("breakdownCacheRead"), color: "var(--chart-3)" },
    cache_write_tokens: { label: t("breakdownCacheWrite"), color: "var(--chart-4)" },
  } satisfies ChartConfig;
  const allHidden = TOKEN_KEYS.every((k) => series.isHidden(k));

  return (
    <ChartCard
      title={t("chartTokens")}
      sub="Tokens"
      loading={loading}
      empty={items.length === 0}
      emptyHint={t("trendEmpty")}
    >
      <ChartContainer config={config} className="h-[260px] w-full">
        <LineChart data={items} accessibilityLayer>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="date" tickLine={false} axisLine={false} />
          <YAxis tickLine={false} axisLine={false} />
          <ChartTooltip content={<ChartTooltipContent />} />
          {TOKEN_KEYS.map((k) => (
            <Line
              key={k}
              type="monotone"
              dataKey={k}
              stroke={`var(--color-${k})`}
              strokeWidth={2}
              dot={false}
              hide={series.isHidden(k)}
            />
          ))}
          <ChartLegend
            content={({ payload }) => (
              <div className="flex flex-wrap gap-3 mt-2 text-xs">
                {payload?.map((p) => (
                  <button
                    key={p.dataKey as string}
                    type="button"
                    onClick={() => series.toggle(p.dataKey as TokenKey)}
                    className={cn(
                      "flex items-center gap-1.5 cursor-pointer transition-opacity",
                      series.isHidden(p.dataKey as TokenKey) && "opacity-40 line-through",
                    )}
                  >
                    <span className="size-2 rounded-sm" style={{ background: p.color }} />
                    {p.value}
                  </button>
                ))}
              </div>
            )}
          />
        </LineChart>
      </ChartContainer>
      {allHidden && (
        <p className="mt-2 text-center text-xs text-muted-foreground">
          {t("chartAllHidden")}
        </p>
      )}
    </ChartCard>
  );
}

const COST_KEYS = ["input_cost", "output_cost"] as const;
type CostKey = (typeof COST_KEYS)[number];

function CostChart({ items, loading }: ChartProps) {
  const t = useTranslations("byok.stats");
  const series = useToggleSet(COST_KEYS);
  const config = {
    input_cost: { label: t("chartInputCost"), color: "var(--chart-1)" },
    output_cost: { label: t("chartOutputCost"), color: "var(--chart-2)" },
  } satisfies ChartConfig;
  const allHidden = COST_KEYS.every((k) => series.isHidden(k));

  return (
    <ChartCard
      title={t("chartCost")}
      sub="Cost (USD)"
      loading={loading}
      empty={items.length === 0}
      emptyHint={t("trendEmpty")}
    >
      <ChartContainer config={config} className="h-[260px] w-full">
        <LineChart data={items} accessibilityLayer>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="date" tickLine={false} axisLine={false} />
          <YAxis
            tickLine={false}
            axisLine={false}
            tickFormatter={formatMoneyCompact}
          />
          <ChartTooltip
            content={<ChartTooltipContent formatter={(v) => formatMoneyExact(Number(v))} />}
          />
          {COST_KEYS.map((k) => (
            <Line
              key={k}
              type="monotone"
              dataKey={k}
              stroke={`var(--color-${k})`}
              strokeWidth={2}
              dot={false}
              hide={series.isHidden(k)}
            />
          ))}
          <ChartLegend
            content={({ payload }) => (
              <div className="flex flex-wrap gap-3 mt-2 text-xs">
                {payload?.map((p) => (
                  <button
                    key={p.dataKey as string}
                    type="button"
                    onClick={() => series.toggle(p.dataKey as CostKey)}
                    className={cn(
                      "flex items-center gap-1.5 cursor-pointer transition-opacity",
                      series.isHidden(p.dataKey as CostKey) && "opacity-40 line-through",
                    )}
                  >
                    <span className="size-2 rounded-sm" style={{ background: p.color }} />
                    {p.value}
                  </button>
                ))}
              </div>
            )}
          />
        </LineChart>
      </ChartContainer>
      {allHidden && (
        <p className="mt-2 text-center text-xs text-muted-foreground">
          {t("chartAllHidden")}
        </p>
      )}
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
    <div className="grid grid-cols-3 gap-4">
      {charts.map((c) => (
        <c.Comp key={c.key} items={items} loading={loading} />
      ))}
    </div>
  );
}
