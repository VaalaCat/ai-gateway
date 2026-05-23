"use client";

import Link from "next/link";
import { Suspense, useEffect, useMemo, useState } from "react";
import { useTranslations } from "next-intl";
import { ColumnDef } from "@tanstack/react-table";
import { toast } from "sonner";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DateCell } from "@/components/business/date-cell";
import { ObservabilityHeader } from "@/components/business/observability-header";
import { RebuildButton } from "@/components/business/rebuild-button";
import { RebuildDialog } from "@/components/business/rebuild-dialog";
import { StackedAreaChart } from "@/components/business/stacked-area-chart";
import { KpiGrid } from "@/components/business/kpi-grid";
import { DataGlyph } from "@/components/business/data-glyph";
import { normalize0to100 } from "@/lib/utils/normalize";
import {
  useBillingOverview,
  useChannelBilling,
  useTokenBilling,
} from "@/lib/api/billing";
import { useBillingInsights } from "@/lib/api/billing-insights";
import { useChannelTypes } from "@/lib/api/channels";
import { buildQuery } from "@/lib/api/client";
import { useAuth } from "@/lib/auth";
import { useObsRange } from "@/lib/hooks/use-obs-range";
import { tsToDateStr } from "@/lib/utils/date-range";
import { PAGE_SIZES } from "@/lib/constants";
import { formatMoneyCompact, formatMoneyExact, formatSuccessRate } from "@/lib/utils/format";
import { MoneyCell } from "@/components/business/money-cell";
import { TokensCell } from "@/components/business/tokens-cell";
import type {
  BillingChannelRow,
  BillingOverviewResponse,
  BillingTokenRow,
} from "@/lib/types";

function logHref(params: Record<string, string | number | undefined>) {
  return `/logs${buildQuery(params)}`;
}

export default function BillingPage() {
  return (
    <Suspense
      fallback={
        <div className="flex items-center justify-center py-12 text-muted-foreground">
          Loading...
        </div>
      }
    >
      <BillingPageContent />
    </Suspense>
  );
}

