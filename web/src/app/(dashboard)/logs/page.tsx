"use client";

import { Suspense, useMemo, useState } from "react";
import { useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { ColumnDef, Row } from "@tanstack/react-table";
import { ChevronRight, RefreshCw } from "lucide-react";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { Input } from "@/components/ui/input";
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
import { UserPicker } from "@/components/business/user-picker";
import { UsernameCell } from "@/components/business/username-cell";

import { formatCurrency } from "@/lib/utils/format";
import { useLogs } from "@/lib/api/logs";
import { useChannels } from "@/lib/api/channels";
import { useAuth } from "@/lib/auth";
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
  const searchParams = useSearchParams();
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

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZES.LOGS);
  const [userId, setUserId] = useState(() => searchParams.get("user_id") ?? "");
  const [tokenId, setTokenId] = useState(() => searchParams.get("token_id") ?? "");
  const [channelId, setChannelId] = useState(() => searchParams.get("channel_id") ?? "");
  const [modelName, setModelName] = useState("");
  const [status, setStatus] = useState(() => searchParams.get("status") ?? "");
  const [rawLog, setRawLog] = useState<UsageLog | null>(null);

  const { data, isLoading, isFetching, refetch } = useLogs({
    page,
    page_size: pageSize,
    ...(tokenId ? { token_id: Number(tokenId) } : {}),
    ...(isAdmin && userId ? { user_id: Number(userId) } : {}),
    ...(isAdmin && channelId ? { channel_id: Number(channelId) } : {}),
    ...(modelName ? { model_name: modelName } : {}),
    ...(status ? { status } : {}),
  });

  const logs = data?.data ?? [];
  const total = data?.total ?? 0;
  const pageCount = Math.ceil(total / pageSize) || 1;

  const handlePaginationChange = (newPage: number, newPageSize: number) => {
    if (newPageSize !== pageSize) {
      setPage(1);
      setPageSize(newPageSize);
    } else {
      setPage(newPage);
    }
  };

  const handleRefresh = () => {
    void refetch();
  };

  const rawLogText = useMemo(() => {
    if (!rawLog) return "";
    return JSON.stringify(rawLog, null, 2);
  }, [rawLog]);

  const columns: ColumnDef<UsageLog>[] = useMemo(() => {
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
            size="sm"
            className="h-7 px-2"
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
          <span className="max-w-[120px] truncate block font-mono text-xs">
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
        cell: ({ row }) => <UsernameCell userId={row.original.user_id} />,
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
  }, [isAdmin, t, tc]);

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
      [t("totalCost"), formatCurrency(log.total_cost)],
      [t("duration"), `${log.duration} ms`],
      [t("firstResponseMs"), log.first_response_ms ? `${log.first_response_ms} ms` : "-"],
      [t("stream"), log.is_stream ? "Yes" : "No"],
      [t("clientIp"), log.client_ip || "-"],
      [t("inboundProtocol"), log.inbound_protocol || "-"],
      [t("outboundProtocol"), log.outbound_protocol || "-"],
      [t("cacheReadTokens"), log.cache_read_tokens],
      [t("cacheWriteTokens"), log.cache_write_tokens],
      [t("useLegacy"), log.use_legacy ? "Yes" : "No"],
    ];
    return (
      <div className="space-y-3 text-sm">
        <div className="grid grid-cols-2 gap-x-8 gap-y-2 md:grid-cols-3">
          {details.map(([label, value]) => (
            <div key={String(label)}>
              <span className="text-muted-foreground">{String(label)}: </span>
              <span className="font-medium">{String(value)}</span>
            </div>
          ))}
        </div>
        {log.status === 0 && log.error_message && (
          <div>
            <span className="text-muted-foreground">{t("errorMessage")}: </span>
            <pre className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-md border bg-muted/50 p-2 text-xs font-mono">
              {log.error_message}
            </pre>
          </div>
        )}
        {log.has_trace && (
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
        renderExpandedRow={renderExpandedRow}
        toolbar={
          <div className="flex flex-wrap items-center gap-2">
            <Input
              placeholder={t("filterByToken")}
              value={tokenId}
              onChange={(e) => {
                setTokenId(e.target.value);
                setPage(1);
              }}
              className="w-full sm:w-40"
              type="number"
            />
            {isAdmin && (
              <UserPicker
                value={userId}
                onChange={(v) => { setUserId(v); setPage(1); }}
                placeholder={t("filterByUser")}
                className="w-full sm:w-48"
              />
            )}
            {isAdmin && (
              <Input
                placeholder={t("filterByChannel")}
                value={channelId}
                onChange={(e) => {
                  setChannelId(e.target.value);
                  setPage(1);
                }}
                className="w-full sm:w-40"
                type="number"
              />
            )}
            <Input
              placeholder={t("filterByModel")}
              value={modelName}
              onChange={(e) => {
                setModelName(e.target.value);
                setPage(1);
              }}
              className="w-full sm:w-40"
            />
            <Select value={status || "all"} onValueChange={(v) => { setStatus(v === "all" ? "" : v); setPage(1); }}>
              <SelectTrigger className="w-full sm:w-32">
                <SelectValue placeholder={t("filterByStatus")} />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">{t("allStatus")}</SelectItem>
                <SelectItem value="1">{t("statusSuccess")}</SelectItem>
                <SelectItem value="0">{t("statusFailed")}</SelectItem>
              </SelectContent>
            </Select>
            <Button
              variant="outline"
              size="sm"
              onClick={handleRefresh}
              disabled={isFetching}
            >
              <RefreshCw className={`mr-2 size-4 ${isFetching ? "animate-spin" : ""}`} />
              {isFetching ? t("refreshing") : t("refresh")}
            </Button>
          </div>
        }
      />

      <Dialog open={!!rawLog} onOpenChange={(open) => { if (!open) setRawLog(null); }}>
        <DialogContent className="sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle>{t("rawJsonTitle")}</DialogTitle>
          </DialogHeader>
          <pre className="max-h-[60vh] overflow-auto rounded-md border bg-muted p-3 text-xs">
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
