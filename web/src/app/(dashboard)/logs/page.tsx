"use client";

import { Suspense, useCallback, useEffect, useMemo, useState } from "react";
import { useTranslations } from "next-intl";
import { ColumnDef, Row } from "@tanstack/react-table";
import { ChevronRight, KeyRound, RefreshCw } from "lucide-react";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import { useFilterState } from "@/components/data-table/use-filter-state";
import { usePaginationState } from "@/components/data-table/use-pagination-state";
import type { FilterSpec } from "@/components/data-table/filter-spec";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

import { DateCell } from "@/components/business/date-cell";
import { CostDetailCell } from "@/components/business/cost-cell";
import { DurationCell } from "@/components/business/duration-cell";
import { StreamBadge } from "@/components/business/status-badge";
import { ModelName } from "@/components/business/model-name";
import { TraceDetail } from "@/components/business/trace-detail";
import { FallbackChain } from "@/components/business/fallback-chain";
import { RateLimitSection } from "@/components/business/rate-limit-section";
import { EntityLabel } from "@/components/business/entity-label";
import { KpiGrid } from "@/components/business/kpi-grid";

import { formatDuration, formatFactor, formatMoneyCompact } from "@/lib/utils/format";
import { useLogs } from "@/lib/api/logs";
import { useLogsInsights } from "@/lib/api/logs-insights";
import { useChannels } from "@/lib/api/channels";
import { useBYOKChannels } from "@/lib/api/byok-channels";
import { useAuth } from "@/lib/auth";
import { useUserPref } from "@/hooks/use-user-pref";
import { PAGE_SIZES } from "@/lib/constants";
import type { UsageLog } from "@/lib/types";

const defaultColumnVisibility = {
  request_id: false,
  user_id: false,
  upstream_model: false,
  token_name: false,
  first_response_ms: false,
  inbound_protocol: false,
  outbound_protocol: false,
  is_stream: false,
  client_ip: false,
  cache_read_tokens: false,
  cache_write_tokens: false,
};

export default function LogsPage() {
  return (
    <Suspense fallback={<div className="py-12 text-center text-muted-foreground">Loading...</div>}>
      <LogsPageContent />
    </Suspense>
  );
}

