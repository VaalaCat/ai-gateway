"use client";

import { useTranslations } from "next-intl";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { formatDate, formatFileSize } from "@/lib/utils/format";
import type { MonitoringAgentSample } from "./monitoring-data";

// RetainedAgentData 单独展示未成功获取节点的旧明细与最后成功时间。
export function RetainedAgentData({ samples, failedAgents, error }: {
  samples: MonitoringAgentSample[];
  failedAgents: { agent_id: number; agent_name: string; error: string }[];
  error?: unknown;
}) {
  // 1. 展示旧节点数据，不加入本轮聚合。
  const t = useTranslations("monitoring");
  const observability = useTranslations("observability");
  if (samples.length === 0) return null;
  return (
    <section className="flex flex-col gap-3" aria-label={t("refresh.retainedAgents", { n: samples.length })}>
      <h3 className="font-medium">{t("refresh.retainedAgents", { n: samples.length })}</h3>
      {samples.map((sample) => (
        <Card key={sample.agentID}>
          <CardHeader className="gap-2">
            <CardTitle className="flex flex-wrap items-center gap-2 text-sm">
              {sample.agentName || `#${sample.agentID}`} (#{sample.agentID})
              <Badge variant="secondary">{t("refresh.stale")}</Badge>
            </CardTitle>
            <p className="text-xs text-muted-foreground">
              {t("refresh.staleAt", { time: formatDate(sample.updatedAt / 1000) })}
            </p>
            <p className="break-words text-xs text-muted-foreground">
              {error ? (error instanceof Error ? error.message : String(error)) : failedAgents.find((agent) => agent.agent_id === sample.agentID)?.error || t("refresh.unknownAgent")}
            </p>
          </CardHeader>
          <CardContent className="flex flex-col gap-3 text-sm tabular-nums">
            {sample.entries.length === 0 && <p className="text-muted-foreground">{t("refresh.emptySample")}</p>}
            {sample.entries.map((entry, index) => {
              if (entry.kind === "limiter") {
                return (
                  <div key={index} className="flex flex-wrap gap-x-4 gap-y-1">
                    <span className="font-medium">{entry.bucket.name}</span>
                    <span className="break-all font-mono text-xs">{entry.bucket.bucket}</span>
                    <span>{entry.cell.occupied} / {entry.cell.capacity} {observability("limiter.colSaturation")}</span>
                    <span>{observability("limiter.colWaiters")}: {entry.cell.waiters}</span>
                  </div>
                );
              }
              if (entry.kind === "breaker") {
                return (
                  <div key={index} className="flex flex-wrap gap-x-4 gap-y-1">
                    <span className="font-mono">{entry.channel.source}:{entry.channel.channel_id}</span>
                    <span>{t(`breaker.state.${entry.cell.state === "half-open" ? "halfOpen" : entry.cell.state}`)}</span>
                    <span>{t("breaker.failures", { n: entry.cell.failures })}</span>
                    {entry.cell.remaining_ms > 0 && <span>{t("breaker.cooldownRemaining", { s: (entry.cell.remaining_ms / 1000).toFixed(1) })}</span>}
                  </div>
                );
              }
              return (
                <div key={index} className="flex flex-col gap-2">
                  <p>{t("delivery.mainQueue")} {entry.queue.store_len} · {t("delivery.retryQueue")} {entry.queue.retry_len} · {formatFileSize(entry.queue.total_bytes)} · {t("delivery.inflight")} {entry.queue.inflight}</p>
                  <p>{t("delivery.usageDropped", { n: entry.queue.shared_usage_dropped })} · {t("delivery.traceSlimmed", { n: entry.queue.api_trace_slimmed })}</p>
                  {entry.queue.last_error && <p className="break-words text-muted-foreground">{entry.queue.last_error}</p>}
                  {entry.queue.items.map((item) => (
                    <p key={item.queue_id || item.request_id} className="break-all font-mono text-xs text-muted-foreground">
                      {item.request_id} · {formatFileSize(item.bytes)} · {t("delivery.attempts")}: {item.attempts} · L{item.degrade_level}
                    </p>
                  ))}
                </div>
              );
            })}
          </CardContent>
        </Card>
      ))}
    </section>
  );
}
