"use client";

import { Suspense, useMemo } from "react";
import { useTranslations } from "next-intl";
import { useDashboard, type LeaderRow, type SpeedRow } from "@/lib/api/dashboard";
import { useObsRange } from "@/lib/hooks/use-obs-range";
import { useAuth } from "@/lib/auth";

import { ObservabilityHeader } from "@/components/business/observability-header";
import { TrendChart } from "@/components/business/trend-chart";
import { DonutChart } from "@/components/business/donut-chart";
import { Leaderboard } from "@/components/business/leaderboard";
import { KpiGrid, type KpiItem } from "@/components/business/kpi-grid";
import { formatDuration, formatMoneyCompact, formatTokensCompact, UNIT_QUOTA_SCALE } from "@/lib/utils/format";

export default function DashboardPage() {
  return (
    <Suspense
      fallback={
        <div className="flex items-center justify-center py-12 text-muted-foreground">
          Loading...
        </div>
      }
    >
      <DashboardPageContent />
    </Suspense>
  );
}

function DashboardPageContent() {
  const t = useTranslations("dashboard");
  const { isAdmin } = useAuth();
  const { range: rawRange, setRange, refresh, refreshKey } = useObsRange({
    gran: "day",
  });
  const range = useMemo(
    () =>
      rawRange.end - rawRange.start <= 86400
        ? { ...rawRange, start: rawRange.end - 7 * 86400 }
        : rawRange,
    [rawRange],
  );
  const { data, isFetching, refetch } = useDashboard(range, {
    refetchKey: refreshKey,
  });

  const kpis = data?.kpis;
  const quota = !isAdmin ? kpis?.quota : undefined;

  const handleRefresh = () => {
    refresh();
    refetch();
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
          items.push({
            key: "users",
            label: t("kpi.users"),
            value: kpis.users?.value ?? 0,
          });
          const succ = kpis.success_rate?.value ?? 0;
          const reqs = kpis.requests?.value ?? 0;
          const successPct = reqs > 0 ? Math.min(succ / reqs, 1) * 100 : 0;
          const errorPct = 100 - successPct;
          items.push({
            key: "successRate",
            label: t("kpi.successRate"),
            value: `${successPct.toFixed(1)}%`,
            ratio: errorPct,
            threshold: { warn: 5, critical: 10 },
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
            isAdmin && data?.model_distribution && data.model_distribution.length > 0
              ? "lg:col-span-2"
              : "lg:col-span-3"
          }
        >
          <TrendChart
            buckets={data?.trend.buckets ?? []}
            title={t("trendCard.title")}
          />
        </div>
        {isAdmin &&
          data?.model_distribution &&
          data.model_distribution.length > 0 && (
            <DonutChart
              slices={data.model_distribution}
              title={t("modelDist.title")}
              topN={5}
            />
          )}
      </div>

      {isAdmin && data?.speed_compare && (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <Leaderboard<SpeedRow>
            title={t("speed.modelTitle")}
            rows={data.speed_compare.by_model}
            columns={[
              { key: "name", label: "Name" },
              {
                key: "ttft_ms",
                label: "TTFT",
                render: (r) => formatDuration(r.ttft_ms),
              },
              {
                key: "tps",
                label: "TPS",
                render: (r) => r.tps.toFixed(1),
              },
            ]}
          />
          <Leaderboard<SpeedRow>
            title={t("speed.channelTitle")}
            rows={data.speed_compare.by_channel}
            columns={[
              { key: "name", label: "Name" },
              {
                key: "ttft_ms",
                label: "TTFT",
                render: (r) => formatDuration(r.ttft_ms),
              },
              {
                key: "tps",
                label: "TPS",
                render: (r) => r.tps.toFixed(1),
              },
            ]}
          />
        </div>
      )}

      {isAdmin && data?.leaderboard && (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <Leaderboard<LeaderRow>
            title={t("leaderboard.byUsers")}
            rows={data.leaderboard.users}
            columns={[
              { key: "name", label: "Name" },
              {
                key: "cost",
                label: "Cost",
                render: (r) => formatMoneyCompact(r.cost),
              },
              { key: "requests", label: "Reqs" },
            ]}
          />
          <Leaderboard<LeaderRow>
            title={t("leaderboard.byModels")}
            rows={data.leaderboard.models}
            columns={[
              { key: "name", label: "Name" },
              {
                key: "cost",
                label: "Cost",
                render: (r) => formatMoneyCompact(r.cost),
              },
              { key: "requests", label: "Reqs" },
            ]}
          />
          <Leaderboard<LeaderRow>
            title={t("leaderboard.byChannels")}
            rows={data.leaderboard.channels}
            columns={[
              { key: "name", label: "Name" },
              {
                key: "cost",
                label: "Cost",
                render: (r) => formatMoneyCompact(r.cost),
              },
              { key: "requests", label: "Reqs" },
            ]}
          />
        </div>
      )}
    </div>
  );
}