function LogsPageContent() {
  const t = useTranslations("logs");
  const tc = useTranslations("common");
  const { isAdmin } = useAuth();

  const { data: channelsData } = useChannels({ page_size: 100 }, { enabled: isAdmin });
  const channelMap = useMemo(() => {
    const map = new Map<number, string>();
    for (const ch of channelsData?.data ?? []) {
      map.set(ch.id, ch.name);
    }
    return map;
  }, [channelsData]);

  // 仅作 hasOwnBYOK gate（决定非 admin 是否显示 BYOK filter）；
  // picker 自己的 list query 懒加载（enabled: open），不与此重复。
  // page_size:1 是探测当前用户是否有 BYOK channel 的最小代价。
  // admin 永远显示 picker，无需 gate query（用 enabled: !isAdmin 短路）。
  const ownBYOKQuery = useBYOKChannels({ page_size: 1 }, { enabled: !isAdmin });
  const hasOwnBYOK = (ownBYOKQuery.data?.data?.length ?? 0) > 0;

  const filterSpec = useMemo(() => ({
    time: { kind: "time", defaultDays: 7, maxHourDays: 7 },
    user_id: { kind: "picker", entity: "user", visible: (ctx: { isAdmin: boolean }) => ctx.isAdmin },
    token_id: { kind: "picker", entity: "token" },
    channel_id: { kind: "picker", entity: "channel", visible: (ctx: { isAdmin: boolean }) => ctx.isAdmin },
    private_channel_id: {
      kind: "picker",
      entity: "byok-channel",
      visible: (ctx: { isAdmin: boolean; hasOwnBYOK?: unknown }) => ctx.isAdmin || Boolean(ctx.hasOwnBYOK),
    },
    model_name: { kind: "picker", entity: "model" },
    request_id: { kind: "text", placeholder: t("searchRequestId") },
    status: {
      kind: "enum",
      options: [
        { value: "1", label: t("statusSuccess") },
        { value: "0", label: t("statusFailed") },
      ],
      placeholder: t("status"),
    },
  } satisfies FilterSpec), [t]);

  const [filterValues, setFilterValues] = useFilterState(filterSpec);

  const [page, pageSize, setPagination] = usePaginationState(PAGE_SIZES.LOGS);
  const [rawLog, setRawLog] = useState<UsageLog | null>(null);

  // 自动刷新间隔(ms);null=关。用户级持久化,多账号不串扰。
  const [autoRefreshMs, setAutoRefreshMs] = useUserPref<number | null>("logs-auto-refresh", null);

  const [now] = useState(() => Math.floor(Date.now() / 1000));
  const defaultStart = now - 7 * 86_400;
  const insights = useLogsInsights(
    {
      start: filterValues.start ? Number(filterValues.start) : defaultStart,
      end: filterValues.end ? Number(filterValues.end) : now,
    },
  );

  const { data, isLoading, isFetching, refetch } = useLogs(
    {
      page,
      page_size: pageSize,
      ...(filterValues.start ? { start: Number(filterValues.start) } : {}),
      ...(filterValues.end ? { end: Number(filterValues.end) } : {}),
      ...(filterValues.user_id ? { user_id: Number(filterValues.user_id) } : {}),
      ...(filterValues.token_id ? { token_id: Number(filterValues.token_id) } : {}),
      ...(filterValues.channel_id ? { channel_id: Number(filterValues.channel_id) } : {}),
      ...(filterValues.private_channel_id ? { private_channel_id: Number(filterValues.private_channel_id) } : {}),
      ...(filterValues.model_name ? { model_name: String(filterValues.model_name) } : {}),
      ...(filterValues.request_id ? { request_id: String(filterValues.request_id) } : {}),
      ...(filterValues.status ? { status: String(filterValues.status) } : {}),
    },
    { refetchInterval: autoRefreshMs ?? false },
  );

  const logs = data?.data ?? [];
  const total = data?.total ?? 0;
  const pageCount = Math.ceil(total / pageSize) || 1;

  // 陈旧书签(?page=99 但只有 14 页)自动回退到最后一页,避免空表格死角。
  // total===0 不回退(空结果集合法停在第 1/1 页);pageCount 恒 >=1,回退后 page<=pageCount,不会再触发,无死循环。
  useEffect(() => {
    if (!isLoading && total > 0 && page > pageCount) {
      setPagination(pageCount, pageSize);
    }
  }, [isLoading, total, page, pageCount, pageSize, setPagination]);

  const handlePaginationChange = (newPage: number, newPageSize: number) => {
    // 改每页条数回第 1 页(语义与 DataTablePagination 的 onPaginationChange(1, size) 一致)
    setPagination(newPageSize !== pageSize ? 1 : newPage, newPageSize);
  };

  const handleRefresh = () => {
    void refetch();
  };

  const rawLogText = useMemo(() => {
    if (!rawLog) return "";
    return JSON.stringify(rawLog, null, 2);
  }, [rawLog]);

  const renderAffinityBadge = useCallback((status?: string) => {
    if (status === "hit") {
      return (
        <Badge className="bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200 font-normal text-xs">
          {t("affinityHit")}
        </Badge>
      );
    }
    if (status === "fallback") {
      return (
        <Badge className="bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200 font-normal text-xs">
          {t("affinityFallback")}
        </Badge>
      );
    }
    return null;
  }, [t]);

  const columns: ColumnDef<UsageLog>[] = useMemo(() => {
    const billingModeLabel = (row: UsageLog): string | undefined => {
      const f = row.billing_factor;
      if (f == null) return undefined; // 老行,无标注
      if (row.free) return t("billingMode.free"); // 免费渠道 ×0
      if (row.owner_type === "private") {
        return f === 0
          ? t("billingMode.byokFree")
          : t("billingMode.byokFee", { factor: formatFactor(f) });
      }
      if (f === 1) return undefined; // 公共全价,不标注倍率
      return t("billingMode.channelRatio", { factor: formatFactor(f) });
    };
    const cols: ColumnDef<UsageLog>[] = [
      {
        id: "expand",
        header: "",
        cell: ({ row }) => (
          <Button
            variant="ghost"
            size="icon"
            className="size-6"
            onClick={() => row.toggleExpanded()}
          >
            <ChevronRight
              className={`size-4 transition-transform ${row.getIsExpanded() ? "rotate-90" : ""}`}
            />
          </Button>
        ),
        enableHiding: false,
      },
      {
        id: "raw_json",
        header: t("rawJson"),
        cell: ({ row }) => (
          <Button
            variant="ghost"
            size="xs"
            className="px-2"
            onClick={() => setRawLog(row.original)}
          >
            {t("viewRawJson")}
          </Button>
        ),
        enableHiding: false,
      },
      {
        accessorKey: "id",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={tc("id")} />
        ),
        enableHiding: false,
      },
      {
        accessorKey: "request_id",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("requestId")} />
        ),
        cell: ({ row }) => (
          <span className="max-w-[120px] truncate block font-mono text-meta">
            {row.original.request_id}
          </span>
        ),
      },
    ];

    // Conditionally include user_id and channel_id columns for admin only
    if (isAdmin) {
      cols.push({
        accessorKey: "user_id",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("userId")} />
        ),
        cell: ({ row }) => <EntityLabel entity="user" id={row.original.user_id} />,
      });
    }

    cols.push(
      {
        accessorKey: "model_name",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("modelName")} />
        ),
        cell: ({ row }) => <ModelName name={row.original.model_name} />,
      },
      {
        id: "channel",
        header: t("channelName"),
        cell: ({ row }) => {
          const log = row.original;
          const ownerType = log.owner_type || "admin";
          if (ownerType === "private") {
            return (
              <div className="flex items-center gap-1">
                <Badge variant="secondary" className="font-normal">
                  <KeyRound className="size-3 mr-1" />
                  {log.channel_name || `${t("byokBadge")} #${log.private_channel_id}`}
                </Badge>
                {renderAffinityBadge(log.affinity_status)}
              </div>
            );
          }
          if (isAdmin) {
            return (
              <div className="flex items-center gap-1">
                <span>{log.channel_name || "-"}</span>
                {renderAffinityBadge(log.affinity_status)}
              </div>
            );
          }
          return <span className="text-muted-foreground">{tc("shared")}</span>;
        },
      },
      {
        accessorKey: "status",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("status")} />
        ),
        cell: ({ row }) => {
          const s = row.original.status;
          if (s === 0) {
            return <Badge variant="destructive" className="text-xs">{t("statusFailed")}</Badge>;
          }
          return <Badge className="bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200 text-xs">{t("statusSuccess")}</Badge>;
        },
      },
      {
        id: "chain",
        header: t("chain"),
        cell: ({ row }) => {
          const chain = row.original.fallback_chain ?? [];
          if (chain.length <= 1) {
            return <span className="text-muted-foreground">{chain.length === 1 ? "✓" : "-"}</span>;
          }
          const ok = chain[chain.length - 1]?.status === "ok";
          return (
            <Badge className={ok
              ? "bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200 text-xs font-normal"
              : "text-xs"} variant={ok ? undefined : "destructive"}>
              {ok ? `⤵ ${chain.length}${t("chainTriesSuffix")}` : `✗ ${chain.length}${t("chainTriesSuffix")}`}
            </Badge>
          );
        },
      },
      {
        accessorKey: "upstream_model",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("upstreamModel")} />
        ),
        cell: ({ row }) => row.original.upstream_model
          ? <ModelName name={row.original.upstream_model} />
          : <span className="text-muted-foreground">-</span>,
      },
      {
        accessorKey: "token_name",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("tokenName")} />
        ),
        cell: ({ row }) => row.original.token_name || "-",
      },
      {
        accessorKey: "prompt_tokens",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("promptTokens")} />
        ),
      },
      {
        accessorKey: "completion_tokens",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("completionTokens")} />
        ),
      },
      {
        accessorKey: "total_cost",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("totalCost")} />
        ),
        cell: ({ row }) => (
          <CostDetailCell
            amount={row.original.total_cost}
            promptTokens={row.original.prompt_tokens}
            completionTokens={row.original.completion_tokens}
            cacheReadTokens={row.original.cache_read_tokens}
            cacheWriteTokens={row.original.cache_write_tokens}
            inputCost={row.original.input_cost}
            outputCost={row.original.output_cost}
            cacheReadCost={row.original.cache_read_cost}
            cacheWriteCost={row.original.cache_write_cost}
            rawInputCost={row.original.raw_input_cost}
            rawOutputCost={row.original.raw_output_cost}
            rawCacheReadCost={row.original.raw_cache_read_cost}
            rawCacheWriteCost={row.original.raw_cache_write_cost}
            billingFactor={row.original.billing_factor}
            modeLabel={billingModeLabel(row.original)}
          />
        ),
      },
      {
        accessorKey: "duration",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("duration")} />
        ),
        cell: ({ row }) => <DurationCell ms={row.original.duration} />,
      },
      {
        accessorKey: "first_response_ms",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("firstResponseMs")} />
        ),
        cell: ({ row }) => <DurationCell ms={row.original.first_response_ms} />,
      },
      {
        accessorKey: "inbound_protocol",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("inboundProtocol")} />
        ),
        cell: ({ row }) => row.original.inbound_protocol || "-",
      },
      {
        accessorKey: "outbound_protocol",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("outboundProtocol")} />
        ),
        cell: ({ row }) => row.original.outbound_protocol || "-",
      },
      {
        accessorKey: "is_stream",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("stream")} />
        ),
        cell: ({ row }) => <StreamBadge isStream={row.original.is_stream} />,
      },
      {
        accessorKey: "client_ip",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("clientIp")} />
        ),
      },
      {
        accessorKey: "cache_read_tokens",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("cacheReadTokens")} />
        ),
      },
      {
        accessorKey: "cache_write_tokens",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("cacheWriteTokens")} />
        ),
      },
      {
        accessorKey: "created_at",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={tc("createdAt")} />
        ),
        cell: ({ row }) => <DateCell timestamp={row.original.created_at} />,
      },
    );

    return cols;
  }, [isAdmin, renderAffinityBadge, t, tc]);

  const renderExpandedRow = (row: Row<UsageLog>) => {
    const log = row.original;
    const details = [
      [t("requestId"), log.request_id],
      ...(isAdmin ? [[t("userId"), log.user_id]] : []),
      [t("tokenName"), log.token_name || "-"],
      ...(isAdmin ? [[t("channelId"), channelMap.get(log.channel_id) ? `${log.channel_id} (${channelMap.get(log.channel_id)})` : log.channel_id]] : []),
      [t("modelName"), log.model_name],
      [t("upstreamModel"), log.upstream_model || "-"],
      [t("promptTokens"), log.prompt_tokens],
      [t("completionTokens"), log.completion_tokens],
      [t("totalCost"), formatMoneyCompact(log.total_cost)],
      ...(log.price_ratio !== undefined && log.price_ratio !== 1
        ? [[t("priceRatio"), String(log.price_ratio)]]
        : []),
      [t("duration"), formatDuration(log.duration)],
      [t("firstResponseMs"), log.first_response_ms ? formatDuration(log.first_response_ms) : "-"],
      [t("stream"), log.is_stream ? "Yes" : "No"],
      [t("clientIp"), log.client_ip || "-"],
      [t("inboundProtocol"), log.inbound_protocol || "-"],
      [t("outboundProtocol"), log.outbound_protocol || "-"],
      [t("cacheReadTokens"), log.cache_read_tokens],
      [t("cacheWriteTokens"), log.cache_write_tokens],
      [t("useLegacy"), log.use_legacy ? "Yes" : "No"],
    ];
    return (
      <div className="space-y-3 text-body">
        <div className="grid grid-cols-2 gap-x-8 gap-y-2 md:grid-cols-3">
          {details.map(([label, value]) => (
            <div key={String(label)}>
              <span className="text-muted-foreground">{String(label)}: </span>
              <span className="font-medium">{String(value)}</span>
            </div>
          ))}
        </div>
        {log.affinity_status && (
          <div>
            <span className="text-muted-foreground">{t("affinityLabel")}: </span>
            <span className="font-medium">
              {renderAffinityBadge(log.affinity_status) ?? t("affinityNone")}
              {log.affinity_recorded && (
                <span className="ml-2 text-xs text-muted-foreground">{t("affinityRecorded")}</span>
              )}
            </span>
          </div>
        )}
        {log.rate_limit_decision != null && log.rate_limit_decision !== "" && (
          <RateLimitSection
            decision={log.rate_limit_decision}
            waitMs={log.rate_limit_wait_ms}
            reason={log.rate_limit_reason}
            hits={log.rate_limit_hits}
          />
        )}
        {(log.fallback_chain?.length ?? 0) > 1 && (
          <FallbackChain chain={log.fallback_chain!} requestId={log.request_id} />
        )}
        {log.status === 0 && log.error_message && (
          <div>
            <span className="text-muted-foreground">{t("errorMessage")}: </span>
            <pre className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-md border bg-muted/50 p-2 text-meta font-mono">
              {log.error_message}
            </pre>
          </div>
        )}
        {(log.fallback_chain?.length ?? 0) <= 1 && log.has_trace && (
          <TraceDetail requestId={log.request_id} />
        )}
      </div>
    );
  };

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold">{t("title")}</h1>
        <p className="text-muted-foreground mt-1">{t("description")}</p>
      </div>

      {/* Row 1: 5 KpiGrid */}
      {(() => {
        const total = insights.data?.totals.total ?? 0;
        const failed = insights.data?.totals.failed ?? 0;
        const failedPct = total > 0 ? (failed / total) * 100 : 0;
        return (
          <KpiGrid
            items={[
              {
                key: "total",
                label: t("kpi.total"),
                value: total,
                ...(insights.data?.totals.spark_total
                  ? { spark: insights.data.totals.spark_total }
                  : {}),
              },
              {
                key: "failed",
                label: t("kpi.failed"),
                value: failed,
                ...(insights.data?.totals.spark_failed
                  ? { spark: insights.data.totals.spark_failed }
                  : {}),
              },
              {
                key: "failedRate",
                label: t("kpi.failedRate"),
                value: `${failedPct.toFixed(2)}%`,
                ratio: failedPct,
                threshold: { warn: 1, critical: 5 },
              },
              {
                key: "p95",
                label: t("kpi.p95"),
                value: formatDuration(insights.data?.totals.p95_ms ?? 0),
                ...(insights.data?.totals.spark_p95
                  ? { spark: insights.data.totals.spark_p95 }
                  : {}),
              },
              {
                key: "slowest",
                label: t("kpi.slowest"),
                value: formatDuration(insights.data?.totals.slowest_ms ?? 0),
              },
            ]}
          />
        );
      })()}

      <DataTable
        columns={columns}
        data={logs}
        loading={isLoading}
        total={total}
        page={page}
        pageSize={pageSize}
        pageCount={pageCount}
        onPaginationChange={handlePaginationChange}
        defaultColumnVisibility={defaultColumnVisibility}
        storageKey="logs"
        getRowId={(row) => String(row.id)}
        renderExpandedRow={renderExpandedRow}
        toolbar={
          <FilterableToolbar
            spec={filterSpec}
            value={filterValues}
            onChange={setFilterValues}
            context={{ hasOwnBYOK }}
            secondaryContent={
              <Select
                value={autoRefreshMs === null ? "off" : String(autoRefreshMs)}
                onValueChange={(v) => setAutoRefreshMs(v === "off" ? null : Number(v))}
              >
                <SelectTrigger className="w-40" size="sm">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="off">{t("autoRefreshOff")}</SelectItem>
                  <SelectItem value="5000">{t("autoRefreshEvery", { seconds: 5 })}</SelectItem>
                  <SelectItem value="10000">{t("autoRefreshEvery", { seconds: 10 })}</SelectItem>
                  <SelectItem value="30000">{t("autoRefreshEvery", { seconds: 30 })}</SelectItem>
                </SelectContent>
              </Select>
            }
            secondaryActions={[
              {
                label: isFetching ? t("refreshing") : t("refresh"),
                icon: <RefreshCw className={isFetching ? "animate-spin" : ""} />,
                onClick: handleRefresh,
                disabled: isFetching,
              },
            ]}
          />
        }
      />

      <Dialog open={!!rawLog} onOpenChange={(open) => { if (!open) setRawLog(null); }}>
        <DialogContent className="sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle>{t("rawJsonTitle")}</DialogTitle>
          </DialogHeader>
          <pre className="max-h-[60vh] overflow-auto rounded-md border bg-muted p-3 text-meta">
            <code>{rawLogText}</code>
          </pre>
          <DialogFooter>
            <Button variant="outline" onClick={() => setRawLog(null)}>
              {tc("cancel")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
