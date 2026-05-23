"use client";

import Link from "next/link";
import { useState, useMemo } from "react";
import { useTranslations } from "next-intl";
import { ColumnDef } from "@tanstack/react-table";
import { ArrowLeft, ScrollText } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { MetricTile } from "@/components/business/metric-tile";
import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import {
  DateRangeInputs,
  isDateRangeValid,
} from "@/components/business/date-range-inputs";
import { BreakdownPopover } from "@/components/business/breakdown-popover";
import { BYOKTrendCharts } from "@/components/business/byok-trend-charts";
import { CostDetailCell } from "@/components/business/cost-cell";

import { useResponsiveColumnVisibility } from "@/hooks/use-responsive-column-visibility";
import { formatMoneyCompact, formatSuccessRate, formatTokensCompact } from "@/lib/utils/format";
import {
  useBYOKBillingByChannel,
  useBYOKBillingByModel,
  useBYOKBillingOverview,
  type ByChannelItem,
  type ByModelItem,
} from "@/lib/api/byok-stats";

export default function BYOKStatsPage() {
  const t = useTranslations("byok.stats");
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState("");
  const dateValid = isDateRangeValid(startDate, endDate);

  const range = useMemo(
    () => ({
      ...(startDate ? { from: startDate } : {}),
      ...(endDate ? { to: endDate } : {}),
    }),
    [startDate, endDate],
  );

  const overview = useBYOKBillingOverview(range, { enabled: dateValid });
  const byChannel = useBYOKBillingByChannel(range, { enabled: dateValid });
  const byModel = useBYOKBillingByModel(range, { enabled: dateValid });

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-2">
        <div className="flex items-center gap-2">
          <Button asChild variant="ghost" size="sm">
            <Link href="/byok">
              <ArrowLeft className="mr-2 size-4" />
              {t("back")}
            </Link>
          </Button>
          <h1 className="text-2xl font-bold">{t("title")}</h1>
        </div>
      </div>

      <p className="text-sm text-muted-foreground">{t("description")}</p>

      <div className="flex flex-wrap items-end gap-3 rounded-lg border p-4">
        <DateRangeInputs
          startDate={startDate}
          endDate={endDate}
          onStartDateChange={setStartDate}
          onEndDateChange={setEndDate}
        />
      </div>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <MetricTile
          label={t("totalRequests")}
          value={overview.data ? (overview.data.total_requests ?? 0).toLocaleString() : "—"}
          loading={overview.isLoading}
        />
        <MetricTile
          label={t("successRate")}
          value={
            overview.data
              ? formatSuccessRate(overview.data.total_success ?? 0, overview.data.total_requests ?? 0)
              : "—"
          }
          loading={overview.isLoading}
        />
        <MetricTile
          label={t("totalTokens")}
          loading={overview.isLoading}
          value={
            overview.data ? (
              <BreakdownPopover
                trigger={formatTokensCompact(overview.data.total_tokens ?? 0)}
                rows={[
                  { label: t("breakdownPromptTokens"),     value: (overview.data.total_prompt_tokens ?? 0).toLocaleString() },
                  { label: t("breakdownCompletionTokens"), value: (overview.data.total_completion_tokens ?? 0).toLocaleString() },
                  { label: t("breakdownCacheRead"),        value: (overview.data.total_cache_read_tokens ?? 0).toLocaleString(), muted: (overview.data.total_cache_read_tokens ?? 0) === 0, accent: "success" },
                  { label: t("breakdownCacheWrite"),       value: (overview.data.total_cache_write_tokens ?? 0).toLocaleString(), muted: (overview.data.total_cache_write_tokens ?? 0) === 0, accent: "info" },
                ]}
                align="start"
              />
            ) : "—"
          }
        />
        <MetricTile
          label={t("totalCost")}
          value={overview.data ? formatMoneyCompact(overview.data.total_cost ?? 0) : "—"}
          loading={overview.isLoading}
        />
      </div>

      <BYOKTrendCharts
        items={overview.data?.daily_series ?? []}
        loading={overview.isLoading}
      />

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <ByChannelCard
          items={byChannel.data?.items ?? []}
          loading={byChannel.isLoading}
        />
        <ByModelCard
          items={byModel.data?.items ?? []}
          loading={byModel.isLoading}
        />
      </div>
    </div>
  );
}

