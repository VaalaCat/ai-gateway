"use client";

import { Cable, Network, RadioTower } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { AgentConnectionStatus } from "@/components/business/agent-connection-status";
import { AgentDiagnosticsButton } from "@/components/business/agent-diagnostics-button";
import { AgentOperationButtons } from "@/components/business/agent-operation-buttons";
import { AgentRouteTargets } from "@/components/business/agent-route-targets";
import { AgentURI } from "@/components/business/agent-uri";
import { Badge } from "@/components/ui/badge";
import { TooltipProvider } from "@/components/ui/tooltip";
import { useAgentRouteTargets, useEnqueueConnectivityProbe } from "@/lib/api/agents";
import { formatErrorToast } from "@/lib/api/error-toast";
import {
  routeTargetsIdentity,
  routeTargetsPageMatchesSnapshot,
} from "@/lib/agent-connection-snapshot";
import type {
  ConnectionSnapshot,
  RecentConnectionError,
  RelayMode,
  RouteTargetsPage,
} from "@/lib/types";

const relayModeLabelKeys: Record<RelayMode, "relayModeInherit" | "relayModeCustom" | "relayModeDisabled"> = {
  inherit: "relayModeInherit",
  custom: "relayModeCustom",
  disabled: "relayModeDisabled",
};

function RailNode({
  icon: Icon,
  title,
  status,
  actions,
  children,
}: {
  icon: typeof Cable;
  title: string;
  status: React.ReactNode;
  actions?: React.ReactNode;
  children?: React.ReactNode;
}) {
  return (
    <section className="min-w-0 py-2.5 first:pt-0 last:pb-0">
      <div className="grid min-w-0 grid-cols-[1.25rem_minmax(0,1fr)_auto] items-center gap-x-2">
        <Icon aria-hidden="true" className="size-4 justify-self-center text-muted-foreground" />
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <h2 className="text-sm font-medium">{title}</h2>
          {status}
        </div>
        {actions ? <div className="flex shrink-0 items-center">{actions}</div> : null}
      </div>
      {children ? (
        <div className="ml-2.5 mt-2 min-w-0 border-l pl-[1.125rem]">
          {children}
        </div>
      ) : null}
    </section>
  );
}

function ErrorList({ errors }: { errors: RecentConnectionError[] }) {
  const t = useTranslations("agents.connection");
  const newest = [...errors]
    .sort((left, right) => right.occurred_at - left.occurred_at)
    .slice(0, 20);
  if (newest.length === 0) return null;
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <h3 className="text-xs font-medium text-muted-foreground">{t("recentErrors")}</h3>
      <ul className="flex min-w-0 flex-col gap-1.5">
        {newest.map((error, index) => (
          <li
            key={[error.code, error.stage, error.occurred_at, error.count, index].join("-")}
            className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] gap-3 text-xs"
          >
            <span className="min-w-0 truncate text-muted-foreground">{error.message}</span>
            <code className="shrink-0 font-mono text-destructive">{error.code}</code>
          </li>
        ))}
      </ul>
    </div>
  );
}

function TargetSummaries({ snapshot }: { snapshot: ConnectionSnapshot }) {
  const t = useTranslations("agents.connection");
  const { direct, relay } = snapshot.target_summaries;
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-1.5">
      <Badge variant="secondary">
        {t("directCount", { reachable: direct.reachable, total: direct.total })}
      </Badge>
      <Badge variant="secondary">
        {t("relayCount", { reachable: relay.reachable, total: relay.total })}
      </Badge>
    </div>
  );
}

function RelayDetails({ snapshot, compact }: { snapshot: ConnectionSnapshot; compact: boolean }) {
  const t = useTranslations("agents.connection");
  const relay = snapshot.relay;
  if (compact) {
    return (
      <dl className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs">
        <dt className="text-muted-foreground">{t("desiredUri")}</dt>
        <dd className="min-w-0"><AgentURI uri={relay.desired.effective_uri} /></dd>
        <dt className="text-muted-foreground">{t("activeUri")}</dt>
        <dd className="min-w-0"><AgentURI uri={relay.active.uri} /></dd>
      </dl>
    );
  }
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <div className="grid min-w-0 gap-4 md:grid-cols-2">
        <section className="flex min-w-0 flex-col gap-2">
          <h3 className="text-xs font-medium text-muted-foreground">{t("configuration")}</h3>
          <dl className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-xs">
            <dt className="text-muted-foreground">{t("relayMode")}</dt>
            <dd>{t(relayModeLabelKeys[relay.desired.mode])}</dd>
            <dt className="text-muted-foreground">{t("desiredUri")}</dt>
            <dd className="min-w-0"><AgentURI uri={relay.desired.effective_uri} /></dd>
            <dt className="text-muted-foreground">{t("activeUri")}</dt>
            <dd className="min-w-0"><AgentURI uri={relay.active.uri} /></dd>
          </dl>
        </section>
        <section className="flex min-w-0 flex-col gap-2">
          <h3 className="text-xs font-medium text-muted-foreground">{t("relayRuntime")}</h3>
          <dl className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-xs">
            <dt className="text-muted-foreground">{t("streams")}</dt>
            <dd>{t("streamCount", { count: relay.active.streams })}</dd>
            {relay.active.retry_at > 0 ? (
              <>
                <dt className="text-muted-foreground">{t("retryAt")}</dt>
                <dd className="truncate">
                  <time dateTime={new Date(relay.active.retry_at * 1000).toISOString()}>
                    {new Date(relay.active.retry_at * 1000).toLocaleString()}
                  </time>
                </dd>
              </>
            ) : null}
          </dl>
        </section>
      </div>
      <ErrorList errors={relay.recent_errors} />
    </div>
  );
}

