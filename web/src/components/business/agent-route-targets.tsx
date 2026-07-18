"use client";

import { Copy, LoaderCircle, Network, Radar } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import type {
  DirectPathState,
  RelayPathState,
  RouteTargetSnapshot,
  RouteTargetsPage,
} from "@/lib/types";

interface Props {
  pages?: RouteTargetsPage[];
  currentSnapshot: Pick<RouteTargetsPage, "snapshot_epoch" | "snapshot_seq" | "observed_at">;
  isLoading?: boolean;
  isFetchingNextPage?: boolean;
  hasNextPage?: boolean;
  probingTargetID?: string;
  compact?: boolean;
  onLoadMore?: () => void;
  onProbeTarget?: (targetAgentID: string) => void;
}

const MAX_RENDERED_TARGETS = 100;

function statusVariant(value: DirectPathState | RelayPathState) {
  if (value === "reachable") return "default" as const;
  if (value === "unreachable" || value === "unavailable") return "destructive" as const;
  if (value === "checking" || value === "stale") return "outline" as const;
  return "secondary" as const;
}

function labelKey(value: string) {
  return value.replace(/_([a-z])/g, (_, letter: string) => letter.toUpperCase());
}

function PathState({
  path,
  state,
  latency,
  reasonCode,
}: {
  path: "direct" | "relay";
  state: DirectPathState | RelayPathState;
  latency: number;
  reasonCode?: string;
}) {
  const t = useTranslations("agents.connection");
  const showReasonCode = state === "unreachable" || state === "unavailable" || state === "degraded";
  return (
    <div className="grid min-w-0 grid-cols-[3.5rem_minmax(0,1fr)_auto] items-center gap-2 py-1 sm:grid-cols-[auto_minmax(0,1fr)] sm:content-center sm:py-0">
      <span className="text-xs font-medium text-muted-foreground sm:hidden">{t(path)}</span>
      <div className="flex min-w-0 flex-wrap items-center gap-1.5">
        <Badge variant={statusVariant(state)}>{t(labelKey(state))}</Badge>
        {latency > 0 ? (
          <span className="text-xs tabular-nums text-muted-foreground">{latency} ms</span>
        ) : null}
      </div>
      {showReasonCode && reasonCode ? (
        <code className="col-span-2 col-start-2 truncate font-mono text-xs text-destructive sm:col-span-2 sm:col-start-1">
          {reasonCode}
        </code>
      ) : null}
    </div>
  );
}

function TargetActions({
  target,
  probing,
  onProbe,
}: {
  target: RouteTargetSnapshot;
  probing: boolean;
  onProbe?: () => void;
}) {
  const t = useTranslations("agents.connection");
  const copyDiagnostic = async () => {
    try {
      await navigator.clipboard.writeText(JSON.stringify(target, null, 2));
      toast.success(t("diagnosticCopied"));
    } catch {
      toast.error(t("diagnosticCopyFailed"));
    }
  };
  return (
    <div className="flex shrink-0 items-center gap-1">
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="size-11 sm:size-8"
            aria-label={t("probeTarget", { target: target.target_name || target.target_agent_id })}
            disabled={!onProbe || probing}
            onClick={onProbe}
          >
            {probing ? <LoaderCircle className="animate-spin" /> : <Radar />}
          </Button>
        </TooltipTrigger>
        <TooltipContent>{t("probeTarget", { target: target.target_name || target.target_agent_id })}</TooltipContent>
      </Tooltip>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="size-11 sm:size-8"
            aria-label={t("copyTargetDiagnostic")}
            onClick={() => void copyDiagnostic()}
          >
            <Copy />
          </Button>
        </TooltipTrigger>
        <TooltipContent>{t("copyTargetDiagnostic")}</TooltipContent>
      </Tooltip>
    </div>
  );
}

