"use client";

import Link from "next/link";
import { Suspense, useEffect, useMemo, useState } from "react";
import { useTranslations } from "next-intl";
import { useSearchParams, useRouter, usePathname } from "next/navigation";
import { ColumnDef, Row } from "@tanstack/react-table";
import { Ellipsis } from "lucide-react";
import { toast } from "sonner";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { DateCell } from "@/components/business/date-cell";
import { DebouncedSearchInput } from "@/components/business/debounced-search-input";
import { FilterBar, FilterField } from "@/components/business/filter-bar";
import { ObservabilityHeader } from "@/components/business/observability-header";
import { RebuildButton } from "@/components/business/rebuild-button";
import { RebuildDialog } from "@/components/business/rebuild-dialog";
import { MetricTrendChart } from "@/components/business/metric-trend-chart";
import { KpiGrid } from "@/components/business/kpi-grid";
import { DataGlyph } from "@/components/business/data-glyph";
import { normalize0to100 } from "@/lib/utils/normalize";
import {
  useBillingOverview,
  useChannelBilling,
  useRebuildBillingJobs,
  useTokenBilling,
} from "@/lib/api/billing";
import { useBillingInsights } from "@/lib/api/billing-insights";
import type { ChartTopN } from "@/lib/api/dashboard";
import { useChannelTypes } from "@/lib/api/channels";
import { buildQuery } from "@/lib/api/client";
import { useAuth } from "@/lib/auth";
import { useObsRange } from "@/lib/hooks/use-obs-range";
import { useChartTopN } from "@/lib/hooks/use-chart-top-n";
import { tsToDateStr } from "@/lib/utils/date-range";
import { applySevenDayDefaultRange } from "@/lib/utils/observability-range";
import { PAGE_SIZES } from "@/lib/constants";
import { formatMoneyCompact, formatSuccessRate, formatTokensCompact } from "@/lib/utils/format";
import { MoneyCell } from "@/components/business/money-cell";
import { EntityLabel } from "@/components/business/entity-label";
import { EntityPicker } from "@/components/business/entity-picker/entity-picker";
import { buildTokenBreakdownColumns } from "@/components/business/token-breakdown-columns";
import { ChannelModelBreakdown } from "@/components/business/channel-model-breakdown";
import { ChartOptionSelect } from "@/components/business/chart-option-select";
import { PageLayoutSkeleton } from "@/components/layout/page-layout-skeleton";
import type {
  BillingChannelRow,
  BillingOverviewResponse,
  BillingTokenRow,
} from "@/lib/types";

function logHref(params: Record<string, string | number | undefined>) {
  return `/logs${buildQuery(params)}`;
}

function BillingPageSkeleton() {
  const t = useTranslations("billing");

  return (
    <PageLayoutSkeleton
      title={t("title")}
      description={t("description")}
      maxWidth="full"
    />
  );
}

export default function BillingPage() {
  return (
    <Suspense fallback={<BillingPageSkeleton />}>
      <BillingPageContent />
    </Suspense>
  );
}

