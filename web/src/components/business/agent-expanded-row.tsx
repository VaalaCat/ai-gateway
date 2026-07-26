"use client";

import { useTranslations } from "next-intl";

import { AgentConnectionRail } from "@/components/business/agent-connection-rail";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useAgentConnections } from "@/lib/hooks/use-agent-connections";
import type { Agent } from "@/lib/types";

interface AgentExpandedRowProps {
  agent: Agent;
  expanded: boolean;
}

export function AgentExpandedRow({ agent, expanded }: AgentExpandedRowProps) {
  const t = useTranslations("agents.connection");
  const connections = useAgentConnections(agent.id, agent.connection, { enabled: expanded });

  if (!expanded) return null;
  if (connections.isPending && !connections.data) {
    return (
      <div aria-label={t("loadingDetail")} className="flex flex-col gap-3 py-1">
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-16 w-full" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }
  if (connections.isError && !connections.data) {
    return (
      <Alert variant="destructive">
        <AlertTitle>{t("detailLoadFailed")}</AlertTitle>
        <AlertDescription>
          <Button type="button" variant="outline" size="sm" onClick={() => void connections.refetch()}>
            {t("retry")}
          </Button>
        </AlertDescription>
      </Alert>
    );
  }
  if (!connections.data) return null;

  return (
    <AgentConnectionRail
      agentId={agent.id}
      snapshot={connections.data}
      initialRouteTargetsPage={connections.routeTargetsPage}
      stale={connections.stale}
      loading={connections.isPending}
      refreshing={connections.isFetching || !connections.routeTargetsCurrent}
      routeTargetsCurrent={connections.routeTargetsCurrent}
      compact
    />
  );
}
