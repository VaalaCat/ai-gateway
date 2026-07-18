"use client";

import { Suspense, useState } from "react";
import { ArrowLeft, LoaderCircle, RefreshCw } from "lucide-react";
import { useRouter, useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { AgentConnectionsPanel } from "@/components/business/agent-connections-panel";
import { AgentConnectionStatus } from "@/components/business/agent-connection-status";
import { CacheStatsTable } from "@/components/business/cache-stats-table";
import { CopyableText } from "@/components/business/copyable-text";
import { DateCell } from "@/components/business/date-cell";
import { InflightTable } from "@/components/business/inflight-table";
import { InflightBlockDetail } from "@/components/observability/inflight-block-detail";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  useAgentDetail,
  useAgentGoroutines,
  useAgentInflight,
  useFullSyncAgents,
  useInterruptInflight,
} from "@/lib/api/agents";
import type { GoroutineDump } from "@/lib/api/agents";
import { formatErrorToast } from "@/lib/api/error-toast";
import type { AgentAddress, AgentDetail, GlobalInflightRow } from "@/lib/types";
import { formatDuration, formatUptime } from "@/lib/utils/format";
import { useAgentConnections } from "@/lib/hooks/use-agent-connections";
import { routeTargetsPageMatchesSnapshot } from "@/lib/agent-connection-snapshot";

function parseAddresses(raw: string): AgentAddress[] {
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function Stat({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0">
      <div className="truncate text-xs text-muted-foreground">{label}</div>
      <div className="mt-0.5 min-w-0 truncate text-sm font-medium">{children}</div>
    </div>
  );
}

function GoroutineDumpDialog({ open, onOpenChange, data }: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  data: GoroutineDump | null;
}) {
  const t = useTranslations("agents");
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[80vh] max-w-3xl flex-col">
        <DialogHeader>
          <DialogTitle>{data ? t("goroutineDumpTitle", { count: data.count }) : t("goroutineDump")}</DialogTitle>
          <DialogDescription className="sr-only">{t("goroutineDumpDescription")}</DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-auto">
          <pre className="whitespace-pre-wrap break-all rounded-md bg-muted p-2 text-xs">{data?.dump ?? ""}</pre>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function InflightSection({ agentId, agentName }: { agentId: number; agentName: string }) {
  const t = useTranslations("agents");
  const tc = useTranslations("common");
  const { data: rows = [], isFetching, refetch } = useAgentInflight(agentId);
  const goroutines = useAgentGoroutines();
  const interrupt = useInterruptInflight();
  const [dumpOpen, setDumpOpen] = useState(false);
  const [dumpData, setDumpData] = useState<GoroutineDump | null>(null);
  const [selected, setSelected] = useState<GlobalInflightRow | null>(null);

  const loadDump = async () => {
    try {
      setDumpData(await goroutines.mutateAsync(agentId));
      setDumpOpen(true);
    } catch (error) {
      toast.error(formatErrorToast(error, tc("error")));
    }
  };

  return (
    <section className="rounded-md border">
      <div className="flex flex-wrap items-center justify-between gap-2 px-4 py-3">
        <h2 className="text-sm font-medium">{t("inflightTitle")}</h2>
        <div className="flex items-center gap-2">
          <Button type="button" variant="outline" size="sm" onClick={() => void refetch()} disabled={isFetching}>
            {isFetching ? <LoaderCircle data-icon="inline-start" className="animate-spin" /> : <RefreshCw data-icon="inline-start" />}
            {t("inflightRefresh")}
          </Button>
          <Button type="button" variant="outline" size="sm" onClick={() => void loadDump()} disabled={goroutines.isPending}>
            {goroutines.isPending ? <LoaderCircle data-icon="inline-start" className="animate-spin" /> : <RefreshCw data-icon="inline-start" />}
            {goroutines.isPending ? t("goroutineDumping") : t("goroutineDump")}
          </Button>
        </div>
      </div>
      <div className="border-t">
        <InflightTable
          rows={rows}
          emptyText={t("inflightEmpty")}
          onSelectRow={(row) => setSelected({ ...row, agent_id: agentId, agent_name: agentName } as GlobalInflightRow)}
          onInterrupt={(row) => interrupt.mutate(
            { agent_id: agentId, id: row.id },
            {
              onSuccess: () => toast.success(t("inflightInterrupted")),
              onError: (error) => toast.error(formatErrorToast(error, tc("error"))),
            },
          )}
        />
      </div>
      <InflightBlockDetail row={selected} onClose={() => setSelected(null)} />
      <GoroutineDumpDialog open={dumpOpen} onOpenChange={setDumpOpen} data={dumpData} />
    </section>
  );
}

function OverviewTab({ agent }: { agent: AgentDetail }) {
  const t = useTranslations("agents");
  const addresses = parseAddresses(agent.effective_http_addresses ?? agent.http_addresses);
  const tags = agent.tags.split(",").map((tag) => tag.trim()).filter(Boolean);
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <section className="rounded-md border">
        <div className="grid grid-cols-2 gap-x-8 gap-y-4 p-4 sm:grid-cols-4">
          <Stat label={t("agentId")}><CopyableText text={agent.agent_id} /></Stat>
          <Stat label={t("lastSeen")}><DateCell timestamp={agent.last_seen} relative /></Stat>
          <Stat label={t("tags")}>
            {tags.length > 0 ? <span className="flex flex-wrap gap-1">{tags.map((tag) => <Badge key={tag} variant="secondary">{tag}</Badge>)}</span> : "-"}
          </Stat>
          <Stat label={t("proxyUrl")}>{agent.proxy_url || "-"}</Stat>
        </div>
        {addresses.length > 0 ? (
          <div className="flex min-w-0 items-start gap-3 border-t px-4 py-3">
            <span className="shrink-0 text-xs leading-5 text-muted-foreground">{t("httpAddresses")}</span>
            <div className="flex min-w-0 flex-wrap gap-x-4 gap-y-1.5">
              {addresses.map((address, index) => (
                <span key={`${address.url}-${address.tag}-${index}`} className="inline-flex min-w-0 items-center gap-1.5">
                  <code className="max-w-full break-all rounded bg-muted px-1.5 py-0.5 text-xs">{address.url}</code>
                  {address.tag ? <Badge variant="outline">{address.tag}</Badge> : null}
                </span>
              ))}
            </div>
          </div>
        ) : null}
      </section>
    </div>
  );
}

