"use client";

import { AgentConnectionRail } from "@/components/business/agent-connection-rail";
import type { ConnectionSnapshot, RouteTargetsPage } from "@/lib/types";

interface AgentConnectionsPanelProps {
  agentId: number;
  snapshot: ConnectionSnapshot;
  initialRouteTargetsPage?: RouteTargetsPage;
  stale?: boolean;
  loading?: boolean;
}

export function AgentConnectionsPanel({
  agentId,
  snapshot,
  initialRouteTargetsPage,
  stale = false,
  loading = false,
}: AgentConnectionsPanelProps) {
  return (
    <AgentConnectionRail
      agentId={agentId}
      snapshot={snapshot}
      initialRouteTargetsPage={initialRouteTargetsPage}
      stale={stale}
      loading={loading}
    />
  );
}
