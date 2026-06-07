"use client";

import { useMemo } from "react";
import {
  useAllInflight,
  useLimiterUsage,
  useRecentHealth,
  useHealthThresholds,
  type HealthThresholds,
} from "@/lib/api/observability";
import { useAgents, useOnlineAgents } from "@/lib/api/agents";
import type { Agent } from "@/lib/types";

export type AgentStatus = "healthy" | "warn" | "down";

/** 单个 agent 的归一化健康视图（已完成各数据源 join）。 */
export interface AgentHealth {
  uid: string;
  numericId?: number;
  name: string;
  secsSinceSeen: number;
  errorRate: number; // 0..1
  qps: number;
  inflight: number;
  saturationPct: number; // 0..100
  hasWaiters: boolean;
  status: AgentStatus;
  /** 是否击穿红线（误差/饱和达到 red 阈值），用于更强的红色强调。 */
  red: boolean;
}

export function healthStatus(
  a: { online: boolean; secsSinceSeen: number; errorRate: number; saturationPct: number },
  th: HealthThresholds,
): AgentStatus {
  if (!a.online || a.secsSinceSeen > th.offlineSeconds) return "down";
  const errPct = a.errorRate * 100;
  if (errPct >= th.errYellowPct || a.saturationPct >= th.satYellowPct) return "warn";
  return "healthy";
}

export function isRed(
  a: { errorRate: number; saturationPct: number },
  th: HealthThresholds,
): boolean {
  return a.errorRate * 100 >= th.errRedPct || a.saturationPct >= th.satRedPct;
}

export interface AgentHealthResult {
  /** 数值 agent_id → 健康视图（仅在线节点有条目）。 */
  byId: Map<number, AgentHealth>;
  counts: { total: number; warn: number; down: number };
  /** 非健康（warn/down）的在线节点对应的注册行，供「仅异常」绕分页渲染。 */
  anomalies: Agent[];
  thresholds: HealthThresholds;
}

const STATUS_ORDER: Record<AgentStatus, number> = { down: 0, warn: 1, healthy: 2 };

export function useAgentHealth(): AgentHealthResult {
  const { data: online } = useOnlineAgents();
  const { data: recentHealth } = useRecentHealth();
  const { data: inflight } = useAllInflight();
  const { data: limiter } = useLimiterUsage();
  // 全量 agents：把 online 的 string uid 映射到数值 id，并供「仅异常」用注册行。
  const { data: agentsPage } = useAgents({ page: 1, page_size: 1000 });
  const th = useHealthThresholds();

  return useMemo<AgentHealthResult>(() => {
    const nowSec = Math.floor(Date.now() / 1000);
    const roster = online ?? [];
    const healthByUid = new Map((recentHealth?.agents ?? []).map((h) => [h.agent_id, h]));
    const agentList = agentsPage?.data ?? [];
    const idByUid = new Map(agentList.map((a) => [a.agent_id, a.id]));

    const list: AgentHealth[] = roster
      .map((o): AgentHealth => {
        const secsSinceSeen = Math.max(0, nowSec - o.last_seen);
        const health = healthByUid.get(o.agent_id);
        // 用数字 agent_id join inflight/limiter——名字无唯一约束，重名会张冠李戴。
        const numericId = idByUid.get(o.agent_id);

        const inflightCount = (inflight?.requests ?? []).filter(
          (r) => r.agent_id === numericId,
        ).length;

        let saturationPct = 0;
        let hasWaiters = false;
        for (const bucket of limiter?.buckets ?? []) {
          for (const pa of bucket.per_agent) {
            if (pa.agent_id !== numericId) continue;
            const pct = pa.capacity > 0 ? (pa.occupied / pa.capacity) * 100 : 0;
            if (pct > saturationPct) saturationPct = pct;
            if (pa.waiters > 0) hasWaiters = true;
          }
        }

        const errorRate = health?.error_rate ?? 0;
        const status = healthStatus(
          { online: true, secsSinceSeen, errorRate, saturationPct },
          th,
        );
        return {
          uid: o.agent_id,
          numericId,
          name: o.name,
          secsSinceSeen,
          errorRate,
          qps: health?.qps ?? 0,
          inflight: inflightCount,
          saturationPct,
          hasWaiters,
          status,
          red: isRed({ errorRate, saturationPct }, th),
        };
      })
      .sort((a, b) => {
        if (STATUS_ORDER[a.status] !== STATUS_ORDER[b.status])
          return STATUS_ORDER[a.status] - STATUS_ORDER[b.status];
        return a.name.localeCompare(b.name);
      });

    const byId = new Map<number, AgentHealth>();
    for (const h of list) if (h.numericId !== undefined) byId.set(h.numericId, h);

    let warn = 0;
    let down = 0;
    for (const h of list) {
      if (h.status === "down") down++;
      else if (h.status === "warn") warn++;
    }

    const anomalies = agentList
      .filter((a) => {
        const s = byId.get(a.id)?.status;
        return s === "warn" || s === "down";
      })
      .sort((a, b) => {
        const sa = byId.get(a.id)!.status;
        const sb = byId.get(b.id)!.status;
        if (STATUS_ORDER[sa] !== STATUS_ORDER[sb]) return STATUS_ORDER[sa] - STATUS_ORDER[sb];
        return a.name.localeCompare(b.name);
      });

    return { byId, counts: { total: list.length, warn, down }, anomalies, thresholds: th };
  }, [online, recentHealth, inflight, limiter, agentsPage, th]);
}
