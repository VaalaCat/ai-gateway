"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { ChevronRight, RotateCcw, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { CopyableText } from "@/components/business/copyable-text";
import { EntityLabel } from "@/components/business/entity-label";
import { queueAccentClass, queueHealth } from "@/components/business/attempt-state";
import { useDeliveryBoard, useDeliveryOp } from "@/lib/api/observability";
import { formatDuration, formatFileSize, formatRelativeTime } from "@/lib/utils/format";
import { formatErrorToast } from "@/lib/api/error-toast";
import type { AgentQueueRow, DeliveryOpRequest, DeliveryQueueItem } from "@/lib/types";

// 降级等级 → 徽章文案+着色。等级越高剥离越多，红色最扎眼。
const DEGRADE_BADGE: Record<number, { label: string; cls: string }> = {
  0: { label: "L0", cls: "bg-muted text-muted-foreground" },
  1: { label: "L1", cls: "bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200" },
  2: { label: "L2", cls: "bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200" },
  3: { label: "L3", cls: "bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200" },
};

interface DropTarget {
  agentId: number;
  item: DeliveryQueueItem;
}

function DegradeBadge({ level }: { level: number }) {
  const t = useTranslations("monitoring");
  const meta = DEGRADE_BADGE[level] ?? DEGRADE_BADGE[0];
  return (
    <Badge title={t(`delivery.degrade.l${level}Desc`)} className={`text-xs font-normal ${meta.cls}`}>
      {meta.label}
    </Badge>
  );
}

function NextRetryCell({ nextAt, now }: { nextAt: number; now: number }) {
  const t = useTranslations("monitoring");
  if (!nextAt) return <span>{t("delivery.immediately")}</span>;
  const diffMs = nextAt * 1000 - now;
  if (diffMs <= 0) return <span>{t("delivery.immediately")}</span>;
  return <span>{formatDuration(diffMs)}</span>;
}

