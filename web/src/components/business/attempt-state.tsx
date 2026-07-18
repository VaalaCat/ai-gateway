"use client";

import type { UsageLog, AttemptInProgress } from "@/lib/types";

type SettledAttempt = NonNullable<UsageLog["fallback_chain"]>[number];

// —— 尝试结果视觉词汇(在途 retry 列 / FallbackChain 共用)——
export function attemptDotClass(e: SettledAttempt): string {
  if (e.breaker_open) return "bg-muted-foreground/40";
  if (e.status === "ok") return "bg-green-500";
  return "bg-destructive";
}
export function attemptGlyph(e: SettledAttempt): string {
  if (e.breaker_open) return "⊘";
  return e.status === "ok" ? "✓" : "✗";
}
export function attemptTextClass(e: SettledAttempt): string {
  if (e.breaker_open) return "text-muted-foreground";
  if (e.status === "ok") return "text-green-600 dark:text-green-400";
  return "text-destructive";
}

// —— 熔断状态视觉词汇(熔断看板共用)——
// 注:closed(健康)时 accent 用绿(行级"整体正常"的正向提示),而 per-agent dot 用中性灰
// (单节点点阵只想突出异常节点,健康节点不喧宾夺主)——两者对 closed 故意不同色,非笔误。
export function breakerDotClass(state: string): string {
  if (state === "open") return "bg-destructive";
  if (state === "half-open") return "bg-amber-500";
  return "bg-muted-foreground/30";
}
export function breakerAccentClass(state: string): string {
  if (state === "open") return "border-l-destructive";
  if (state === "half-open") return "border-l-amber-500";
  return "border-l-green-500";
}

// —— 投递队列健康度视觉词汇(旁路投递看板共用)——
export const QUEUE_STALL_WINDOW_MS = 5 * 60_000;
export const QUEUE_OLDEST_RED_MS = 10 * 60_000;
export type QueueHealth = "idle" | "backlog" | "stalled";
export function queueHealth(
  row: { store_len: number; retry_len: number; last_success_at: number; oldest_ts: number },
  nowMs: number,
): QueueHealth {
  if (row.store_len + row.retry_len === 0) return "idle";
  const noRecentSuccess =
    !row.last_success_at || nowMs - row.last_success_at * 1000 > QUEUE_STALL_WINDOW_MS;
  const tooOld = row.oldest_ts > 0 && nowMs - row.oldest_ts * 1000 > QUEUE_OLDEST_RED_MS;
  return noRecentSuccess || tooOld ? "stalled" : "backlog";
}
export function queueAccentClass(h: QueueHealth): string {
  if (h === "stalled") return "border-l-red-500";
  if (h === "backlog") return "border-l-amber-400";
  return "border-l-emerald-300/60";
}

// FallbackChainInline 横向带名链路:每段=渠道名+结果图标,着色;进行中段脉冲。
export function FallbackChainInline({
  chain,
  pending,
}: {
  chain?: UsageLog["fallback_chain"];
  pending?: AttemptInProgress | null;
}) {
  const settled = chain ?? [];
  if (settled.length === 0 && !pending) return null;
  return (
    <div className="flex flex-wrap items-center gap-x-1.5 gap-y-1 text-xs">
      {settled.map((e, i) => (
        <span key={e.seq} className="flex items-center gap-1">
          <span className="font-medium">{e.channel_name}</span>
          <span className={attemptTextClass(e)}>
            {attemptGlyph(e)}
            {e.http_status ? ` ${e.http_status}` : ""}
          </span>
          {(i < settled.length - 1 || pending) && (
            <span className="text-muted-foreground">›</span>
          )}
        </span>
      ))}
      {pending && (
        <span className="flex items-center gap-1">
          <span className="font-medium">{pending.channel_name}</span>
          <span className="animate-pulse text-blue-500">⟳</span>
        </span>
      )}
    </div>
  );
}
