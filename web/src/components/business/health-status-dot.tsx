"use client";

import { cn } from "@/lib/utils";
import type { AgentStatus } from "@/lib/hooks/use-agent-health";

/** 三档状态点：绿(healthy) / 琥珀(warn) / 红(down 或击穿红线)。 */
export function StatusDot({ status, red }: { status: AgentStatus; red: boolean }) {
  const color =
    status === "down" || (status === "warn" && red)
      ? "bg-red-500"
      : status === "warn"
        ? "bg-amber-500"
        : "bg-green-500";
  return <span className={cn("inline-block h-2.5 w-2.5 shrink-0 rounded-full", color)} />;
}

/** 饱和度条：>0.95 红 / >0.8 琥珀 / 否则绿；hasWaiters 时尾部加 ⋯。 */
export function SaturationBar({ pct, hasWaiters }: { pct: number; hasWaiters: boolean }) {
  const ratio = pct / 100;
  const color = ratio > 0.95 ? "bg-red-500" : ratio > 0.8 ? "bg-amber-500" : "bg-green-500";
  const width = Math.min(100, Math.max(0, pct));
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-20 overflow-hidden rounded bg-muted">
        <div className={cn("h-full", color)} style={{ width: `${width}%` }} />
      </div>
      <span className="tabular-nums text-xs whitespace-nowrap">
        {width.toFixed(0)}%{hasWaiters ? " ⋯" : ""}
      </span>
    </div>
  );
}