function ItemsTable({
  agentId,
  items,
  isPending,
  now,
  onOp,
  onDropRequest,
}: {
  agentId: number;
  items: DeliveryQueueItem[];
  isPending: boolean;
  now: number;
  onOp: (req: DeliveryOpRequest) => void;
  onDropRequest: (target: DropTarget) => void;
}) {
  const t = useTranslations("monitoring");

  return (
    <Table className="bg-muted/20">
      <TableHeader>
        <TableRow>
          <TableHead className="h-7 text-xs">{t("delivery.colRequestId")}</TableHead>
          <TableHead className="h-7 text-xs">{t("delivery.size")}</TableHead>
          <TableHead className="h-7 text-xs">{t("delivery.attempts")}</TableHead>
          <TableHead className="h-7 text-xs">{t("delivery.colDegrade")}</TableHead>
          <TableHead className="h-7 text-xs">{t("delivery.nextRetry")}</TableHead>
          <TableHead className="h-7 text-xs">{t("delivery.colActions")}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((item) => (
          <TableRow key={item.request_id}>
            <TableCell className="py-1.5 font-mono">
              <CopyableText text={item.request_id} />
            </TableCell>
            <TableCell className="py-1.5 text-sm">{formatFileSize(item.bytes)}</TableCell>
            <TableCell className="py-1.5 text-sm">{item.attempts}</TableCell>
            <TableCell className="py-1.5">
              <DegradeBadge level={item.degrade_level} />
            </TableCell>
            <TableCell className="py-1.5 text-sm">
              <NextRetryCell nextAt={item.next_at} now={now} />
            </TableCell>
            <TableCell className="py-1.5">
              <div className="flex items-center gap-1">
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-7 w-7 p-0"
                  disabled={isPending}
                  title={t("delivery.retryOne")}
                  onClick={() =>
                    onOp({ agent_id: agentId, op: "retry_now", request_ids: [item.request_id] })
                  }
                >
                  <RotateCcw className="h-3.5 w-3.5" />
                </Button>
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button variant="ghost" size="sm" className="h-7 text-xs" disabled={isPending}>
                      {t("delivery.degradeAction")}
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    {[2, 3].map((level) => (
                      <DropdownMenuItem
                        key={level}
                        onClick={() =>
                          onOp({
                            agent_id: agentId,
                            op: "degrade",
                            request_ids: [item.request_id],
                            level,
                          })
                        }
                      >
                        <div className="flex flex-col gap-0.5 py-0.5">
                          <span className="font-medium">L{level}</span>
                          <span className="text-xs text-muted-foreground">
                            {t(`delivery.degrade.l${level}Desc`)}
                          </span>
                        </div>
                      </DropdownMenuItem>
                    ))}
                  </DropdownMenuContent>
                </DropdownMenu>
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-7 w-7 p-0 text-destructive hover:text-destructive"
                  disabled={isPending}
                  title={t("delivery.drop")}
                  onClick={() => onDropRequest({ agentId, item })}
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </div>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function rowKey(r: AgentQueueRow) {
  return String(r.agent_id);
}

export function DeliveryBoard() {
  const t = useTranslations("monitoring");
  const tc = useTranslations("common");
  const { data, isLoading, dataUpdatedAt } = useDeliveryBoard();
  const op = useDeliveryOp();
  const [showAll, setShowAll] = useState(false);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [dropTarget, setDropTarget] = useState<DropTarget | null>(null);

  const toggle = (k: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(k)) next.delete(k);
      else next.add(k);
      return next;
    });

  const runOp = (req: DeliveryOpRequest) => {
    op.mutate(req, {
      onSuccess: (res) => toast.success(t("delivery.opSuccess", { n: res.affected })),
      onError: (e) => toast.error(formatErrorToast(e, t("delivery.opFailed"))),
    });
  };

  const all = data?.agents ?? [];
  const rows = showAll ? all : all.filter((r) => r.store_len + r.retry_len > 0);
  const now = dataUpdatedAt;

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs text-muted-foreground">
          {data?.failed_agents?.length
            ? t("delivery.failedAgents", { n: data.failed_agents.length })
            : ""}
        </span>
        <Button
          variant="ghost"
          size="sm"
          className="h-7 text-xs"
          onClick={() => setShowAll((v) => !v)}
        >
          {showAll ? t("delivery.onlyBacklogged") : t("delivery.showAll")}
        </Button>
      </div>

      {!isLoading && rows.length === 0 && (
        <p className="rounded-md border p-6 text-center text-sm text-muted-foreground">
          {t("delivery.allClear")}
        </p>
      )}

      <div className="space-y-2">
        {rows.map((r) => {
          const k = rowKey(r);
          const isOpen = expanded.has(k);
          const health = queueHealth(r, now);
          return (
            <div key={k} className={`rounded-md border border-l-2 ${queueAccentClass(health)}`}>
              <div className="flex items-center gap-3 px-3 py-2 text-sm hover:bg-muted/30">
                <button
                  type="button"
                  className="flex min-w-0 flex-1 flex-wrap items-center gap-x-3 gap-y-1 text-left"
                  onClick={() => toggle(k)}
                >
                  <ChevronRight
                    className={`h-4 w-4 shrink-0 text-muted-foreground transition-transform ${isOpen ? "rotate-90" : ""}`}
                  />
                  <span className="shrink-0 font-medium">
                    <EntityLabel entity="agent" id={r.agent_id} />
                  </span>
                  <span className="shrink-0 text-xs text-muted-foreground">
                    {t("delivery.mainQueue")} {r.store_len} · {t("delivery.retryQueue")} {r.retry_len} ·{" "}
                    {formatFileSize(r.total_bytes)}
                  </span>
                  <span className="shrink-0 text-xs text-muted-foreground">
                    {t("delivery.oldest")}{" "}
                    {r.oldest_ts ? formatRelativeTime(r.oldest_ts) : "—"}
                  </span>
                  <span className="shrink-0 text-xs text-muted-foreground">
                    {t("delivery.inflight")} {r.inflight}
                  </span>
                  <span className="shrink-0 text-xs text-muted-foreground">
                    {t("delivery.lastSuccess")}{" "}
                    {r.last_success_at ? formatRelativeTime(r.last_success_at) : t("delivery.never")}
                  </span>
                  {r.last_error && (
                    <span className="min-w-0 truncate text-xs text-muted-foreground">
                      {r.last_error}
                    </span>
                  )}
                </button>
                <Button
                  variant="outline"
                  size="sm"
                  className="h-7 shrink-0 text-xs"
                  disabled={op.isPending}
                  onClick={() => runOp({ agent_id: r.agent_id, op: "retry_now" })}
                >
                  {t("delivery.retryAll")}
                </Button>
              </div>
              {isOpen && (
                <div className="border-t px-3 py-2 pl-9">
                  <ItemsTable
                    agentId={r.agent_id}
                    items={r.items ?? []}
                    isPending={op.isPending}
                    now={now}
                    onOp={runOp}
                    onDropRequest={setDropTarget}
                  />
                </div>
              )}
            </div>
          );
        })}
      </div>

      <AlertDialog open={dropTarget !== null} onOpenChange={(o) => { if (!o) setDropTarget(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("delivery.dropTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {dropTarget && (
                <>
                  <span className="block font-mono">{dropTarget.item.request_id}</span>
                  <span className="block">
                    {t("delivery.size")}: {formatFileSize(dropTarget.item.bytes)} ·{" "}
                    {t("delivery.attempts")}: {dropTarget.item.attempts}
                  </span>
                  <span className="mt-2 block text-destructive">{t("delivery.dropWarning")}</span>
                </>
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{tc("cancel")}</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              disabled={op.isPending}
              onClick={() => {
                if (!dropTarget) return;
                runOp({
                  agent_id: dropTarget.agentId,
                  op: "drop",
                  request_ids: [dropTarget.item.request_id],
                });
                setDropTarget(null);
              }}
            >
              {t("delivery.dropConfirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