function RuntimeTab({ agent }: { agent: AgentDetail }) {
  const t = useTranslations("agents");
  const runtime = agent.runtime;
  if (!runtime) {
    return <Empty><EmptyHeader><EmptyTitle>{t("noRuntime")}</EmptyTitle></EmptyHeader></Empty>;
  }
  const drift = runtime.master_version - runtime.version;
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <section className="rounded-md border p-4">
        <div className="grid grid-cols-2 gap-x-8 gap-y-4 sm:grid-cols-3 lg:grid-cols-5">
          <Stat label={t("uptime")}>{formatUptime(runtime.uptime)}</Stat>
          <Stat label={t("cachedTokens")}>{runtime.cached_tokens}</Stat>
          <Stat label={t("cachedChannels")}>{runtime.cached_channels}</Stat>
          <Stat label={t("cachedModels")}>{runtime.cached_models}</Stat>
          <Stat label={t("activeConnections")}>{runtime.active_connections}</Stat>
          <Stat label={t("pendingUsage")}>{runtime.pending_usage ?? 0}</Stat>
          <Stat label={t("version")}>{runtime.version}</Stat>
          <Stat label={t("masterVersion")}>{runtime.master_version}</Stat>
          <Stat label={t("versionDrift")}><Badge variant={drift === 0 ? "secondary" : "destructive"}>{drift}</Badge></Stat>
        </div>
      </section>
      {runtime.cache_stats ? (
        <section className="rounded-md border">
          <div className="border-b px-4 py-3"><h2 className="text-sm font-medium">{t("agentSectionTitle")}</h2></div>
          <div className="p-4"><CacheStatsTable data={runtime.cache_stats} mode="agent" /></div>
        </section>
      ) : null}
      <InflightSection agentId={agent.id} agentName={agent.name} />
    </div>
  );
}

export default function AgentDetailPage() {
  const tc = useTranslations("common");
  return (
    <Suspense fallback={<div className="flex flex-col gap-3 py-8" aria-label={tc("loading")}><Skeleton className="h-8 w-48" /><Skeleton className="h-56 w-full" /></div>}>
      <AgentDetailContent />
    </Suspense>
  );
}

