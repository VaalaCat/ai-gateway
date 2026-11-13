"use client";

import { useMemo } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import type { ColumnDef } from "@tanstack/react-table";
import { useTranslations } from "next-intl";
import { DataTable } from "@/components/data-table/data-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { EntityLabel } from "@/components/business/entity-label";
import { ModelName } from "@/components/business/model-name";
import { InflightRequestDetail } from "@/components/observability/inflight-request-detail";
import { CopyableText } from "@/components/business/copyable-text";
import { DurationCell } from "@/components/business/duration-cell";
import { formatDuration, formatFileSize, formatDate } from "@/lib/utils/format";
import { inflightChannel, inflightKey, type ObservedInflightRequest } from "@/lib/inflight";

interface InflightTableProps {
  requests: ObservedInflightRequest[];
  selectedKey: string | null;
  onSelect: (key: string) => void;
  onInterrupt: (key: string) => void;
  interruptPending: boolean;
  cancellationRequested: ReadonlySet<string>;
  unconfirmedInterrupts: Readonly<Record<string, number>>;
  stale: boolean;
  paused: boolean;
  showAgent?: boolean;
  emptyText: string;
}

/** 使用用量日志的表格展示单行请求和单条行内详情。 */
export function InflightTable({ requests, selectedKey, onSelect, onInterrupt, interruptPending, cancellationRequested, unconfirmedInterrupts, stale, paused, showAgent, emptyText }: InflightTableProps) {
  const t = useTranslations("inflight");
  const to = useTranslations("observability");
  const tl = useTranslations("logs");
  const ts = useTranslations("common.relayStage");

  // 1. 每个字段占一列，渠道编号不随名称截断。
  const columns = useMemo<ColumnDef<ObservedInflightRequest>[]>(() => [
    { id: "expand", header: "", enableHiding: false,
      meta: { cellClassName: "sticky left-0 bg-background", headerClassName: "sticky left-0 bg-background" },
      cell: ({ row }) => <Button variant="ghost" size="icon" className="size-6" data-testid={`inflight-row-${inflightKey(row.original.row)}`} aria-expanded={row.getIsExpanded()}
        aria-label={t(row.getIsExpanded() ? "collapseRequest" : "expandRequest", { id: row.original.row.req_id || row.original.row.id })}
        onClick={() => row.toggleExpanded()}>{row.getIsExpanded() ? <ChevronDown /> : <ChevronRight />}</Button>,
    },
    { id: "user", header: tl("userId"), cell: ({ row }) => row.original.row.view.user_id > 0 ? <EntityLabel entity="user" scope="all" id={row.original.row.view.user_id} nameClassName="max-w-24 truncate" /> : "—" },
    { id: "channel", header: to("detailChannel"), cell: ({ row }) => {
      const channel = inflightChannel(row.original.row);
      if (!channel.id) return <span className="text-xs text-muted-foreground">{t("channelPending")}</span>;
      return <span className="inline-flex items-center gap-1">
        {channel.private ? <Badge variant="secondary">{tl("byokBadge")}</Badge> : null}
        <EntityLabel entity={channel.private ? "byok-channel" : "channel"} scope="all" id={channel.id} snapshotName={channel.name} nameClassName="max-w-[120px] truncate" />
      </span>;
    } },
    { id: "model", header: to("detailModel"), cell: ({ row }) => <span className="block max-w-[180px] truncate" title={row.original.row.view.model_name}>
      <ModelName name={row.original.row.view.model_name || "—"} className="max-w-[180px] [&>span]:min-w-0 [&>span]:truncate" />
    </span> },
    { id: "stage", header: to("stage"), cell: ({ row }) => {
      const request = row.original;
      const stageKey = `stages.${request.row.stage}`;
      const label = request.status === "departed" ? t("departed") : stale || request.status === "stale" ? t("stale") : ts.has(request.row.stage) ? ts(request.row.stage) : t.has(stageKey) ? t(stageKey) : request.row.stage || "—";
      return <span className="inline-flex items-center gap-1" title={t("observedAt", { time: formatDate(request.observedAt / 1000) })}>
        {request.status !== "current" || stale ? <Badge variant="outline">{label}</Badge> : <span className="text-xs">{label}</span>}
        {request.row.queued_ms > 0 ? <span className="text-xs tabular-nums text-muted-foreground">{formatDuration(request.row.queued_ms)}</span> : null}
      </span>;
    } },
    { id: "bytes", header: t("clientProgress"), cell: ({ row }) => <span className="font-mono text-xs tabular-nums">{row.original.row.progress?.client_body_bytes == null ? "—" : formatFileSize(row.original.row.progress.client_body_bytes)}</span> },
    { id: "idle", header: t("idleColumn"), cell: ({ row }) => <span className="text-xs tabular-nums">{row.original.row.progress?.client_idle_ms == null ? "—" : formatDuration(row.original.row.progress.client_idle_ms)}</span> },
    { id: "elapsed", header: to("detailElapsed"), cell: ({ row }) => <DurationCell ms={row.original.row.elapsed_ms} /> },
    ...(showAgent ? [{ id: "agent", header: to("detailAgent"), cell: ({ row }: { row: { original: ObservedInflightRequest } }) => <EntityLabel entity="agent" id={row.original.row.view.route_source_agent_id} snapshotName={row.original.row.agent_name} showId={false} nameClassName="max-w-[100px] truncate" /> }] : []),
    { id: "request_id", header: t("requestId"), cell: ({ row }) => <span className="inline-flex items-center" data-row-interaction>
      <CopyableText text={row.original.row.req_id} display={row.original.row.req_id.length > 18 ? `${row.original.row.req_id.slice(0, 14)}…` : row.original.row.req_id || "—"} revealable />
    </span> },
    { id: "actions", header: t("actions"), enableHiding: false,
      meta: { cellClassName: "sticky right-0 bg-background text-right", headerClassName: "sticky right-0 bg-background text-right" },
      cell: ({ row }) => {
        const request = row.original;
        const key = inflightKey(request.row);
        const cancelling = cancellationRequested.has(key);
        const unconfirmed = (unconfirmedInterrupts[key] ?? -1) >= request.observedAt;
        return <Button variant="outline" size="xs" disabled={interruptPending || stale || paused || request.status !== "current" || cancelling || unconfirmed} onClick={() => onInterrupt(key)}>{t(unconfirmed ? "interruptUnconfirmed" : cancelling ? "cancelling" : "interrupt")}</Button>;
      },
    },
  ], [t, to, tl, ts, showAgent, stale, paused, interruptPending, cancellationRequested, unconfirmedInterrupts, onInterrupt]);

  // 2. 复用日志表格的单行密度、列显隐、键盘与选择文本处理。
  if (requests.length === 0) return <Empty><EmptyHeader><EmptyTitle>{emptyText}</EmptyTitle></EmptyHeader></Empty>;
  return <DataTable columns={columns} data={requests} showFooter={false}
    defaultColumnVisibility={{ request_id: false }} storageKey="inflight-columns"
    getRowId={(request) => inflightKey(request.row)}
    expandedState={selectedKey ? { [selectedKey]: true } : {}}
    onExpandedStateChange={(next) => {
      const key = next === true ? null : Object.keys(next).find((key) => next[key] && key !== selectedKey) ?? Object.keys(next).find((key) => next[key]) ?? null;
      if (key && key !== selectedKey) onSelect(key);
      else if (!key && selectedKey) onSelect(selectedKey);
    }}
    expandedRowWidth="viewport"
    renderExpandedRow={({ original }) => <InflightRequestDetail key={inflightKey(original.row)} request={original} stale={stale || original.status === "stale"} />}
  />;
}
