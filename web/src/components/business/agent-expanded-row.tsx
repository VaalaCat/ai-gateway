"use client";

import { useTranslations } from "next-intl";

import { AgentConnectionRail } from "@/components/business/agent-connection-rail";
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
  if (connections.isLoading && !connections.data) {
    return (
      <div aria-label={t("loadingDetail")} className="flex flex-col gap-3 py-1">
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-16 w-full" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }
  if (!connections.data) return null;

  return (
    <AgentConnectionRail
      agentId={agent.id}
      snapshot={connections.data}
      initialRouteTargetsPage={connections.routeTargetsPage}
      stale={connections.stale}
      loading={connections.isLoading}
      compact
    />
  );
}