function AgentDetailContent() {
  const searchParams = useSearchParams();
  const router = useRouter();
  const id = Number(searchParams.get("id"));
  const t = useTranslations("agents");
  const tc = useTranslations("common");
  const detail = useAgentDetail(id);
  const monitored = useAgentConnections(id, undefined, {
    enabled: Boolean(detail.data),
    initialSnapshot: detail.data?.connection,
  });
  const fullSync = useFullSyncAgents();

  const runFullSync = async () => {
    if (!detail.data) return;
    try {
      const result = await fullSync.mutateAsync({ agent_ids: [detail.data.agent_id] });
      const first = result.results[0];
      if (!first?.success) {
        toast.error(first?.error || tc("error"));
        return;
      }
      toast.success(`${t("fullSync")}: v${first.version}, ${formatDuration(first.duration_ms ?? 0)}`);
      await detail.refetch();
    } catch (error) {
      toast.error(formatErrorToast(error, tc("error")));
    }
  };

  if (detail.isError && !detail.data) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertTitle>{t("detailLoadFailed")}</AlertTitle>
        <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
          <span>{t("detailLoadFailedDescription")}</span>
          <Button type="button" variant="outline" size="sm" onClick={() => void detail.refetch()}>{t("retry")}</Button>
        </AlertDescription>
      </Alert>
    );
  }
  if (detail.isLoading || !detail.data) {
    return <div className="flex flex-col gap-3 py-8" aria-label={tc("loading")}><Skeleton className="h-8 w-48" /><Skeleton className="h-56 w-full" /></div>;
  }
  const agent = detail.data;
  const snapshot = monitored.data ?? agent.connection;
  const connectionStale = monitored.stale;
  const routeTargetsPage = monitored.routeTargetsPage ?? (
    routeTargetsPageMatchesSnapshot(agent.route_targets, snapshot)
      ? agent.route_targets
      : undefined
  );
  return (
    <div className="flex min-w-0 flex-col gap-4">
      {detail.isError ? (
        <Alert variant="destructive" role="alert">
          <AlertTitle>{t("detailLoadFailed")}</AlertTitle>
          <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
            <span>{t("detailLoadFailedDescription")}</span>
            <Button type="button" variant="outline" size="sm" onClick={() => void detail.refetch()}>{t("retry")}</Button>
          </AlertDescription>
        </Alert>
      ) : null}
      <header className="flex min-w-0 items-center gap-2">
        <Button type="button" variant="ghost" size="icon" className="shrink-0" aria-label={t("backToList")} onClick={() => router.push("/agents")}>
          <ArrowLeft data-icon="inline-start" />
        </Button>
        <h1 className="min-w-0 truncate text-lg font-semibold">{agent.name}</h1>
        <AgentConnectionStatus kind="control" value={snapshot.control} />
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="ml-auto shrink-0"
          onClick={() => void runFullSync()}
          disabled={connectionStale || fullSync.isPending || snapshot.control.state !== "connected"}
        >
          {fullSync.isPending ? <LoaderCircle data-icon="inline-start" className="animate-spin" /> : <RefreshCw data-icon="inline-start" />}
          {fullSync.isPending ? t("syncing") : t("fullSync")}
        </Button>
      </header>

      <Tabs defaultValue="overview" className="min-w-0">
        <TabsList>
          <TabsTrigger value="overview">{t("overview")}</TabsTrigger>
          <TabsTrigger value="connections">{t("connections")}</TabsTrigger>
          <TabsTrigger value="runtime">{t("runtime")}</TabsTrigger>
        </TabsList>
        <TabsContent value="overview" className="mt-4"><OverviewTab agent={agent} /></TabsContent>
        <TabsContent value="connections" className="mt-4">
          <AgentConnectionsPanel
            agentId={agent.id}
            snapshot={snapshot}
            initialRouteTargetsPage={routeTargetsPage}
            stale={connectionStale}
            loading={detail.isFetching || monitored.isFetching}
          />
        </TabsContent>
        <TabsContent value="runtime" className="mt-4"><RuntimeTab agent={agent} /></TabsContent>
      </Tabs>
    </div>
  );
}