function ByChannelCard({
  items,
  loading,
}: {
  items: ByChannelItem[];
  loading: boolean;
}) {
  const t = useTranslations("byok.stats");
  const visConfig = useMemo(
    () => ({ storageKey: "byok-by-channel", hiddenOnMobile: ["success_rate"] as const }),
    [],
  );
  const [visibility, setVisibility] = useResponsiveColumnVisibility(visConfig);

  const columns = useMemo<ColumnDef<ByChannelItem>[]>(
    () => [
      {
        accessorKey: "channel_name",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("tableChannelName")} />
        ),
      },
      {
        accessorKey: "request_count",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("tableRequests")} />
        ),
        cell: ({ row }) => (
          <span className="font-mono">{(row.original.request_count ?? 0).toLocaleString()}</span>
        ),
      },
      {
        id: "success_rate",
        header: t("tableSuccessRate"),
        cell: ({ row }) => (
          <span className="font-mono">
            {formatSuccessRate(row.original.success_count ?? 0, row.original.request_count ?? 0)}
          </span>
        ),
      },
      {
        accessorKey: "total_tokens",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("tableTokens")} />
        ),
        cell: ({ row }) => (
          <BreakdownPopover
            trigger={formatTokensCompact(row.original.total_tokens ?? 0)}
            triggerClassName="font-mono"
            rows={[
              { label: t("breakdownPromptTokens"),     value: (row.original.prompt_tokens ?? 0).toLocaleString() },
              { label: t("breakdownCompletionTokens"), value: (row.original.completion_tokens ?? 0).toLocaleString() },
              { label: t("breakdownCacheRead"),        value: (row.original.cache_read_tokens ?? 0).toLocaleString(), muted: (row.original.cache_read_tokens ?? 0) === 0, accent: "success" },
              { label: t("breakdownCacheWrite"),       value: (row.original.cache_write_tokens ?? 0).toLocaleString(), muted: (row.original.cache_write_tokens ?? 0) === 0, accent: "info" },
            ]}
          />
        ),
      },
      {
        accessorKey: "total_cost",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("tableCost")} />
        ),
        cell: ({ row }) => (
          <CostDetailCell
            amount={row.original.total_cost ?? 0}
            promptTokens={row.original.prompt_tokens ?? 0}
            completionTokens={row.original.completion_tokens ?? 0}
            cacheReadTokens={row.original.cache_read_tokens ?? 0}
            cacheWriteTokens={row.original.cache_write_tokens ?? 0}
            inputCost={row.original.input_cost ?? 0}
            outputCost={row.original.output_cost ?? 0}
          />
        ),
      },
      {
        id: "actions",
        header: "",
        cell: ({ row }) => (
          <Button asChild variant="ghost" size="sm" title={t("viewLogs")}>
            <Link href={`/logs?private_channel_id=${row.original.private_channel_id}`}>
              <ScrollText className="size-4" />
            </Link>
          </Button>
        ),
      },
    ],
    [t],
  );

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("byChannelTitle")}</CardTitle>
      </CardHeader>
      <CardContent>
        <DataTable
          columns={columns}
          data={items}
          loading={loading}
          columnVisibilityState={visibility}
          onColumnVisibilityChange={setVisibility}
        />
      </CardContent>
    </Card>
  );
}

function ByModelCard({
  items,
  loading,
}: {
  items: ByModelItem[];
  loading: boolean;
}) {
  const t = useTranslations("byok.stats");
  const visConfig = useMemo(
    () => ({ storageKey: "byok-by-model", hiddenOnMobile: ["success_rate"] as const }),
    [],
  );
  const [visibility, setVisibility] = useResponsiveColumnVisibility(visConfig);

  const columns = useMemo<ColumnDef<ByModelItem>[]>(
    () => [
      {
        accessorKey: "model_name",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("tableModelName")} />
        ),
      },
      {
        accessorKey: "request_count",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("tableRequests")} />
        ),
        cell: ({ row }) => (
          <span className="font-mono">{(row.original.request_count ?? 0).toLocaleString()}</span>
        ),
      },
      {
        id: "success_rate",
        header: t("tableSuccessRate"),
        cell: ({ row }) => (
          <span className="font-mono">
            {formatSuccessRate(row.original.success_count ?? 0, row.original.request_count ?? 0)}
          </span>
        ),
      },
      {
        accessorKey: "total_tokens",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("tableTokens")} />
        ),
        cell: ({ row }) => (
          <BreakdownPopover
            trigger={formatTokensCompact(row.original.total_tokens ?? 0)}
            triggerClassName="font-mono"
            rows={[
              { label: t("breakdownPromptTokens"),     value: (row.original.prompt_tokens ?? 0).toLocaleString() },
              { label: t("breakdownCompletionTokens"), value: (row.original.completion_tokens ?? 0).toLocaleString() },
              { label: t("breakdownCacheRead"),        value: (row.original.cache_read_tokens ?? 0).toLocaleString(), muted: (row.original.cache_read_tokens ?? 0) === 0, accent: "success" },
              { label: t("breakdownCacheWrite"),       value: (row.original.cache_write_tokens ?? 0).toLocaleString(), muted: (row.original.cache_write_tokens ?? 0) === 0, accent: "info" },
            ]}
          />
        ),
      },
      {
        accessorKey: "total_cost",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("tableCost")} />
        ),
        cell: ({ row }) => (
          <CostDetailCell
            amount={row.original.total_cost ?? 0}
            promptTokens={row.original.prompt_tokens ?? 0}
            completionTokens={row.original.completion_tokens ?? 0}
            cacheReadTokens={row.original.cache_read_tokens ?? 0}
            cacheWriteTokens={row.original.cache_write_tokens ?? 0}
            inputCost={row.original.input_cost ?? 0}
            outputCost={row.original.output_cost ?? 0}
          />
        ),
      },
      {
        id: "actions",
        header: "",
        cell: ({ row }) => (
          <Button asChild variant="ghost" size="sm" title={t("viewLogs")}>
            <Link href={`/logs?model_name=${encodeURIComponent(row.original.model_name)}`}>
              <ScrollText className="size-4" />
            </Link>
          </Button>
        ),
      },
    ],
    [t],
  );

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("byModelTitle")}</CardTitle>
      </CardHeader>
      <CardContent>
        <DataTable
          columns={columns}
          data={items}
          loading={loading}
          columnVisibilityState={visibility}
          onColumnVisibilityChange={setVisibility}
        />
      </CardContent>
    </Card>
  );
}
