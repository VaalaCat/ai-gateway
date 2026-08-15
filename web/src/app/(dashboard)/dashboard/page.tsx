"use client";

import { Suspense, useMemo, useState } from "react";
import { useSearchParams, useRouter, usePathname } from "next/navigation";
import { useTranslations } from "next-intl";
import {
  useDashboard,
  useMarketShare,
  useMetricTrend,
  type LeaderRow,
  type SpeedRow,
  type MarketShareDim,
  type TrendMetric,
  type TrendStat,
  type ChartTopN,
} from "@/lib/api/dashboard";
import { useModelDistribution } from "@/lib/api/stats";
import { useObsRange } from "@/lib/hooks/use-obs-range";
import { useChartTopN } from "@/lib/hooks/use-chart-top-n";
import { useAuth } from "@/lib/auth";
import type { TimeBucket } from "@/lib/types/observability";
import { applySevenDayDefaultRange } from "@/lib/utils/observability-range";

import { ObservabilityHeader } from "@/components/business/observability-header";
import { PageLayoutSkeleton } from "@/components/layout/page-layout-skeleton";
import { MetricTrendChart, type TrendDim } from "@/components/business/metric-trend-chart";
import { DonutChart } from "@/components/business/donut-chart";
import { Leaderboard } from "@/components/business/leaderboard";
import { KpiGrid, type KpiItem } from "@/components/business/kpi-grid";
import { EntityPicker } from "@/components/business/entity-picker/entity-picker";
import { ModelName } from "@/components/business/model-name";
import {
  SpeedRanking,
  type SpeedRankingEntity,
} from "@/components/business/speed-ranking";
import { MarketShareChart, type MarketShareMode } from "@/components/business/market-share-chart";
import { ChartOptionSelect } from "@/components/business/chart-option-select";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  formatAvgPercentile,
  formatDuration,
  formatMoneyCompact,
  formatTokensCompact,
  UNIT_QUOTA_SCALE,
} from "@/lib/utils/format";

export default function DashboardPage() {
  const t = useTranslations("dashboard");
  return (
    <Suspense
      fallback={<PageLayoutSkeleton title={t("title")} description={t("description")} />}
    >
      <DashboardPageContent />
    </Suspense>
  );
}