export function AgentConnectionRail({
  agentId,
  snapshot,
  initialRouteTargetsPage,
  stale = false,
  loading = false,
  compact = false,
}: {
  agentId: number;
  snapshot: ConnectionSnapshot;
  initialRouteTargetsPage?: RouteTargetsPage;
  stale?: boolean;
  loading?: boolean;
  compact?: boolean;
}) {
  const t = useTranslations("agents.connection");
  const targetProbe = useEnqueueConnectivityProbe();
  const initialPageMatchesSnapshot = !!initialRouteTargetsPage &&
    routeTargetsPageMatchesSnapshot(initialRouteTargetsPage, snapshot);
  const waitingForRouteTargetsPage = !initialPageMatchesSnapshot && (
    snapshot.target_summaries.direct.total > 0 || snapshot.target_summaries.relay.total > 0
  );
  const routeTargets = useAgentRouteTargets(agentId, initialRouteTargetsPage?.limit || 20, {
    enabled: initialPageMatchesSnapshot,
    initialPage: initialPageMatchesSnapshot ? initialRouteTargetsPage : undefined,
    snapshot: initialPageMatchesSnapshot ? routeTargetsIdentity(initialRouteTargetsPage) : undefined,
  });

  const probeTarget = async (targetAgentID: string) => {
    try {
      await targetProbe.mutateAsync({
        id: agentId,
        request: {
          scope: { kind: "targets", target_agent_ids: [targetAgentID] },
          expected_epoch: snapshot.snapshot_epoch,
          expected_control_generation: snapshot.control.session_generation,
          expected_relay_generation: snapshot.relay.active.session_generation,
        },
      });
    } catch (error) {
      toast.error(formatErrorToast(error, t("operationFailed")));
    }
  };

  const pageIdentity = initialRouteTargetsPage
    ? routeTargetsIdentity(initialRouteTargetsPage)
    : { snapshot_epoch: snapshot.snapshot_epoch, snapshot_seq: 0 };
  return (
    <TooltipProvider delayDuration={200}>
      <div data-testid="agent-connection-rail" className="min-w-0">
        {stale || routeTargets.snapshotConflict ? (
          <Badge variant="outline" className="mb-2">{t("stale")}</Badge>
        ) : null}
        <RailNode
          icon={Cable}
          title={t("control")}
          status={<AgentConnectionStatus kind="control" value={snapshot.control} />}
        >
          <RailNode
            icon={RadioTower}
            title={t("relay")}
            status={<AgentConnectionStatus kind="relay" value={snapshot.relay} />}
            actions={(
              <div className="flex items-center gap-1">
                <AgentOperationButtons
                  agentId={agentId}
                  snapshot={snapshot}
                  stale={stale}
                  loading={loading}
                />
                {!compact ? <AgentDiagnosticsButton agentId={agentId} /> : null}
              </div>
            )}
          >
            <div className="flex min-w-0 flex-col gap-3">
              <RelayDetails snapshot={snapshot} compact={compact} />
              <RailNode
                icon={Network}
                title={t("routeTargets")}
                status={<TargetSummaries snapshot={snapshot} />}
              >
                <AgentRouteTargets
                  pages={routeTargets.data?.pages}
                  currentSnapshot={{
                    ...pageIdentity,
                    observed_at: initialRouteTargetsPage?.observed_at ?? snapshot.observed_at,
                  }}
                  compact={compact}
                  isLoading={routeTargets.isLoading || waitingForRouteTargetsPage}
                  isFetchingNextPage={routeTargets.isFetchingNextPage}
                  hasNextPage={routeTargets.hasNextPage}
                  probingTargetID={targetProbe.isPending
                    ? targetProbe.variables?.request.scope?.target_agent_ids?.[0]
                    : undefined}
                  onProbeTarget={(targetAgentID) => void probeTarget(targetAgentID)}
                  onLoadMore={() => void routeTargets.fetchNextPage()}
                />
              </RailNode>
            </div>
          </RailNode>
        </RailNode>
      </div>
    </TooltipProvider>
  );
}
