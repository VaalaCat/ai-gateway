"use client";

import { Suspense } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";

import { BarChart, Bar, CartesianGrid, XAxis, YAxis } from "recharts";

import { ObservabilityHeader } from "@/components/business/observability-header";
import { KpiGrid } from "@/components/business/kpi-grid";
import { ChartCard } from "@/components/business/chart-card";
import { DataGlyph } from "@/components/business/data-glyph";
import { StageDistributionBar } from "@/components/business/stage-distribution-bar";
import { RELAY_STAGE_ORDER } from "@/lib/constants/relay/stages";
import { toStageBuckets } from "@/lib/utils/to-stage-buckets";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart";
import { normalize0to100 } from "@/lib/utils/normalize";
import { formatDuration } from "@/lib/utils/format";

import { useObsRange } from "@/lib/hooks/use-obs-range";
import { useMonitoringInsights } from "@/lib/api/monitoring-insights";

export default function MonitoringPage() {
  return (
    <Suspense
      fallback={
        <div className="py-12 text-center text-muted-foreground">Loading...</div>
      }
    >
      <Inner />
    </Suspense>
  );
}

function Inner() {
  const t = useTranslations("monitoring");
  const tStage = useTranslations("common.relayStage");
  const router = useRouter();
  const { range, setRange, refresh, refreshKey } = useObsRange();
  const { data, isFetching, refetch } = useMonitoringInsights(range, {
    refetchKey: refreshKey,
  });

  const totalRequests = Number(data?.kpi_rings.success.value ?? 0);
  const noData = !data || totalRequests === 0;
  const successRatioPct = (data?.kpi_rings.success.ratio ?? 0) * 100;

  const handleRefresh = () => {
    refresh();
    refetch();
  };

  const channels = data?.channels ?? [];
  const agents = data?.agents ?? [];

  return (
    <div className="space-y-6">
      <ObservabilityHeader
        title={t("title")}
        subtitle={t("subtitle")}
        range={range}
        onRangeChange={setRange}
        onRefresh={handleRefresh}
        refreshing={isFetching}
        showGranularity
      />

      {/* Row 1: 4 KPI */}
      <KpiGrid
        items={[
          {
            key: "success",
            label: t("ring.success"),
            value: noData ? "—" : `${successRatioPct.toFixed(1)}%`,
            sublabel: data?.kpi_rings.success.sub,
            ratio: noData ? undefined : successRatioPct,
          },
          {
            key: "agents",
            label: t("ring.agents"),
            value: String(data?.kpi_rings.agents.value ?? "0/0"),
            sublabel: data?.kpi_rings.agents.sub,
            progress: (data?.kpi_rings.agents.ratio ?? 0) * 100,
          },
          {
            key: "tps",
            label: t("ring.tps"),
            value: Number(data?.kpi_rings.tps.value ?? 0).toFixed(1),
            sublabel: data?.kpi_rings.tps.sub,
          },
          {
            key: "requests",
            label: t("ring.requests"),
            value: noData ? "—" : totalRequests.toLocaleString(),
            sublabel: data?.kpi_rings.success.sub,
          },
        ]}
      />

      {/* Channels */}
      <Card>
        <CardHeader>
          <CardTitle>{t("channels.title")}</CardTitle>
        </CardHeader>
        <CardContent>
          <Table className="text-body">
            <TableHeader>
              <TableRow className="text-muted-foreground">
                <TableHead>{t("channels.name")}</TableHead>
                <TableHead className="text-right">
                  {t("channels.requests")}
                </TableHead>
                <TableHead className="text-right">
                  {t("channels.errorRatio")}
                </TableHead>
                <TableHead className="text-right">
                  {t("channels.ttft")}
                </TableHead>
                <TableHead className="text-right">
                  {t("channels.tps")}
                </TableHead>
                <TableHead className="text-right">
                  {t("channels.p95")}
                </TableHead>
                <TableHead>{t("channels.spark24h")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {channels.map((c) => (
                <TableRow
                  key={c.id}
                  className="cursor-pointer hover:bg-accent"
                  onClick={() => router.push(`/logs?channel_id=${c.id}`)}
                >
                  <TableCell>{c.name || `#${c.id}`}</TableCell>
                  <TableCell className="text-right">{c.requests}</TableCell>
                  <TableCell className="text-right">
                    {(c.error_ratio * 100).toFixed(1)}%
                  </TableCell>
                  <TableCell className="text-right">
                    {c.ttft_p95_ms ? formatDuration(c.ttft_p95_ms) : "—"}
                  </TableCell>
                  <TableCell className="text-right">
                    {c.tps_avg ? c.tps_avg.toFixed(1) : "—"}
                  </TableCell>
                  <TableCell className="text-right">
                    {c.latency_p95_ms ? formatDuration(c.latency_p95_ms) : "—"}
                  </TableCell>
                  <TableCell>
                    <DataGlyph
                      kind="line"
                      values={normalize0to100(c.spark_24h ?? [])}
                      targetByBreakpoint={{ xs: 8, "sm-md": 12, "lg+": 20 }}
                    />
                  </TableCell>
                </TableRow>
              ))}
              {channels.length === 0 && (
                <TableRow>
                  <TableCell
                    colSpan={7}
                    className="py-4 text-center text-muted-foreground"
                  >
                    {t("noData")}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {/* Agents */}
      <Card>
        <CardHeader>
          <CardTitle>{t("agents.title")}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2">
          {agents.map((a) => (
            <div
              key={a.id}
              className="flex cursor-pointer flex-col gap-2 rounded border p-3 hover:bg-accent sm:flex-row sm:items-center sm:gap-3"
              onClick={() =>
                router.push(
                  `/monitoring/insight?type=agent&id=${encodeURIComponent(a.id)}`,
                )
              }
            >
              <span
                className={`size-2 rounded-full ${a.online ? "bg-emerald-500" : "bg-muted"}`}
              />
              <span className="w-full truncate font-medium sm:flex-1">
                {a.name || a.id}
              </span>
              <Badge variant={a.online ? "secondary" : "outline"}>
                {a.online ? t("agents.online") : t("agents.offline")}
              </Badge>
              <span className="text-sm text-muted-foreground">
                {a.requests} req
              </span>
              <span className="text-sm text-muted-foreground">
                {a.tps_avg ? a.tps_avg.toFixed(1) : "—"} tps
              </span>
              <DataGlyph
                kind="line"
                values={normalize0to100(a.spark_24h ?? [])}
                targetByBreakpoint={{ xs: 8, "sm-md": 12, "lg+": 20 }}
              />
            </div>
          ))}
          {agents.length === 0 && (
            <div className="py-4 text-center text-muted-foreground">
              {t("noData")}
            </div>
          )}
        </CardContent>
      </Card>

      {/* Errors: stage 紧凑流程条占一行,channel 详图占一行 */}
      <StageDistributionBar
        title={t("errors.byStage")}
        loading={isFetching}
        order={RELAY_STAGE_ORDER}
        data={toStageBuckets(data?.errors.by_stage, tStage)}
      />
      <ChartCard
        title={t("errors.byChannel")}
        loading={isFetching}
        empty={(data?.errors.by_channel ?? []).length === 0}
      >
        <ChartContainer
          config={{ count: { label: "Errors", color: "var(--chart-2)" } }}
          className="h-[280px] w-full"
        >
          <BarChart
            data={(data?.errors.by_channel ?? []).map((e) => ({
              name: e.name ?? `#${e.id ?? "?"}`,
              count: e.count,
            }))}
            layout="vertical"
            accessibilityLayer
            margin={{ left: 16, right: 16 }}
          >
            <CartesianGrid horizontal={false} />
            <XAxis type="number" tickLine={false} axisLine={false} />
            <YAxis
              type="category"
              dataKey="name"
              tickLine={false}
              axisLine={false}
              width={120}
            />
            <ChartTooltip content={<ChartTooltipContent />} />
            <Bar
              dataKey="count"
              fill="var(--color-count)"
              radius={[0, 4, 4, 0]}
            />
          </BarChart>
        </ChartContainer>
      </ChartCard>

    </div>
  );
}