function DashboardPageContent() {
  const t = useTranslations("dashboard");
  const tc = useTranslations("common");
  const { user, isAdmin } = useAuth();
  const tcf = useTranslations("charts");
  const searchParams = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();
  const model = searchParams.get("model") ?? "";
  const userId = searchParams.get("user_id") ?? "";
  const selectedUserId = isAdmin && userId ? Number(userId) : undefined;
  const [topN, setTopN] = useChartTopN(user?.user_id ?? 0, pathname);
  const setParam = (key: string, value: string) => {
    const sp = new URLSearchParams(searchParams.toString());
    if (value) sp.set(key, value);
    else sp.delete(key);
    router.replace(`${pathname}?${sp.toString()}`, { scroll: false });
  };
  const setModel = (v: string) => setParam("model", v);
  const setUserId = (v: string) => setParam("user_id", v);
  const { range: rawRange, setRange, refresh, refreshKey } = useObsRange({
    gran: "day",
  });
  const hasExplicitStart = searchParams.has("start");
  const range = useMemo(
    () => applySevenDayDefaultRange(rawRange, hasExplicitStart),
    [rawRange, hasExplicitStart],
  );
  const { data, isFetching, refetch } = useDashboard(
    {
      ...range,
      top_n: topN,
      ...(model ? { model } : {}),
      ...(selectedUserId ? { user_id: selectedUserId } : {}),
    },
    { refetchKey: refreshKey },
  );

  const kpis = data?.kpis;
  const quota = !isAdmin ? kpis?.quota : undefined;

  const [marketShareDim, setMarketShareDim] = useState<MarketShareDim>("model");
  const [marketShareMode, setMarketShareMode] = useState<MarketShareMode>("percent");
  const [speedRankingDimension, setSpeedRankingDimension] =
    useState<SpeedRankingEntity>("model");
  const marketShare = useMarketShare(marketShareDim, range.start, range.end, {
    gran: range.gran,
    ...(model ? { model } : {}),
    top_n: topN,
    enabled: isAdmin,
  });

  const modelDistribution = useModelDistribution(
    {
      ...range,
      ...(model ? { model } : {}),
      ...(selectedUserId ? { user_id: selectedUserId } : {}),
      top_n: topN,
    },
    { enabled: isAdmin && !model },
  );

  // 趋势图 metric/dim 提升到页面级受控 state:dim 决定是否要按 model/channel 拆多线；
  // 普通用户由后端锁定 self scope，只有管理员可以附加 URL 中选中的 user_id。
  const [trendMetric, setTrendMetric] = useState<TrendMetric>("tokens");
  const [trendStat, setTrendStat] = useState<TrendStat>("avg");
  const [trendDim, setTrendDim] = useState<TrendDim>("off");
  const metricTrend = useMetricTrend(
    trendMetric,
    trendDim === "channel" ? "channel" : "model",
    range.start,
    range.end,
    {
      gran: range.gran,
      top_n: topN,
      ...((trendMetric === "ttft" || trendMetric === "tps") ? { stat: trendStat } : {}),
      ...(model ? { model } : {}),
      ...(selectedUserId ? { user_id: selectedUserId } : {}),
      enabled: trendDim !== "off",
    },
  );

  const changeTrendMetric = (metric: TrendMetric) => {
    setTrendMetric(metric);
    setTrendStat("avg");
  };
  const changeTrendStat = (stat: TrendStat) => {
    setTrendStat(stat);
    if (stat !== "avg") setTrendDim("model");
  };
  const changeTrendDim = (dim: TrendDim) => {
    setTrendDim(dim);
    if (dim !== "model") setTrendStat("avg");
  };

  const chartBuckets = useMemo<TimeBucket[]>(() => {
    const performanceByTs = new Map(
      (data?.log_metrics?.trend.buckets ?? []).map((bucket) => [bucket.ts, bucket]),
    );
    return (data?.trend.buckets ?? []).map((bucket) => ({
      ...bucket,
      ...performanceByTs.get(bucket.ts),
    }));
  }, [data]);
  const logUnavailable = data?.data_status.log_db === "unavailable";
  const availableMetrics = [
    ...(data?.trend.metrics ?? []),
    // behavior change: keep performance controls reachable so the inline
    // unavailable state can explain an offline log database.
    ...(data?.log_metrics?.trend.metrics ?? (logUnavailable ? ["ttft", "tps", "cache_hit_rate"] : [])),
  ];
  const leaderboard = data?.log_metrics?.leaderboard;
  const speedCompare = data?.log_metrics?.speed_compare;
  const speedRankingRows = speedRankingDimension === "model"
    ? speedCompare?.by_model ?? []
    : speedCompare?.by_channel ?? [];
  const expectedTrendStat = trendMetric === "ttft" || trendMetric === "tps"
    ? trendStat
    : trendMetric === "cache_hit_rate" ? "ratio" : "sum";
  const groupedTrend = metricTrend.data?.metric === trendMetric
    && metricTrend.data.stat === expectedTrendStat
    ? metricTrend.data
    : undefined;
  const groupedOnly = (trendMetric === "ttft" || trendMetric === "tps") && trendStat !== "avg";

  const handleRefresh = () => {
    refresh();
    refetch();
    marketShare.refetch();
    if (trendDim !== "off") metricTrend.refetch();
  };

  return (
    <div className="space-y-6">
      <ObservabilityHeader
        title={t("title")}
        subtitle={t("description")}
        range={range}
        onRangeChange={setRange}
        onRefresh={handleRefresh}
        refreshing={isFetching}
        showGranularity
        scopeLabel={tc("filters")}
        scopeControls={
          <>
            <EntityPicker
              entity="model"
              value={model}
              onChange={setModel}
              placeholder={tcf("filter.model")}
              className="w-full [&_[data-slot=button]]:h-9 sm:w-40 sm:[&_[data-slot=button]]:h-8"
              size="sm"
            />
            {isAdmin && (
              <EntityPicker
                entity="user"
                value={userId}
                onChange={setUserId}
                placeholder={tcf("filter.user")}
                className="w-full [&_[data-slot=button]]:h-9 sm:w-40 sm:[&_[data-slot=button]]:h-8"
                size="sm"
              />
            )}
            <ChartOptionSelect
              value={String(topN) as "5" | "10" | "20"}
              onValueChange={(value) => setTopN(Number(value) as ChartTopN)}
              label={tcf("prefix.topN")}
              options={[
                { value: "5", label: "5" },
                { value: "10", label: "10" },
                { value: "20", label: "20" },
              ]}
              className="h-9 w-full sm:h-8 sm:w-auto"
            />
          </>
        }
      />

      {(() => {
        if (!kpis) return null;
        const items: KpiItem[] = [
          {
            key: "requests",
            label: t("kpi.requests"),
            value: kpis.requests.value,
            ...(kpis.requests.spark ? { spark: kpis.requests.spark } : {}),
          },
          {
            key: "cost",
            label: t("kpi.cost"),
            value: formatMoneyCompact(kpis.cost.value),
            ...(kpis.cost.spark ? { spark: kpis.cost.spark } : {}),
          },
          {
            key: "tokens",
            label: t("kpi.tokens"),
            value: formatTokensCompact(kpis.tokens.value),
            ...(kpis.tokens.spark ? { spark: kpis.tokens.spark } : {}),
          },
        ];

        if (isAdmin) {
          if (!userId) {
            items.push({
              key: "users",
              label: t("kpi.users"),
              value: kpis.users?.value ?? 0,
            });
          }
          const succ = kpis.success_rate?.value;
          const reqs = kpis.requests?.value ?? 0;
          const successPct = succ !== undefined && reqs > 0
            ? Math.min(succ / reqs, 1) * 100
            : undefined;
          items.push({
            key: "successRate",
            label: t("kpi.successRate"),
            value: successPct === undefined ? "—" : `${successPct.toFixed(1)}%`,
            ...(successPct === undefined ? {} : {
              ratio: 100 - successPct,
              threshold: { warn: 5, critical: 10 },
            }),
          });
        }

        if (quota) {
          const pct =
            quota.quota > 0
              ? Math.min(100, ((quota.used_quota || 0) / quota.quota) * 100)
              : 0;
          items.push({
            key: "quota",
            label: t("kpi.quota"),
            value: `${((quota.used_quota || 0) / UNIT_QUOTA_SCALE).toFixed(4)} / ${((quota.quota || 0) / UNIT_QUOTA_SCALE).toFixed(4)}`,
            progress: pct,
            threshold: { warn: 80, critical: 95 },
          });
        }

        return <KpiGrid items={items} />;
      })()}

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        <div
          className={
            isAdmin && (modelDistribution.data?.buckets.length ?? 0) > 0 && !model
              ? "lg:col-span-2"
              : "lg:col-span-3"
          }
        >
          <MetricTrendChart
            buckets={chartBuckets}
            availableMetrics={availableMetrics}
            metric={trendMetric}
            onMetricChange={changeTrendMetric}
            dim={trendDim}
            onDimChange={changeTrendDim}
            grouped={groupedTrend}
            groupedOnly={groupedOnly}
            logUnavailable={logUnavailable}
            loading={groupedOnly && metricTrend.isLoading}
            title={t("trendCard.title")}
            displayExtra={
              trendMetric === "ttft" || trendMetric === "tps" ? (
                <ChartOptionSelect
                  value={trendStat}
                  onValueChange={changeTrendStat}
                  label={tcf("prefix.stat")}
                  options={trendMetric === "ttft"
                    ? [
                        { value: "avg", label: tcf("stat.avg") },
                        { value: "p95", label: tcf("stat.p95") },
                      ]
                    : [
                        { value: "avg", label: tcf("stat.avg") },
                        { value: "p5", label: tcf("stat.p5") },
                      ]}
                />
              ) : undefined
            }
          />
        </div>
        {isAdmin &&
          modelDistribution.data &&
          modelDistribution.data.buckets.length > 0 &&
          !model && (
            <DonutChart
              slices={modelDistribution.data.buckets}
              title={t("modelDist.title")}
              topN={topN}
              othersLabel={tcf("trend.others")}
              legendLabel={tcf("legend.series")}
            />
          )}
      </div>

      {isAdmin && (
        <MarketShareChart
          buckets={marketShare.data?.buckets ?? []}
          seriesOrder={marketShare.data?.series_order ?? []}
          mode={marketShareMode}
          onModeChange={setMarketShareMode}
          dim={marketShareDim}
          onDimChange={setMarketShareDim}
          loading={marketShare.isLoading}
        />
      )}

      {isAdmin && speedCompare && !userId && (
        <>
          <div className="flex flex-wrap items-center gap-3">
            <span className="text-sm text-muted-foreground">
              {t("speedRanking.dimension")}
            </span>
            <Tabs
              value={speedRankingDimension}
              onValueChange={(value) => {
                if (value === "model" || value === "channel") {
                  setSpeedRankingDimension(value);
                }
              }}
            >
              <TabsList className="!h-8" aria-label={t("speedRanking.dimension")}>
                <TabsTrigger value="model">{t("speedRanking.model")}</TabsTrigger>
                <TabsTrigger value="channel">{t("speedRanking.channel")}</TabsTrigger>
              </TabsList>
            </Tabs>
          </div>
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
            <SpeedRanking
              rows={speedRankingRows}
              entity={speedRankingDimension}
              metric="ttft"
              title={t(speedRankingDimension === "model"
                ? "speedRanking.ttftModelTitle"
                : "speedRanking.ttftChannelTitle")}
              rankLabel={t("speedRanking.rank")}
              nameLabel={t(`speedRanking.${speedRankingDimension}`)}
              valueLabel={t("speedRanking.ttftP95")}
              emptyText={t("speedRanking.empty")}
              topN={topN}
            />
            <SpeedRanking
              rows={speedRankingRows}
              entity={speedRankingDimension}
              metric="tps"
              title={t(speedRankingDimension === "model"
                ? "speedRanking.tpsModelTitle"
                : "speedRanking.tpsChannelTitle")}
              rankLabel={t("speedRanking.rank")}
              nameLabel={t(`speedRanking.${speedRankingDimension}`)}
              valueLabel={t("speedRanking.tpsP5")}
              emptyText={t("speedRanking.empty")}
              topN={topN}
            />
          </div>
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
            {!model && (
              <Leaderboard<SpeedRow>
                title={t("speed.modelTitle")}
                rows={speedCompare.by_model}
                columns={[
                  {
                    key: "name",
                    label: t("speed.col.name"),
                    render: (r) => <ModelName name={r.name} />,
                  },
                  {
                    key: "ttft_ms",
                    label: t("speed.col.ttft"),
                    render: (r) => formatAvgPercentile(r.ttft_ms, r.ttft_p95_ms, formatDuration),
                  },
                  {
                    key: "tps",
                    label: t("speed.col.tps"),
                    render: (r) => formatAvgPercentile(r.tps, r.tps_p5, (v) => v.toFixed(1)),
                  },
                ]}
              />
            )}
            <Leaderboard<SpeedRow>
              title={t("speed.channelTitle")}
              rows={speedCompare.by_channel}
              columns={[
                { key: "name", label: t("speed.col.name") },
                {
                  key: "ttft_ms",
                  label: t("speed.col.ttft"),
                  render: (r) => formatAvgPercentile(r.ttft_ms, r.ttft_p95_ms, formatDuration),
                },
                {
                  key: "tps",
                  label: t("speed.col.tps"),
                  render: (r) => formatAvgPercentile(r.tps, r.tps_p5, (v) => v.toFixed(1)),
                },
              ]}
            />
          </div>
        </>
      )}

      {isAdmin && leaderboard && (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          {!userId && (
            <Leaderboard<LeaderRow>
              title={t("leaderboard.byUsers")}
              rows={leaderboard.users}
              columns={[
                { key: "name", label: t("leaderboard.col.name") },
                {
                  key: "tokens",
                  label: t("leaderboard.col.tokens"),
                  render: (r) => formatTokensCompact(r.tokens),
                },
                { key: "requests", label: t("leaderboard.col.requests") },
                {
                  key: "cost",
                  label: t("leaderboard.col.cost"),
                  render: (r) => formatMoneyCompact(r.cost),
                  className: "text-muted-foreground",
                },
              ]}
            />
          )}
          <Leaderboard<LeaderRow>
            title={t("leaderboard.byModels")}
            rows={leaderboard.models}
            columns={[
              {
                key: "name",
                label: t("leaderboard.col.name"),
                render: (r) => <ModelName name={r.name} />,
              },
              {
                key: "tokens",
                label: t("leaderboard.col.tokens"),
                render: (r) => formatTokensCompact(r.tokens),
              },
              { key: "requests", label: t("leaderboard.col.requests") },
              {
                key: "cost",
                label: t("leaderboard.col.cost"),
                render: (r) => formatMoneyCompact(r.cost),
                className: "text-muted-foreground",
              },
            ]}
          />
          <Leaderboard<LeaderRow>
            title={t("leaderboard.byChannels")}
            rows={leaderboard.channels}
            columns={[
              { key: "name", label: t("leaderboard.col.name") },
              {
                key: "tokens",
                label: t("leaderboard.col.tokens"),
                render: (r) => formatTokensCompact(r.tokens),
              },
              { key: "requests", label: t("leaderboard.col.requests") },
              {
                key: "cost",
                label: t("leaderboard.col.cost"),
                render: (r) => formatMoneyCompact(r.cost),
                className: "text-muted-foreground",
              },
            ]}
          />
        </div>
      )}
    </div>
  );
}
