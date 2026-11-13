"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { ChevronRight } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { EntityLabel } from "@/components/business/entity-label";
import { breakerDotClass, breakerAccentClass } from "@/components/business/attempt-state";
import type { ChannelBreakerRow, AgentBreakerCell, BreakerBoardResponse } from "@/lib/types";

// StateBadge 把熔断状态映射成着色徽章:open=红,half-open=黄,closed=绿。
function StateBadge({ state }: { state: string }) {
  const t = useTranslations("monitoring");
  if (state === "open") {
    return (
      <Badge variant="destructive" className="text-xs font-normal">
        {t("breaker.state.open")}
      </Badge>
    );
  }
  if (state === "half-open") {
    return (
      <Badge className="bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200 text-xs font-normal">
        {t("breaker.state.halfOpen")}
      </Badge>
    );
  }
  return (
    <Badge className="bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200 text-xs font-normal">
      {t("breaker.state.closed")}
    </Badge>
  );
}

function AgentRow({ cell }: { cell: AgentBreakerCell }) {
  const t = useTranslations("monitoring");
  return (
    <div className="flex flex-wrap items-center gap-2 text-xs">
      <span className="w-32 shrink-0 truncate text-muted-foreground">{cell.agent_name}</span>
      <StateBadge state={cell.state} />
      {cell.remaining_ms > 0 && (
        <span className="text-muted-foreground">
          {t("breaker.cooldownRemaining", { s: (cell.remaining_ms / 1000).toFixed(1) })}
        </span>
      )}
      <span className="text-muted-foreground">{t("breaker.failures", { n: cell.failures })}</span>
    </div>
  );
}

function rowKey(r: ChannelBreakerRow) {
  return `${r.source}:${r.channel_id}`;
}

export function BreakerBoard({ data, incomplete = false }: { data: BreakerBoardResponse; incomplete?: boolean }) {
  const t = useTranslations("monitoring");
  const [showAll, setShowAll] = useState(false);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const toggle = (k: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(k)) next.delete(k);
      else next.add(k);
      return next;
    });

  const all = data?.channels ?? [];
  const rows = showAll ? all : all.filter((r) => r.worst_state !== "closed");

  return (
    <div className="space-y-3">
      {incomplete && <p className="text-sm text-muted-foreground">{t("refresh.currentOnly")}</p>}
      <div className="flex items-center justify-between gap-2">
        <Button
          variant="ghost"
          size="sm"
          className="h-7 text-xs"
          onClick={() => setShowAll((v) => !v)}
        >
          {showAll ? t("breaker.onlyAbnormal") : t("breaker.showAll")}
        </Button>
      </div>

      {rows.length === 0 && (
        <p className="rounded-md border p-6 text-center text-sm text-muted-foreground">
          {t(incomplete ? "refresh.noCurrentData" : "breaker.allHealthy")}
        </p>
      )}

      <div className="space-y-2">
        {rows.map((r) => {
          const k = rowKey(r);
          const isOpen = expanded.has(k);
          return (
            <div key={k} className={`rounded-md border border-l-2 ${breakerAccentClass(r.worst_state)}`}>
              <button
                type="button"
                className="flex w-full items-center gap-3 px-3 py-2 text-sm hover:bg-muted/30"
                onClick={() => toggle(k)}
              >
                <ChevronRight
                  className={`h-4 w-4 shrink-0 text-muted-foreground transition-transform ${isOpen ? "rotate-90" : ""}`}
                />
                <span className="font-medium">
                  <EntityLabel entity="channel" id={r.channel_id} />
                </span>
                <div className="flex items-center gap-1">
                  {r.agents.map((a) => (
                    <span
                      key={a.agent_id}
                      className={`inline-block h-2 w-2 rounded-full ${breakerDotClass(a.state)}`}
                    />
                  ))}
                </div>
                <span className="ml-auto text-xs text-muted-foreground">
                  {r.worst_state === "closed"
                    ? t("breaker.agentsHealthy", { total: r.total_agents })
                    : t("breaker.agentsTripped", { open: r.open_agents, total: r.total_agents })}
                </span>
              </button>
              {isOpen && (
                <div className="space-y-1 border-t px-3 py-2 pl-9">
                  {r.agents.map((a) => (
                    <AgentRow key={a.agent_id} cell={a} />
                  ))}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