function BillingPageContent() {
  const t = useTranslations("billing");
  const tc = useTranslations("common");
  const { isAdmin, loading } = useAuth();

  const [tab, setTab] = useState("token");
  const [userId, setUserId] = useState("");
  const [channelId, setChannelId] = useState("");
  const [rebuildOpen, setRebuildOpen] = useState(false);

  const [tokenPage, setTokenPage] = useState(1);
  const [tokenPageSize, setTokenPageSize] = useState<number>(PAGE_SIZES.DEFAULT);
  const [channelPage, setChannelPage] = useState(1);
  const [channelPageSize, setChannelPageSize] = useState<number>(
    PAGE_SIZES.DEFAULT
  );

  // 统一时间窗 + gran (day/hour) 控制所有数据源 (KPI / trend / token-list / channel-list).
  // useObsRange 默认 24h, 24h 配 gran=day 会出"1 个点", 这里仅在 URL 没显式 start 时
  // 把窗口拉成 7 天 (gran 保留 useObsRange 给的, 用户可切 hour)。
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

  const startDateStr = tsToDateStr(range.start);
  const endDateStr = tsToDateStr(range.end);

  const insights = useBillingInsights(range, {
    enabled: !loading,
    refetchKey: refreshKey,
  });

  const tokenUserId = userId ? Number(userId) : undefined;
  const channelFilterId = channelId ? Number(channelId) : undefined;

  const overview = useBillingOverview(
    {
      start_date: startDateStr,
      end_date: endDateStr,
      ...(tokenUserId ? { user_id: tokenUserId } : {}),
    },
    { enabled: !loading }
  );
  const tokenBilling = useTokenBilling(
    {
      page: tokenPage,
      page_size: tokenPageSize,
      start_date: startDateStr,
      end_date: endDateStr,
      ...(tokenUserId ? { user_id: tokenUserId } : {}),
    },
    { enabled: !loading }
  );
  const channelBilling = useChannelBilling(
    {
      page: channelPage,
      page_size: channelPageSize,
      start_date: startDateStr,
      end_date: endDateStr,
      ...(channelFilterId ? { channel_id: channelFilterId } : {}),
    },
    { enabled: !loading && isAdmin && tab === "channel" }
  );
  const channelTypes = useChannelTypes({ enabled: isAdmin });

  useEffect(() => {
    if (overview.isError) toast.error(tc("error"));
  }, [overview.isError, tc]);
  useEffect(() => {
    if (tokenBilling.isError) toast.error(tc("error"));
  }, [tokenBilling.isError, tc]);
  useEffect(() => {
    if (channelBilling.isError) toast.error(tc("error"));
  }, [channelBilling.isError, tc]);

  const channelTypeMap = useMemo(() => {
    const map = new Map<number, string>();
    for (const item of channelTypes.data ?? []) {
      map.set(item.id, item.name);
    }
    return map;
  }, [channelTypes.data]);

  const tokenColumns = useMemo<ColumnDef<BillingTokenRow>[]>(() => {
    const cols: ColumnDef<BillingTokenRow>[] = [
      {
        accessorKey: "token_name",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("token")} />
        ),
      },
      {
        accessorKey: "token_id",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("tokenId")} />
        ),
      },
    ];

    if (isAdmin) {
      cols.push({
        accessorKey: "user_id",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("user")} />
        ),
      });
    }

    cols.push(
      {
        accessorKey: "total_cost",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("totalCost")} />
        ),
        cell: ({ row }) => <MoneyCell quota={row.original.total_cost} />,
      },
      {
        accessorKey: "request_count",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("requestCount")} />
        ),
      },
      {
        id: "success_rate",
        header: t("successRate"),
        cell: ({ row }) =>
          formatSuccessRate(
            row.original.success_count,
            row.original.request_count
          ),
      },
      {
        accessorKey: "prompt_tokens",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("promptTokens")} />
        ),
        cell: ({ row }) => <TokensCell tokens={row.original.prompt_tokens} />,
      },
      {
        accessorKey: "completion_tokens",
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={t("completionTokens")}
          />
        ),
        cell: ({ row }) => <TokensCell tokens={row.original.completion_tokens} />,
      },
      {
        accessorKey: "last_used_at",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("lastUsedAt")} />
        ),
        cell: ({ row }) => <DateCell timestamp={row.original.last_used_at} />,
      },
      {
        id: "spark_24h",
        header: t("spark24h"),
        cell: ({ row }) => (
          <DataGlyph kind="line" values={normalize0to100(row.original.spark_24h ?? [])} title="24h"
            targetByBreakpoint={{ xs: 8, "sm-md": 12, "lg+": 20 }} />
        ),
      },
      {
        id: "logs",
        header: t("viewLogs"),
        cell: ({ row }) => (
          <Button variant="outline" size="sm" asChild>
            <Link
              href={logHref({
                token_id: row.original.token_id,
                ...(isAdmin ? { user_id: row.original.user_id } : {}),
              })}
            >
              {t("viewLogs")}
            </Link>
          </Button>
        ),
        enableHiding: false,
      }
    );

    return cols;
  }, [isAdmin, t]);

  const channelColumns = useMemo<ColumnDef<BillingChannelRow>[]>(
    () => [
      {
        accessorKey: "channel_name",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("channel")} />
        ),
      },
      {
        accessorKey: "channel_id",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("channelId")} />
        ),
      },
      {
        accessorKey: "channel_type",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("channelType")} />
        ),
        cell: ({ row }) =>
          channelTypeMap.get(row.original.channel_type) ??
          String(row.original.channel_type),
      },
      {
        accessorKey: "total_cost",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("totalCost")} />
        ),
        cell: ({ row }) => <MoneyCell quota={row.original.total_cost} />,
      },
      {
        accessorKey: "request_count",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("requestCount")} />
        ),
      },
      {
        id: "success_rate",
        header: t("successRate"),
        cell: ({ row }) =>
          formatSuccessRate(
            row.original.success_count,
            row.original.request_count
          ),
      },
      {
        accessorKey: "prompt_tokens",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("promptTokens")} />
        ),
        cell: ({ row }) => <TokensCell tokens={row.original.prompt_tokens} />,
      },
      {
        accessorKey: "completion_tokens",
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={t("completionTokens")}
          />
        ),
        cell: ({ row }) => <TokensCell tokens={row.original.completion_tokens} />,
      },
      {
        accessorKey: "last_used_at",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("lastUsedAt")} />
        ),
        cell: ({ row }) => <DateCell timestamp={row.original.last_used_at} />,
      },
      {
        id: "spark_24h",
        header: t("spark24h"),
        cell: ({ row }) => (
          <DataGlyph kind="line" values={normalize0to100(row.original.spark_24h ?? [])} title="24h"
            targetByBreakpoint={{ xs: 8, "sm-md": 12, "lg+": 20 }} />
        ),
      },
      {
        id: "logs",
        header: t("viewLogs"),
        cell: ({ row }) => (
          <Button variant="outline" size="sm" asChild>
            <Link href={logHref({ channel_id: row.original.channel_id })}>
              {t("viewLogs")}
            </Link>
          </Button>
        ),
        enableHiding: false,
      },
    ],
    [channelTypeMap, t]
  );

  const tokenTotal = tokenBilling.data?.total ?? 0;
  const tokenPageCount = Math.ceil(tokenTotal / tokenPageSize) || 1;
  const channelTotal = channelBilling.data?.total ?? 0;
  const channelPageCount = Math.ceil(channelTotal / channelPageSize) || 1;

  const overviewValue: BillingOverviewResponse | undefined = overview.data;

  if (loading) {
    return (
      <div className="py-12 text-center text-muted-foreground">
        {tc("loading")}
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <ObservabilityHeader
        title={t("title")}
        subtitle={t("description")}
        range={range}
        onRangeChange={setRange}
        onRefresh={refresh}
        refreshing={insights.isFetching || overview.isFetching}
        showGranularity
      />
      {isAdmin && (
        <div className="flex justify-end">
          <RebuildButton onClick={() => setRebuildOpen(true)} />
        </div>
      )}

      {(() => {
        const noData = !overviewValue || (overviewValue.request_count ?? 0) === 0;
        const successPct = (overviewValue?.success_rate ?? 0) * 100;
        const errorPct = 100 - successPct;
        const cacheHitPct = (insights.data?.cache_saving?.hit_ratio ?? 0) * 100;
        const savedTokens = insights.data?.cache_saving?.saved_tokens ?? 0;
        const cacheReadTokens = insights.data?.cache_saving?.read_tokens ?? 0;
        const cacheWriteTokens = insights.data?.cache_saving?.write_tokens ?? 0;
        return (
          <KpiGrid
            items={[
              {
                key: "totalCost",
                label: t("totalCost"),
                value: formatMoneyCompact(overviewValue?.total_cost ?? 0),
              },
              {
                key: "requestCount",
                label: t("requestCount"),
                value: overviewValue?.request_count ?? 0,
              },
              {
                key: "successRate",
                label: t("successRate"),
                value: noData ? "—" : `${successPct.toFixed(1)}%`,
                ratio: noData ? undefined : errorPct,
                threshold: noData ? undefined : { warn: 5, critical: 10 },
              },
              {
                key: "activeTokens",
                label: t("activeTokens"),
                value: overviewValue?.active_tokens ?? 0,
              },
              {
                key: "cacheHit",
                label: t("kpi.cacheHit"),
                value: noData ? "—" : `${cacheHitPct.toFixed(1)}%`,
                sublabel: t("kpi.cacheSubFull", {
                  n: savedTokens.toLocaleString(),
                  r: cacheReadTokens.toLocaleString(),
                  w: cacheWriteTokens.toLocaleString(),
                }),
                ratio: noData ? undefined : cacheHitPct,
              },
            ]}
          />
        );
      })()}

      <StackedAreaChart
        buckets={insights.data?.cost_trend_stacked.buckets ?? []}
        seriesOrder={insights.data?.cost_trend_stacked.series_order ?? []}
        title={t("costTrend")}
        loading={insights.isLoading}
        axisFormatter={formatMoneyCompact}
        tooltipFormatter={formatMoneyExact}
        unitLabel="Cost (USD)"
      />

      <div className="flex flex-wrap items-end gap-3 rounded-lg border p-4">
        {isAdmin && tab === "token" && (
          <div className="space-y-1">
            <Label>{t("user")}</Label>
            <Input
              type="number"
              placeholder={t("user")}
              value={userId}
              onChange={(e) => {
                setUserId(e.target.value);
                setTokenPage(1);
              }}
            />
          </div>
        )}
        {isAdmin && tab === "channel" && (
          <div className="space-y-1">
            <Label>{t("channelId")}</Label>
            <Input
              type="number"
              placeholder={t("channelId")}
              value={channelId}
              onChange={(e) => {
                setChannelId(e.target.value);
                setChannelPage(1);
              }}
            />
          </div>
        )}
      </div>

      {isAdmin ? (
        <Tabs value={tab} onValueChange={setTab}>
          <TabsList>
            <TabsTrigger value="token">{t("byToken")}</TabsTrigger>
            <TabsTrigger value="channel">{t("byChannel")}</TabsTrigger>
          </TabsList>
          <TabsContent value="token" className="space-y-4">
            <DataTable
              columns={tokenColumns}
              data={tokenBilling.data?.data ?? []}
              loading={tokenBilling.isLoading}
              total={tokenTotal}
              page={tokenPage}
              pageSize={tokenPageSize}
              pageCount={tokenPageCount}
              onPaginationChange={(nextPage, nextPageSize) => {
                if (nextPageSize !== tokenPageSize) {
                  setTokenPage(1);
                  setTokenPageSize(nextPageSize);
                  return;
                }
                setTokenPage(nextPage);
              }}
            />
          </TabsContent>
          <TabsContent value="channel" className="space-y-4">
            <DataTable
              columns={channelColumns}
              data={channelBilling.data?.data ?? []}
              loading={channelBilling.isLoading}
              total={channelTotal}
              page={channelPage}
              pageSize={channelPageSize}
              pageCount={channelPageCount}
              onPaginationChange={(nextPage, nextPageSize) => {
                if (nextPageSize !== channelPageSize) {
                  setChannelPage(1);
                  setChannelPageSize(nextPageSize);
                  return;
                }
                setChannelPage(nextPage);
              }}
            />
          </TabsContent>
        </Tabs>
      ) : (
        <DataTable
          columns={tokenColumns}
          data={tokenBilling.data?.data ?? []}
          loading={tokenBilling.isLoading}
          total={tokenTotal}
          page={tokenPage}
          pageSize={tokenPageSize}
          pageCount={tokenPageCount}
          onPaginationChange={(nextPage, nextPageSize) => {
            if (nextPageSize !== tokenPageSize) {
              setTokenPage(1);
              setTokenPageSize(nextPageSize);
              return;
            }
            setTokenPage(nextPage);
          }}
        />
      )}

      {isAdmin && (
        <RebuildDialog open={rebuildOpen} onOpenChange={setRebuildOpen} />
      )}
    </div>
  );
}
