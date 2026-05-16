"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { useTranslations } from "next-intl";
import { ColumnDef } from "@tanstack/react-table";
import { toast } from "sonner";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DateCell } from "@/components/business/date-cell";
import {
  DateRangeInputs,
  isDateRangeValid,
} from "@/components/business/date-range-inputs";
import { RebuildDialog } from "@/components/business/rebuild-dialog";
import {
  useBillingOverview,
  useChannelBilling,
  useTokenBilling,
} from "@/lib/api/billing";
import { useChannelTypes } from "@/lib/api/channels";
import { buildQuery } from "@/lib/api/client";
import { useAuth } from "@/lib/auth";
import { PAGE_SIZES } from "@/lib/constants";
import { formatCurrency, formatSuccessRate } from "@/lib/utils/format";
import type {
  BillingChannelRow,
  BillingOverviewResponse,
  BillingTokenRow,
} from "@/lib/types";

function MetricCard({
  title,
  value,
}: {
  title: string;
  value: string;
}) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium text-muted-foreground">
          {title}
        </CardTitle>
      </CardHeader>
      <CardContent>
        <div className="text-2xl font-semibold">{value}</div>
      </CardContent>
    </Card>
  );
}

function logHref(params: Record<string, string | number | undefined>) {
  return `/logs${buildQuery(params)}`;
}

export default function BillingPage() {
  const t = useTranslations("billing");
  const tc = useTranslations("common");
  const { isAdmin, loading } = useAuth();

  const [tab, setTab] = useState("token");
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState("");
  const [userId, setUserId] = useState("");
  const [channelId, setChannelId] = useState("");
  const [rebuildOpen, setRebuildOpen] = useState(false);

  const [tokenPage, setTokenPage] = useState(1);
  const [tokenPageSize, setTokenPageSize] = useState<number>(PAGE_SIZES.DEFAULT);
  const [channelPage, setChannelPage] = useState(1);
  const [channelPageSize, setChannelPageSize] = useState<number>(
    PAGE_SIZES.DEFAULT
  );

  const tokenUserId = userId ? Number(userId) : undefined;
  const channelFilterId = channelId ? Number(channelId) : undefined;
  const dateValid = isDateRangeValid(startDate, endDate);

  const overview = useBillingOverview(
    {
      ...(startDate ? { start_date: startDate } : {}),
      ...(endDate ? { end_date: endDate } : {}),
      ...(tokenUserId ? { user_id: tokenUserId } : {}),
    },
    { enabled: !loading && dateValid }
  );
  const tokenBilling = useTokenBilling(
    {
      page: tokenPage,
      page_size: tokenPageSize,
      ...(startDate ? { start_date: startDate } : {}),
      ...(endDate ? { end_date: endDate } : {}),
      ...(tokenUserId ? { user_id: tokenUserId } : {}),
    },
    { enabled: !loading && dateValid }
  );
  const channelBilling = useChannelBilling(
    {
      page: channelPage,
      page_size: channelPageSize,
      ...(startDate ? { start_date: startDate } : {}),
      ...(endDate ? { end_date: endDate } : {}),
      ...(channelFilterId ? { channel_id: channelFilterId } : {}),
    },
    { enabled: !loading && isAdmin && tab === "channel" && dateValid }
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
        cell: ({ row }) => formatCurrency(row.original.total_cost),
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
      },
      {
        accessorKey: "completion_tokens",
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={t("completionTokens")}
          />
        ),
      },
      {
        accessorKey: "last_used_at",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("lastUsedAt")} />
        ),
        cell: ({ row }) => <DateCell timestamp={row.original.last_used_at} />,
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
        cell: ({ row }) => formatCurrency(row.original.total_cost),
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
      },
      {
        accessorKey: "completion_tokens",
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={t("completionTokens")}
          />
        ),
      },
      {
        accessorKey: "last_used_at",
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t("lastUsedAt")} />
        ),
        cell: ({ row }) => <DateCell timestamp={row.original.last_used_at} />,
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
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">{t("title")}</h1>
          <p className="mt-1 text-muted-foreground">{t("description")}</p>
        </div>
        {isAdmin && (
          <Button variant="outline" onClick={() => setRebuildOpen(true)}>
            {t("rebuild")}
          </Button>
        )}
      </div>

      <div className="grid gap-3 md:grid-cols-4">
        <MetricCard
          title={t("totalCost")}
          value={formatCurrency(overviewValue?.total_cost ?? 0)}
        />
        <MetricCard
          title={t("requestCount")}
          value={String(overviewValue?.request_count ?? 0)}
        />
        <MetricCard
          title={t("successRate")}
          value={`${((overviewValue?.success_rate ?? 0) * 100).toFixed(1)}%`}
        />
        <MetricCard
          title={t("activeTokens")}
          value={String(overviewValue?.active_tokens ?? 0)}
        />
      </div>

      <div className="flex flex-wrap items-end gap-3 rounded-lg border p-4">
        <DateRangeInputs
          startDate={startDate}
          endDate={endDate}
          onStartDateChange={(d) => {
            setStartDate(d);
            setTokenPage(1);
            setChannelPage(1);
          }}
          onEndDateChange={(d) => {
            setEndDate(d);
            setTokenPage(1);
            setChannelPage(1);
          }}
        />
        {isAdmin && tab === "token" && (
          <div className="space-y-1">
            <label className="text-sm font-medium">{t("user")}</label>
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
            <label className="text-sm font-medium">{t("channelId")}</label>
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
