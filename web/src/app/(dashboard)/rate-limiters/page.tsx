"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { ColumnDef } from "@tanstack/react-table";
import { toast } from "sonner";
import { Gauge, MoreHorizontal, Pencil, Trash2 } from "lucide-react";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { DeleteConfirm } from "@/components/business/delete-confirm";

import {
  useRateLimiters,
  useUpdateRateLimiter,
  useDeleteRateLimiter,
  useLimiterBindings,
} from "@/lib/api/rate-limiters";
import { formatErrorToast } from "@/lib/api/error-toast";
import { PAGE_SIZES } from "@/lib/constants";
import type {
  RequestLimiter,
  LimiterMetric,
  LimiterKeyBy,
  LimiterAction,
  LimiterChannelScope,
} from "@/lib/types";

const RATE_LIMITER_EDIT_PATH = "/rate-limiters/edit";

function metricLabelKey(metric: LimiterMetric): "metricConcurrency" | "metricRate" {
  return metric === "rate" ? "metricRate" : "metricConcurrency";
}

function keyByLabelKey(keyBy: LimiterKeyBy):
  | "keyShared"
  | "keyPerUser"
  | "keyPerGroup"
  | "keyPerChannel"
  | "keyPerChannelUser" {
  switch (keyBy) {
    case "per_user":
      return "keyPerUser";
    case "per_group":
      return "keyPerGroup";
    case "per_channel":
      return "keyPerChannel";
    case "per_channel_user":
      return "keyPerChannelUser";
    default:
      return "keyShared";
  }
}

function actionLabelKey(action: LimiterAction): "actionReject" | "actionWait" {
  return action === "wait" ? "actionWait" : "actionReject";
}

function scopeLabelKey(scope: LimiterChannelScope): "scopeAdmin" | "scopePrivate" | "scopeAll" {
  switch (scope) {
    case "private":
      return "scopePrivate";
    case "all":
      return "scopeAll";
    default:
      return "scopeAdmin";
  }
}

// channelKeyed 报告该 key_by 是否依赖具体渠道（决定 channel_scope 是否有意义）。
function channelKeyed(keyBy: LimiterKeyBy): boolean {
  return keyBy === "per_channel" || keyBy === "per_channel_user";
}

// 把 window_ms 渲染成人话窗口名："60 次/分"。整时长走整词，否则退化到毫秒。
function useLimitLabel() {
  const t = useTranslations("rateLimiters");
  return (limiter: RequestLimiter): string => {
    if (limiter.metric !== "rate") {
      return t("limitConcurrency", { capacity: limiter.capacity });
    }
    const window = formatWindow(limiter.window_ms, t);
    return t("limitRate", { capacity: limiter.capacity, window });
  };
}

function formatWindow(
  windowMs: number,
  t: ReturnType<typeof useTranslations>,
): string {
  if (windowMs === 1000) return t("windowSecond");
  if (windowMs === 60_000) return t("windowMinute");
  if (windowMs === 3_600_000) return t("windowHour");
  return t("windowMs", { ms: windowMs });
}

function BindingCount({ limiterId }: { limiterId: number }) {
  const { data, isLoading } = useLimiterBindings(limiterId);
  if (isLoading) return <span className="text-sm text-muted-foreground">…</span>;
  return <span className="text-sm tabular-nums text-muted-foreground">{data?.length ?? 0}</span>;
}

