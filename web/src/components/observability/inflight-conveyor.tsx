"use client";

import { useMemo } from "react";
import { AnimatePresence, motion, useReducedMotion } from "framer-motion";
import { useTranslations } from "next-intl";
import { cn } from "@/lib/utils";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card";
import type { GlobalInflightRow } from "@/lib/types";
import { EntityLabel } from "@/components/business/entity-label";
import { ModelName } from "@/components/business/model-name";
import { Badge } from "@/components/ui/badge";
import { formatDate, formatDuration, formatFileSize } from "@/lib/utils/format";
import { inflightChannel, inflightKey } from "@/lib/inflight";

const SEGMENTS = ["queue", "plan", "exec", "dispatch", "streaming"] as const;
type Segment = (typeof SEGMENTS)[number];

// 映射真实 trace.Stage（inbound_decode/outbound_encode/upstream_dispatch/
// upstream_status/upstream_decode/client_encode/internal/none）到传送带分段。
function segmentOf(row: GlobalInflightRow): Segment {
  if (row.queued_ms > 0 || row.stage === "ratelimit_wait") return "queue";
  switch (row.stage) {
    case "inbound_decode":
      return "plan"; // 解析入站请求
    case "upstream_dispatch":
    case "upstream_status":
      return "dispatch"; // 正在呼叫上游
    case "upstream_decode":
    case "client_encode":
      return "streaming"; // 读取/回写上游响应
    default:
      return "exec"; // outbound_encode / internal / none / ""
  }
}

function blockClass(row: GlobalInflightRow, seg: Segment): string {
  if (seg === "queue") {
    const ms = row.queued_ms;
    if (ms > 5000) return "bg-red-500";
    if (ms > 1000) return "bg-amber-500";
    return "bg-muted-foreground/50";
  }
  const byStage: Record<Segment, string> = {
    queue: "",
    plan: "bg-blue-500",
    exec: "bg-indigo-500",
    dispatch: "bg-violet-500",
    streaming: "bg-emerald-500",
  };
  return byStage[seg];
}

const MAX_VISIBLE = 120;

export function InflightConveyor({ rows, onSelect, selectedKey, paused, stale, staleKeys, observedAt }: {
  rows: GlobalInflightRow[];
  onSelect: (row: GlobalInflightRow) => void;
  selectedKey: string | null;
  paused: boolean;
  stale: boolean;
  staleKeys: ReadonlySet<string>;
  observedAt: ReadonlyMap<string, number>;
}) {
  const t = useTranslations("observability");
  const ti = useTranslations("inflight");
  const reduce = useReducedMotion();
  const anim = !reduce && !paused && !stale;
  const grouped = useMemo(() => {
    const m: Record<Segment, GlobalInflightRow[]> = { queue: [], plan: [], exec: [], dispatch: [], streaming: [] };
    for (const r of rows) m[segmentOf(r)].push(r);
    m.queue.sort((a, b) => b.queued_ms - a.queued_ms);
    return m;
  }, [rows]);

  return (
    <div className="flex flex-col gap-2 lg:flex-row lg:items-stretch">
      {SEGMENTS.map((seg) => {
        const items = grouped[seg];
        const visible = items.slice(0, MAX_VISIBLE);
        const overflow = items.length - visible.length;
        const staleCount = stale ? items.length : items.filter((row) => staleKeys.has(inflightKey(row))).length;
        return (
          <div key={seg} className={cn("rounded-md border p-2 lg:flex-1", seg === "queue" && "lg:max-w-[16rem] border-amber-500/40")}>
            <div className="mb-1 flex items-center justify-between text-xs text-muted-foreground">
              <span>{t(`segment.${seg}`)}</span>
              <span className="tabular-nums">{paused ? ti("dotSnapshotCount", { n: items.length }) : staleCount > 0 ? ti("dotPartialCount", { current: items.length - staleCount, stale: staleCount }) : items.length}</span>
            </div>
            <div className="flex flex-wrap gap-1">
              <AnimatePresence mode="popLayout">
                {visible.map((row) => {
                  const key = inflightKey(row);
                  const old = stale || staleKeys.has(key);
                  const move = anim && !old;
                  const channel = inflightChannel(row);
                  return (
                  <HoverCard key={key} openDelay={80}>
                    <HoverCardTrigger asChild>
                      <motion.button
                        layout={move}
                        layoutId={move ? key : undefined}
                        initial={move ? { scale: 0, opacity: 0 } : false}
                        animate={{ scale: 1, opacity: old ? 0.45 : 1 }}
                        exit={move ? { scale: 0, opacity: 0 } : undefined}
                        transition={{ duration: 0.35, ease: "easeOut" }}
                        onClick={() => onSelect(row)}
                        className={cn("h-4 w-4 rounded-sm", blockClass(row, seg), selectedKey === key && "ring-2 ring-foreground ring-offset-2")}
                        aria-pressed={selectedKey === key}
                        aria-label={`${row.view.model_name} ${row.stage}`}
                      />
                    </HoverCardTrigger>
                    <HoverCardContent className="w-72 text-xs">
                      <div className="space-y-0.5">
                        <div className="font-medium"><ModelName name={row.view.model_name} /></div>
                        <div className="text-muted-foreground">
                          <EntityLabel entity="user" scope="all" id={row.view.user_id > 0 ? row.view.user_id : undefined} hover={false} />
                          {" · "}
                          {channel.private ? <Badge variant="secondary">BYOK</Badge> : null}{" "}
                          <EntityLabel entity={channel.private ? "byok-channel" : "channel"} scope="all" id={channel.id} snapshotName={channel.name} hover={false} />
                          {" · "}
                          <EntityLabel entity="agent" id={row.view.route_source_agent_id} snapshotName={row.agent_name} showId={false} hover={false} />
                        </div>
                        {row.queued_ms > 0 ? (
                          <div className="text-amber-600">{ti("queued", { value: formatDuration(row.queued_ms) })} · {row.queued_reason}</div>
                        ) : (
                          <div>{t("stage")}: {row.stage} · {formatDuration(row.elapsed_ms)}</div>
                        )}
                        <div>{ti("clientProgress")}: {row.progress?.client_body_bytes == null ? "—" : formatFileSize(row.progress.client_body_bytes)} · {ti("idleColumn")}: {row.progress?.client_idle_ms == null ? "—" : formatDuration(row.progress.client_idle_ms)}</div>
                        {old || paused ? <div>{ti(old ? "stale" : "dotPaused")} · {formatDate((observedAt.get(key) ?? 0) / 1000)}</div> : null}
                      </div>
                    </HoverCardContent>
                  </HoverCard>
                );
                })}
              </AnimatePresence>
              {overflow > 0 && (
                <span className="flex h-4 items-center rounded-sm bg-muted px-1 text-[10px] tabular-nums">+{overflow}</span>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}
