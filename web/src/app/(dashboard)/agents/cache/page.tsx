"use client";

import { useMemo } from "react";
import { useTranslations } from "next-intl";
import { RefreshCw } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import { CacheStatsTable } from "@/components/business/cache-stats-table";
import { DateCell } from "@/components/business/date-cell";

import { useCacheStats } from "@/lib/api/cache-stats";

export default function AgentsCachePage() {
  const t = useTranslations("observability");
  const ta = useTranslations("agents");
  const tc = useTranslations("common");
  const { data, isLoading, refetch, isFetching, dataUpdatedAt } = useCacheStats();

  // Online agents 的 agent_id 列表 → Accordion 初始展开状态。
  const defaultExpanded = useMemo(
    () => (data?.agents ?? []).filter((a) => a.online).map((a) => a.agent_id),
    [data],
  );
  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-start justify-between gap-2">
        <div>
          <h1 className="text-2xl font-bold">{t("cache")}</h1>
          <p className="text-muted-foreground mt-1">{ta("cacheSubtitle")}</p>
        </div>
        <div className="flex items-center gap-3 text-xs text-muted-foreground shrink-0 mt-1">
          {dataUpdatedAt > 0 && (
            <span>
              {ta("lastUpdated")}: <DateCell timestamp={Math.floor(dataUpdatedAt / 1000)} relative />
            </span>
          )}
          <Button
            variant="outline"
            size="sm"
            className="h-7 text-xs"
            onClick={() => refetch()}
            disabled={isFetching}
          >
            <RefreshCw className={`mr-1 size-3 ${isFetching ? "animate-spin" : ""}`} />
            {ta("refresh")}
          </Button>
        </div>
      </div>

      {isLoading || !data ? (
        <div className="flex items-center justify-center py-12 text-muted-foreground">
          {tc("loading")}
        </div>
      ) : (
        <>
          {/* Cluster aggregate */}
          <div className="rounded-md border">
            <div className="px-4 py-3 border-b">
              <h2 className="text-sm font-medium">{ta("clusterAggregate")}</h2>
            </div>
            <div className="p-4">
              <CacheStatsTable data={data.cluster} mode="cluster" />
            </div>
          </div>

          {/* Per-agent */}
          <div className="rounded-md border">
            <div className="px-4 py-3 border-b">
              <h2 className="text-sm font-medium">{ta("perAgent")}</h2>
            </div>
            {data.agents.length === 0 ? (
              <div className="px-4 py-6 text-center text-muted-foreground text-sm">
                {ta("noData")}
              </div>
            ) : (
              <Accordion
                type="multiple"
                defaultValue={defaultExpanded}
                className="px-2"
              >
                {data.agents.map((a) => (
                  <AccordionItem key={a.agent_id} value={a.agent_id}>
                    <AccordionTrigger className="hover:no-underline">
                      <div className="flex items-center gap-3 min-w-0">
                        <span className="font-medium truncate">{a.name}</span>
                        {a.online ? (
                          <Badge variant="secondary" className="shrink-0 bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200">
                            {ta("online")}
                          </Badge>
                        ) : (
                          <Badge variant="outline" className="shrink-0 text-muted-foreground">
                            {ta("offline")}
                          </Badge>
                        )}
                        {a.last_seen > 0 && (
                          <span className="text-xs text-muted-foreground shrink-0">
                            <DateCell timestamp={a.last_seen} relative />
                          </span>
                        )}
                      </div>
                    </AccordionTrigger>
                    <AccordionContent>
                      {a.cache_stats ? (
                        <CacheStatsTable data={a.cache_stats} mode="agent" />
                      ) : (
                        <div className="px-4 py-3 text-sm text-muted-foreground">
                          {ta("noData")}
                        </div>
                      )}
                    </AccordionContent>
                  </AccordionItem>
                ))}
              </Accordion>
            )}
          </div>
        </>
      )}
    </div>
  );
}
