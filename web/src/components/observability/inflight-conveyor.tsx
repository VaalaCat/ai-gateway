"use client";

import { useMemo } from "react";
import { AnimatePresence, motion, useReducedMotion } from "framer-motion";
import { useTranslations } from "next-intl";
import { cn } from "@/lib/utils";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card";
import type { GlobalInflightRow } from "@/lib/types";
import { EntityLabel } from "@/components/business/entity-label";

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

// blockKey 用 (agent_id, id) 复合键——id 是每节点唯一自增，req_id 来自客户端
// X-Request-Id 可能为空/重复，不能用作 React key 或 framer-motion layoutId。
function blockKey(row: GlobalInflightRow): string {
  return `${row.agent_id}:${row.id}`;
}

function blockClass(row: GlobalInflightRow, seg: Segment): string {
  if (seg === "queue") {
    const ms = row.queued_ms;
    if (ms > 5000) return "bg-red-500";
    if (ms > 1000) return "bg-amber-500";
    return "bg-muted-foreground/50";
  }
  const stuck = row.elapsed_ms > 60000 ? "ring-2 ring-red-500" : "";
  const byStage: Record<Segment, string> = {
    queue: "",
    plan: "bg-blue-500",
    exec: "bg-indigo-500",
    dispatch: "bg-violet-500",
    streaming: "bg-emerald-500",
  };
  return cn(byStage[seg], stuck);
}

const MAX_VISIBLE = 120;

export function InflightConveyor({ rows, onSelect }: {
  rows: GlobalInflightRow[];
  onSelect: (row: GlobalInflightRow) => void;
}) {
  const t = useTranslations("observability");
  const reduce = useReducedMotion();
  const anim = !reduce;
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
        return (
          <div key={seg} className={cn("rounded-md border p-2 lg:flex-1", seg === "queue" && "lg:max-w-[16rem] border-amber-500/40")}>
            <div className="mb-1 flex items-center justify-between text-xs text-muted-foreground">
              <span>{t(`segment.${seg}`)}</span>
              <span className="tabular-nums">{items.length}</span>
            </div>
            <div className="flex flex-wrap gap-1">
              <AnimatePresence mode="popLayout">
                {visible.map((row) => (
                  <HoverCard key={blockKey(row)} openDelay={80}>
                    <HoverCardTrigger asChild>
                      <motion.button
                        layout={anim}
                        layoutId={anim ? blockKey(row) : undefined}
                        initial={{ scale: 0, opacity: 0 }}
                        animate={{ scale: 1, opacity: 1 }}
                        exit={{ scale: 0, opacity: 0 }}
                        transition={{ duration: 0.35, ease: "easeOut" }}
                        onClick={() => onSelect(row)}
                        className={cn("h-4 w-4 rounded-sm", blockClass(row, seg))}
                        aria-label={`${row.view.model_name} ${row.stage}`}
                      />
                    </HoverCardTrigger>
                    <HoverCardContent className="w-72 text-xs">
                      <div className="space-y-0.5">
                        <div className="font-medium">{row.view.model_name}</div>
                        <div className="text-muted-foreground">
                          <EntityLabel entity="user" id={row.view.user_id} hover={false} />
                          {" · "}
                          <EntityLabel entity="channel" id={row.view.channel_id} hover={false} />
                          {" · "}
                          {row.agent_name}
                        </div>
                        {row.queued_ms > 0 ? (
                          <div className="text-amber-600">{t("queuedFor", { s: (row.queued_ms / 1000).toFixed(1) })} · {row.queued_reason}</div>
                        ) : (
                          <div>{t("stage")}: {row.stage} · {(row.elapsed_ms / 1000).toFixed(1)}s</div>
                        )}
                        {row.view.is_stream && <div>stream</div>}
                      </div>
                    </HoverCardContent>
                  </HoverCard>
                ))}
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
