"use client";

import { useTranslations } from "next-intl";
import { formatDate } from "@/lib/utils/format";
import { useState } from "react";

import { LiveTabHeader } from "@/components/monitoring/live-tab-header";
import { DeliveryBoard } from "@/components/observability/delivery-board";
import { useDeliveryBoard } from "@/lib/api/observability";
import { useMonitoringAgentHistory } from "./monitoring-data";
import { RetainedAgentData } from "./retained-agent-data";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";

export function DeliveryTab() {
  const t = useTranslations("monitoring");
  const [paused, setPaused] = useState(false);
  const query = useDeliveryBoard({ paused });
  const stale = useMonitoringAgentHistory(query.data, query.dataUpdatedAt, !!query.error);
  const backlog = query.data?.log_backlog;
  return (
    <div className="flex flex-col gap-4">
      <LiveTabHeader title={t("tab.delivery")} paused={paused} onPausedChange={setPaused}
        isFetching={query.isFetching} error={query.error} hasData={!!query.data}
        dataUpdatedAt={query.dataUpdatedAt} failedAgents={query.error ? [] : query.data?.failed_agents}
        staleCount={stale.length} onRefresh={() => { void query.refetch(); }} />
      {query.data && !query.error && <DeliveryBoard data={query.data} dataUpdatedAt={query.dataUpdatedAt} incomplete={stale.length > 0 || query.data.failed_agents.length > 0} />}
      {query.error && backlog ? (
        <Alert>
          <AlertTitle>{t("refresh.stale")}</AlertTitle>
          <AlertDescription>
            <p>{t("refresh.staleAt", { time: formatDate(query.dataUpdatedAt / 1000) })}</p>
            <p>{t("delivery.logBacklog", { queued: backlog.pending + backlog.retry + backlog.inflight, dropped: backlog.dropped })}</p>
            {backlog.last_error && <p className="break-words">{backlog.last_error}</p>}
          </AlertDescription>
        </Alert>
      ) : null}
      <RetainedAgentData samples={stale} failedAgents={query.data?.failed_agents ?? []} error={query.error} />
    </div>
  );
}
