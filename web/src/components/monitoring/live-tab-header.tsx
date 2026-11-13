"use client";

import type { ReactNode } from "react";
import { useTranslations } from "next-intl";
import { formatDate } from "@/lib/utils/format";
import { AlertTriangle, Loader2, Pause, Play, RefreshCw } from "lucide-react";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";

// LiveTabHeader 展示刷新状态、数据时间、失败节点和刷新控制。
export function LiveTabHeader({
  title,
  subtitle,
  paused,
  onPausedChange,
  isFetching,
  error,
  dataUpdatedAt,
  hasData,
  failedAgents = [],
  staleCount = 0,
  onRefresh,
}: {
  title: string;
  subtitle?: ReactNode;
  paused: boolean;
  onPausedChange: (paused: boolean) => void;
  isFetching: boolean;
  error?: unknown;
  dataUpdatedAt: number;
  hasData: boolean;
  failedAgents?: { agent_id: number; agent_name: string; error: string }[];
  staleCount?: number;
  onRefresh: () => void;
}) {
  // 1. 按暂停、失败和节点缺失情况显示刷新状态。
  const t = useTranslations("monitoring");
  const partial = failedAgents.length > 0 || staleCount > 0;
  const status = paused ? "paused" : error ? (hasData ? "stale" : "error") : !hasData ? "loading" : isFetching ? "refreshing" : partial ? "partialStatus" : "live";
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-1">
          <h2 className="text-xl font-semibold">{title}</h2>
          {subtitle ? <p className="text-sm text-muted-foreground">{subtitle}</p> : null}
          <p className="text-xs text-muted-foreground" aria-live="polite">
            {hasData && dataUpdatedAt > 0
              ? t("refresh.lastUpdated", { time: formatDate(dataUpdatedAt / 1000) })
              : t("refresh.neverUpdated")}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant={error ? "destructive" : "secondary"} role="status">
            {isFetching && <Loader2 className="animate-spin" />}
            {t(`refresh.${status}`)}
          </Badge>
          <Button variant="outline" size="sm" onClick={() => onPausedChange(!paused)}>
            {paused ? <Play data-icon="inline-start" /> : <Pause data-icon="inline-start" />}
            {t(paused ? "refresh.resume" : "refresh.pause")}
          </Button>
          <Button variant="outline" size="sm" disabled={isFetching} onClick={onRefresh}>
            <RefreshCw data-icon="inline-start" />
            {t("refresh.refresh")}
          </Button>
        </div>
      </div>
      {error ? (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertTitle>{t("refresh.error")}</AlertTitle>
          <AlertDescription className="break-words">
            <p>{error instanceof Error ? error.message : String(error)}</p>
            <p>{t(hasData ? "refresh.retained" : "refresh.noData")}</p>
          </AlertDescription>
        </Alert>
      ) : null}
      {partial && (
        <Alert>
          <AlertTriangle />
          <AlertDescription>
            <p>{t("refresh.partial")}</p>
            {failedAgents.length > 0 && (
              <div className="flex flex-col gap-1">
                <p className="font-medium">{t("refresh.failedAgents", { n: failedAgents.length })}</p>
                <ul className="flex flex-col gap-1">
                  {failedAgents.map((agent) => (
                    <li key={agent.agent_id} className="break-words">
                      {agent.agent_name || `#${agent.agent_id}`} (#{agent.agent_id}): {agent.error}
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </AlertDescription>
        </Alert>
      )}
      {!hasData && isFetching && <Skeleton className="h-24 w-full" />}
      {!hasData && !isFetching && !error && <p className="text-sm text-muted-foreground">{t("refresh.noData")}</p>}
    </div>
  );
}