export default function RateLimitersPage() {
  const t = useTranslations("rateLimiters");
  const tc = useTranslations("common");
  const router = useRouter();
  const limitLabel = useLimitLabel();

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZES.DEFAULT);

  const { data, isLoading } = useRateLimiters({ page, page_size: pageSize });
  const updateMut = useUpdateRateLimiter();
  const deleteMut = useDeleteRateLimiter();
  const [deleteItem, setDeleteItem] = useState<RequestLimiter | null>(null);

  const limiters = data?.data ?? [];
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

  const handleToggle = (row: RequestLimiter, enabled: boolean) => {
    updateMut.mutate(
      { id: row.id, enabled },
      { onError: (e) => toast.error(formatErrorToast(e, tc("error"))) },
    );
  };

  const handleDelete = async () => {
    if (!deleteItem) return;
    try {
      await deleteMut.mutateAsync(deleteItem.id);
      toast.success(tc("success"));
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    } finally {
      setDeleteItem(null);
    }
  };

  const columns: ColumnDef<RequestLimiter>[] = [
    {
      accessorKey: "name",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("name")} />,
      cell: ({ row }) => (
        <button
          className="text-left text-sm font-medium hover:underline"
          onClick={() => router.push(`${RATE_LIMITER_EDIT_PATH}?id=${row.original.id}`)}
        >
          {row.original.name}
        </button>
      ),
    },
    {
      id: "metric",
      header: t("metric"),
      cell: ({ row }) => (
        <Badge variant="secondary" className="text-xs">
          {t(metricLabelKey(row.original.metric))}
        </Badge>
      ),
    },
    {
      id: "limit",
      header: t("limit"),
      cell: ({ row }) => (
        <span className="text-sm tabular-nums">{limitLabel(row.original)}</span>
      ),
    },
    {
      id: "keyBy",
      header: t("keyBy"),
      cell: ({ row }) => (
        <span className="text-sm">{t(keyByLabelKey(row.original.key_by))}</span>
      ),
    },
    {
      id: "action",
      header: t("action"),
      cell: ({ row }) => (
        <Badge variant="outline" className="text-xs">
          {t(actionLabelKey(row.original.action))}
        </Badge>
      ),
    },
    {
      id: "channelScope",
      header: t("channelScope"),
      cell: ({ row }) =>
        channelKeyed(row.original.key_by) ? (
          <span className="text-sm">{t(scopeLabelKey(row.original.channel_scope))}</span>
        ) : (
          <span className="text-sm text-muted-foreground">—</span>
        ),
    },
    {
      accessorKey: "enabled",
      header: t("status"),
      cell: ({ row }) => (
        <Switch
          checked={row.original.enabled}
          onCheckedChange={(v) => handleToggle(row.original, v)}
        />
      ),
    },
    {
      id: "bindings",
      header: t("bindings"),
      cell: ({ row }) => <BindingCount limiterId={row.original.id} />,
    },
    {
      id: "actions",
      header: tc("actions"),
      cell: ({ row }) => (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="size-8">
              <MoreHorizontal className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem
              onClick={() => router.push(`${RATE_LIMITER_EDIT_PATH}?id=${row.original.id}`)}
            >
              <Pencil className="mr-2 size-4" />
              {t("edit")}
            </DropdownMenuItem>
            <DropdownMenuItem
              className="text-destructive focus:text-destructive"
              onClick={() => setDeleteItem(row.original)}
            >
              <Trash2 className="mr-2 size-4" />
              {t("delete")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      ),
    },
  ];

  const isEmpty = !isLoading && limiters.length === 0;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t("title")}</h1>
          <p className="mt-1 text-muted-foreground">{t("description")}</p>
        </div>
        <Button onClick={() => router.push(`${RATE_LIMITER_EDIT_PATH}?id=new`)}>
          {t("create")}
        </Button>
      </div>

      {isEmpty ? (
        <div className="flex flex-col items-center justify-center gap-4 py-24 text-center">
          <Gauge className="size-12 text-muted-foreground" />
          <div>
            <p className="text-lg font-semibold">{t("empty")}</p>
            <p className="mt-1 text-muted-foreground">{t("emptyHint")}</p>
          </div>
          <Button onClick={() => router.push(`${RATE_LIMITER_EDIT_PATH}?id=new`)}>
            {t("create")}
          </Button>
        </div>
      ) : (
        <DataTable
          columns={columns}
          data={limiters}
          loading={isLoading}
          total={total}
          page={page}
          pageSize={pageSize}
          pageCount={pageCount}
          onPaginationChange={handlePaginationChange}
        />
      )}

      <DeleteConfirm
        open={!!deleteItem}
        onOpenChange={(open) => {
          if (!open) setDeleteItem(null);
        }}
        onConfirm={handleDelete}
        description={t("deleteConfirm")}
      />
    </div>
  );
}