function BillingPageContent() {
  const t = useTranslations("billing");
  const tc = useTranslations("common");
  const tcf = useTranslations("charts");
  const { user, isAdmin, loading } = useAuth();

  const [tab, setTab] = useState("token");
  const [rebuildOpen, setRebuildOpen] = useState(false);
  const [headerMenuOpen, setHeaderMenuOpen] = useState(false);
  const rebuildJobs = useRebuildBillingJobs();
  const hasRunningRebuild =
    rebuildJobs.data?.jobs?.some((job) => job.status === "running") ?? false;

  const [tokenPage, setTokenPage] = useState(1);
  const [tokenPageSize, setTokenPageSize] = useState<number>(PAGE_SIZES.DEFAULT);
  const [channelPage, setChannelPage] = useState(1);
  const [channelPageSize, setChannelPageSize] = useState<number>(
    PAGE_SIZES.DEFAULT
  );

  const searchParams = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();
  const userId = searchParams.get("user_id") ?? "";
  const model = searchParams.get("model") ?? "";
  const channelId = searchParams.get("channel_id") ?? "";
  const channelType = searchParams.get("channel_type") ?? "";
  const search = searchParams.get("q") ?? "";
  const minTokens = searchParams.get("min_tokens") ?? "";
  const tokenId = searchParams.get("token_id") ?? "";
  const trendTokenId = searchParams.get("trend_token_id") ?? "";
  const selectedUserId = isAdmin && userId ? Number(userId) : undefined;
  const [topN, setTopN] = useChartTopN(user?.user_id ?? 0, pathname);
  const setParams = (updates: Record<string, string>) => {
    const sp = new URLSearchParams(searchParams.toString());
    for (const [key, value] of Object.entries(updates)) {
      if (value) sp.set(key, value);
      else sp.delete(key);
    }
    router.replace(`${pathname}?${sp.toString()}`, { scroll: false });
  };
  const setParam = (key: string, value: string) => setParams({ [key]: value });
  const setUserFilter = (v: string) => {
    setParams({ user_id: v, trend_token_id: "" });
    setTokenPage(1);
  };
  const setModel = (v: string) => setParam("model", v);
  const setChannelIdFilter = (v: string) => { setParam("channel_id", v); setChannelPage(1); };
  const setChannelType = (v: string) => { setParam("channel_type", v); setChannelPage(1); };
  const setSearch = (v: string) => { setParam("q", v); setTokenPage(1); setChannelPage(1); };
  const setMinTokens = (v: string) => { setParam("min_tokens", v); setTokenPage(1); setChannelPage(1); };
  const setTokenIdFilter = (v: string) => { setParam("token_id", v); setTokenPage(1); };
  const setTrendTokenId = (v: string) => setParam("trend_token_id", v);

  // 统一时间窗 + gran (day/hour) 控制所有数据源 (KPI / trend / token-list / channel-list).
  // useObsRange 默认 24h, 24h 配 gran=day 会出"1 个点", 这里仅在 URL 没显式 start 时
  // 把窗口拉成 7 天 (gran 保留 useObsRange 给的, 用户可切 hour)。
  const { range: rawRange, setRange, refresh, refreshKey } = useObsRange({
    gran: "day",
  });
  const hasExplicitStart = searchParams.has("start");
  const range = useMemo(
    () => applySevenDayDefaultRange(rawRange, hasExplicitStart),
    [rawRange, hasExplicitStart],
  );

  const startDateStr = tsToDateStr(range.start);
  const endDateStr = tsToDateStr(range.end);

  const insights = useBillingInsights(
    {
      from: range.start,
      to: range.end,
      gran: range.gran,
      ...(model ? { model } : {}),
      ...(selectedUserId ? { user_id: selectedUserId } : {}),
      ...(trendTokenId ? { token_id: Number(trendTokenId) } : {}),
      top_n: topN,
    },
    { enabled: !loading, refetchKey: refreshKey },
  );

  const tokenUserId = selectedUserId;
  const channelFilterId = channelId ? Number(channelId) : undefined;

  // 注意:model 筛选「只作用于趋势图」(useBillingInsights)。
  // 顶部 KPI 卡与下方 token/channel 表来自日账汇总表(token_daily_billings / channel_daily_billings),
  // 这些表没有 model_name 列,无法按模型拆;故它们只跟随 user 筛选,不接收 model。这是设计如此,勿"修复"。
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
      ...(tokenId ? { token_id: Number(tokenId) } : {}),
      ...(search ? { search } : {}),
      ...(minTokens ? { min_tokens: Number(minTokens) } : {}),
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
      ...(search ? { search } : {}),
      ...(channelType ? { channel_type: Number(channelType) } : {}),
      ...(minTokens ? { min_tokens: Number(minTokens) } : {}),
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
        cell: ({ row }) => <EntityLabel entity="user" id={row.original.user_id} />,
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
      ...buildTokenBreakdownColumns<BillingTokenRow>(t),
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
          <Button variant="outline" size="xs" asChild>
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
      ...buildTokenBreakdownColumns<BillingChannelRow>(t),
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
          <Button variant="outline" size="xs" asChild>
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

  const renderChannelExpandedRow = (row: Row<BillingChannelRow>) => (
    <ChannelModelBreakdown
      channelId={row.original.channel_id}
      start={range.start}
      end={range.end}
    />
  );

  const tokenTotal = tokenBilling.data?.total ?? 0;
  const tokenPageCount = Math.ceil(tokenTotal / tokenPageSize) || 1;
  const channelTotal = channelBilling.data?.total ?? 0;
  const channelPageCount = Math.ceil(channelTotal / channelPageSize) || 1;

  const overviewValue: BillingOverviewResponse | undefined = overview.data;

  const tokenFilters = (
    <FilterBar className="gap-2">
      <FilterField label={t("filterLabelToken")}>
        <EntityPicker
          entity="token"
          size="sm"
          value={tokenId}
          onChange={setTokenIdFilter}
          placeholder={t("filterTokenPick")}
          className="w-full [&_[data-slot=button]]:h-9 sm:w-40 sm:[&_[data-slot=button]]:h-8"
        />
      </FilterField>
      <FilterField label={t("filterLabelSearch")}>
        <DebouncedSearchInput
          value={search}
          onCommit={setSearch}
          placeholder={t("filterSearchToken")}
          className="h-9 w-full sm:h-8 sm:w-56"
        />
      </FilterField>
      <FilterField label={t("filterMinTokens")}>
        <Input
          type="number"
          min={0}
          value={minTokens}
          onChange={(e) => setMinTokens(e.target.value)}
          placeholder={t("filterMinTokens")}
          className="h-9 w-full sm:h-8 sm:w-36"
        />
      </FilterField>
    </FilterBar>
  );

  const channelFilters = (
    <FilterBar className="gap-2">
      <FilterField label={t("filterLabelChannel")}>
        <EntityPicker
          entity="channel"
          size="sm"
          value={channelId}
          onChange={setChannelIdFilter}
          placeholder={t("channelId")}
          className="w-full [&_[data-slot=button]]:h-9 sm:w-40 sm:[&_[data-slot=button]]:h-8"
        />
      </FilterField>
      <FilterField label={t("filterChannelType")}>
        <Select
          value={channelType || "all"}
          onValueChange={(v) => setChannelType(v === "all" ? "" : v)}
        >
          <SelectTrigger size="sm" className="h-9 w-full sm:h-8 sm:w-40">
            <SelectValue placeholder={t("filterAllTypes")} />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              <SelectItem value="all">{t("filterAllTypes")}</SelectItem>
              {(channelTypes.data ?? []).map((ct) => (
                <SelectItem key={ct.id} value={String(ct.id)}>
                  {ct.name}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      </FilterField>
      <FilterField label={t("filterLabelSearch")}>
        <DebouncedSearchInput
          value={search}
          onCommit={setSearch}
          placeholder={t("filterSearchChannel")}
          className="h-9 w-full sm:h-8 sm:w-56"
        />
      </FilterField>
      <FilterField label={t("filterMinTokens")}>
        <Input
          type="number"
          min={0}
          value={minTokens}
          onChange={(e) => setMinTokens(e.target.value)}
          placeholder={t("filterMinTokens")}
          className="h-9 w-full sm:h-8 sm:w-36"
        />
      </FilterField>
    </FilterBar>
  );

  if (loading) {
    return <BillingPageSkeleton />;
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
        scopeLabel={isAdmin ? tc("filters") : undefined}
        scopeControls={isAdmin ? (
          <EntityPicker
            entity="user"
            size="sm"
            value={userId}
            onChange={setUserFilter}
            placeholder={tcf("filter.user")}
            className="w-full [&_[data-slot=button]]:h-9 sm:w-40 sm:[&_[data-slot=button]]:h-8"
          />
        ) : undefined}
        headerActions={isAdmin ? (
          <DropdownMenu open={headerMenuOpen} onOpenChange={setHeaderMenuOpen}>
            <TooltipProvider>
              <Tooltip>
                <TooltipTrigger asChild>
                  <DropdownMenuTrigger asChild>
                    <Button
                      variant="outline"
                      size="icon-sm"
                      className="size-9 sm:size-8"
                      aria-label={tc("more")}
                      data-running={hasRunningRebuild || undefined}
                    >
                      <Ellipsis />
                      {hasRunningRebuild && (
                        <span
                          data-slot="rebuild-running-indicator"
                          aria-hidden
                          className="pointer-events-none absolute right-1 top-1 size-1.5 rounded-full bg-primary"
                        />
                      )}
                    </Button>
                  </DropdownMenuTrigger>
                </TooltipTrigger>
                <TooltipContent>{tc("more")}</TooltipContent>
              </Tooltip>
            </TooltipProvider>
            <DropdownMenuContent align="end">
              <DropdownMenuGroup>
                <RebuildButton
                  placement="menu"
                  hasRunning={hasRunningRebuild}
                  onClick={() => {
                    setHeaderMenuOpen(false);
                    setRebuildOpen(true);
                  }}
                />
              </DropdownMenuGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        ) : undefined}
      />

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
                key: "totalTokens",
                label: t("totalTokens"),
                value: formatTokensCompact(overviewValue?.total_tokens ?? 0),
              },
              {
                key: "cacheHit",
                label: t("kpi.cacheHit"),
                value: noData ? "—" : `${cacheHitPct.toFixed(1)}%`,
                sublabel: t("kpi.cacheSubFull", {
                  n: formatTokensCompact(savedTokens),
                  r: formatTokensCompact(cacheReadTokens),
                  w: formatTokensCompact(cacheWriteTokens),
                }),
                ratio: noData ? undefined : cacheHitPct,
              },
            ]}
          />
        );
      })()}

      <MetricTrendChart
        buckets={insights.data?.trend ?? []}
        costStacked={insights.data?.cost_trend_stacked}
        defaultMetric="tokens"
        title={t("usageTrend")}
        loading={insights.isLoading}
        scopeActiveCount={(model ? 1 : 0) + (trendTokenId ? 1 : 0) + (topN !== 5 ? 1 : 0)}
        scopeControls={
          <>
            <EntityPicker
              entity="model"
              size="sm"
              value={model}
              onChange={setModel}
              placeholder={tcf("filter.model")}
              className="w-full [&_[data-slot=button]]:h-9 sm:w-40 sm:[&_[data-slot=button]]:h-8"
            />
            <EntityPicker
              entity="token"
              size="sm"
              value={trendTokenId}
              onChange={setTrendTokenId}
              {...(selectedUserId ? { ownerUserId: selectedUserId } : {})}
              placeholder={t("filterTokenPick")}
              className="w-full [&_[data-slot=button]]:h-9 sm:w-40 sm:[&_[data-slot=button]]:h-8"
            />
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

      {isAdmin ? (
        <Tabs value={tab} onValueChange={setTab}>
          <TabsList>
            <TabsTrigger value="token">{t("byToken")}</TabsTrigger>
            <TabsTrigger value="channel">{t("byChannel")}</TabsTrigger>
          </TabsList>
          <TabsContent value="token" className="space-y-4">
            {tokenFilters}
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
            {channelFilters}
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
              getRowId={(r) => String(r.channel_id)}
              renderExpandedRow={renderChannelExpandedRow}
            />
          </TabsContent>
        </Tabs>
      ) : (
        <div className="space-y-4">
          {tokenFilters}
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
        </div>
      )}

      {isAdmin && (
        <RebuildDialog open={rebuildOpen} onOpenChange={setRebuildOpen} />
      )}
    </div>
  );
}