export function AgentRouteTargets({
  pages,
  currentSnapshot,
  isLoading,
  isFetchingNextPage,
  hasNextPage,
  probingTargetID,
  compact = false,
  onLoadMore,
  onProbeTarget,
}: Props) {
  const t = useTranslations("agents.connection");
  const byID = new Map<string, RouteTargetSnapshot>();
  for (const page of pages ?? []) {
    if (
      page.snapshot_epoch !== currentSnapshot.snapshot_epoch ||
      page.snapshot_seq !== currentSnapshot.snapshot_seq
    ) {
      continue;
    }
    for (const target of page.data) {
      byID.set(target.target_agent_id, target);
    }
  }
  const allTargets = [...byID.values()];
  const targets = allTargets.slice(-MAX_RENDERED_TARGETS);
  const targetGridColumns = compact
    ? "sm:grid-cols-[minmax(0,1.15fr)_minmax(0,1fr)_minmax(0,1fr)_auto]"
    : "sm:grid-cols-[minmax(0,1.25fr)_minmax(0,1fr)_minmax(0,1fr)_auto]";

  if (isLoading && targets.length === 0) {
    return (
      <div className="flex flex-col gap-2">
        <Skeleton className="h-20 w-full" />
        <Skeleton className="h-20 w-full" />
      </div>
    );
  }
  if (targets.length === 0) {
    return (
      <Empty className="min-h-24 p-3 md:p-3">
        <EmptyHeader>
          <EmptyMedia variant="icon"><Network /></EmptyMedia>
          <EmptyTitle className="text-sm">{t("noRouteTargets")}</EmptyTitle>
        </EmptyHeader>
      </Empty>
    );
  }

  return (
    <div className="flex min-w-0 flex-col gap-3">
      <div
        data-testid="route-target-columns"
        className={cn(
          "hidden min-w-0 items-center gap-x-2 border-b pb-1.5 text-xs font-medium text-muted-foreground sm:grid",
          targetGridColumns,
        )}
      >
        <span />
        <span>{t("direct")}</span>
        <span>{t("relay")}</span>
        <span />
      </div>
      <div role="list" className="flex min-w-0 flex-col">
        {targets.map((target, index) => (
          <div key={target.target_agent_id}>
            {index > 0 ? <Separator /> : null}
            <div
              role="listitem"
              className={cn(
                "grid min-w-0 grid-cols-[minmax(0,1fr)_auto] gap-x-2 sm:items-center",
                targetGridColumns,
                compact ? "py-2.5" : "py-3",
              )}
            >
              <div className="min-w-0 self-center">
                <div className="truncate text-sm font-medium">{target.target_name || target.target_agent_id}</div>
                <div className="truncate font-mono text-xs text-muted-foreground">{target.target_agent_id}</div>
              </div>
              <div className="col-start-2 row-start-1 sm:col-start-4">
                <TargetActions
                  target={target}
                  probing={probingTargetID === target.target_agent_id}
                  onProbe={onProbeTarget ? () => onProbeTarget(target.target_agent_id) : undefined}
                />
              </div>
              <div className="col-span-2 mt-1 min-w-0 sm:col-span-1 sm:col-start-2 sm:row-start-1 sm:mt-0">
                <PathState
                  path="direct"
                  state={target.direct.state}
                  latency={target.direct.latency_ms}
                  reasonCode={target.direct.last_error?.code}
                />
              </div>
              <div className="col-span-2 min-w-0 sm:col-span-1 sm:col-start-3 sm:row-start-1">
                <PathState
                  path="relay"
                  state={target.relay.state}
                  latency={target.relay.latency_ms}
                  reasonCode={target.relay.last_error?.code}
                />
              </div>
            </div>
          </div>
        ))}
      </div>
      {hasNextPage && allTargets.length < MAX_RENDERED_TARGETS ? (
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="self-start"
          disabled={isFetchingNextPage}
          onClick={onLoadMore}
        >
          {isFetchingNextPage ? <LoaderCircle data-icon="inline-start" className="animate-spin" /> : null}
          {t("loadMore")}
        </Button>
      ) : null}
    </div>
  );
}
