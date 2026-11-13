"use client";

import { useTranslations } from "next-intl";
import { useState } from "react";

import { LiveTabHeader } from "@/components/monitoring/live-tab-header";
import { BreakerBoard } from "@/components/observability/breaker-board";
import { useBreakerBoard } from "@/lib/api/observability";
import { useMonitoringAgentHistory } from "./monitoring-data";
import { RetainedAgentData } from "./retained-agent-data";

export function BreakerTab() {
  const t = useTranslations("monitoring");
  const [paused, setPaused] = useState(false);
  const query = useBreakerBoard({ paused });
  const stale = useMonitoringAgentHistory(query.data, query.dataUpdatedAt, !!query.error);
  return (
    <div className="flex flex-col gap-4">
      <LiveTabHeader title={t("tab.breaker")} paused={paused} onPausedChange={setPaused}
        isFetching={query.isFetching} error={query.error} hasData={!!query.data}
        dataUpdatedAt={query.dataUpdatedAt} failedAgents={query.error ? [] : query.data?.failed_agents}
        staleCount={stale.length} onRefresh={() => { void query.refetch(); }} />
      {query.data && !query.error && <BreakerBoard data={query.data} incomplete={stale.length > 0 || query.data.failed_agents.length > 0} />}
      <RetainedAgentData samples={stale} failedAgents={query.data?.failed_agents ?? []} error={query.error} />
    </div>
  );
}
