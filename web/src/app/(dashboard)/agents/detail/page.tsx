"use client";

import { Suspense, useState } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { ArrowLeft, RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { OnlineBadge } from "@/components/business/status-badge";
import { DateCell } from "@/components/business/date-cell";
import { CopyableText } from "@/components/business/copyable-text";
import { CacheStatsTable } from "@/components/business/cache-stats-table";

import { useAgentDetail, useConnectivityReport, useCheckConnectivity, useFullSyncAgents, useAgentInflight, useAgentGoroutines, useInterruptInflight } from "@/lib/api/agents";
import type { GoroutineDump } from "@/lib/api/agents";
import { InflightTable } from "@/components/business/inflight-table";
import { formatErrorToast } from "@/lib/api/error-toast";
import { formatDuration, formatUptime } from "@/lib/utils/format";
import type { AgentAddress } from "@/lib/types";

function parseAddresses(raw: string): AgentAddress[] {
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) return parsed;
  } catch { /* ignore */ }
  return [];
}

function Stat({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0">
      <div className="text-xs text-muted-foreground truncate">{label}</div>
      <div className="text-sm font-medium truncate mt-0.5">{children}</div>
    </div>
  );
}

function GoroutineDumpDialog({
  open,
  onOpenChange,
  data,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  data: GoroutineDump | null;
}) {
  const t = useTranslations("agents");
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl max-h-[80vh] flex flex-col">
        <DialogHeader>
          <DialogTitle>
            {data ? t("goroutineDumpTitle", { count: data.count }) : t("goroutineDump")}
          </DialogTitle>
        </DialogHeader>
        <div className="flex-1 overflow-auto min-h-0">
          <pre className="text-xs whitespace-pre-wrap break-all p-2 bg-muted rounded">
            {data?.dump ?? ""}
          </pre>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function InflightSection({ agentId }: { agentId: number }) {
  const t = useTranslations("agents");
  const tc = useTranslations("common");
  const { data: rows = [], isFetching, refetch } = useAgentInflight(agentId);
  const goroutinesMutation = useAgentGoroutines();
  const interrupt = useInterruptInflight();
  const [dumpOpen, setDumpOpen] = useState(false);
  const [dumpData, setDumpData] = useState<GoroutineDump | null>(null);

  const handleGoroutineDump = async () => {
    try {
      const result = await goroutinesMutation.mutateAsync(agentId);
      setDumpData(result);
      setDumpOpen(true);
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  return (
    <div className="rounded-md border">
      <div className="flex items-center justify-between px-4 py-3">
        <h2 className="text-sm font-medium">{t("inflightTitle")}</h2>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            className="h-7 text-xs"
            onClick={() => refetch()}
            disabled={isFetching}
          >
            <RefreshCw className={`mr-1 size-3 ${isFetching ? "animate-spin" : ""}`} />
            {t("inflightRefresh")}
          </Button>
          <Button
            variant="outline"
            size="sm"
            className="h-7 text-xs"
            onClick={handleGoroutineDump}
            disabled={goroutinesMutation.isPending}
          >
            <RefreshCw className={`mr-1 size-3 ${goroutinesMutation.isPending ? "animate-spin" : ""}`} />
            {goroutinesMutation.isPending ? t("goroutineDumping") : t("goroutineDump")}
          </Button>
        </div>
      </div>
      <div className="border-t">
        <InflightTable
          rows={rows}
          emptyText={t("inflightEmpty")}
          onInterrupt={(row) =>
            interrupt.mutate(
              { agent_id: agentId, id: row.id },
              {
                onSuccess: () => { toast.success(t("inflightInterrupted")); },
                onError: (e) => toast.error(formatErrorToast(e, tc("error"))),
              },
            )
          }
        />
      </div>
      <GoroutineDumpDialog open={dumpOpen} onOpenChange={setDumpOpen} data={dumpData} />
    </div>
  );
}

export default function AgentDetailPage() {
  return (
    <Suspense fallback={<div className="flex items-center justify-center py-12 text-muted-foreground">Loading...</div>}>
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

  const { data: agent, isLoading, refetch } = useAgentDetail(id);
  const { data: connectivity } = useConnectivityReport(id);
  const checkMutation = useCheckConnectivity();
  const fullSyncMutation = useFullSyncAgents();

  const handleCheck = async () => {
    try {
      await checkMutation.mutateAsync(id);
      toast.success(tc("success"));
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  const handleFullSync = async () => {
    if (!agent) return;
    try {
      const result = await fullSyncMutation.mutateAsync({ agent_ids: [agent.agent_id] });
      const r = result.results[0];
      if (r?.success) {
        toast.success(`${t("fullSync")}: v${r.version}, ${formatDuration(r.duration_ms ?? 0)}`);
        refetch();
      } else {
        toast.error(r?.error || tc("error"));
      }
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  if (isLoading || !agent) {
    return (
      <div className="flex items-center justify-center py-12 text-muted-foreground">
        {tc("loading")}
      </div>
    );
  }

  const isOnline = agent.last_seen > 0 && (Date.now() / 1000 - agent.last_seen) < 120;
  const addresses = parseAddresses(agent.effective_http_addresses || agent.http_addresses);
  const tags = agent.tags ? agent.tags.split(",").map((s: string) => s.trim()).filter(Boolean) : [];
  const rt = agent.runtime;
  const drift = rt ? rt.master_version - rt.version : 0;

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center gap-2">
        <Button variant="ghost" size="icon" className="size-8 shrink-0" onClick={() => router.push("/agents")}>
          <ArrowLeft className="size-4" />
        </Button>
        <h1 className="text-lg font-semibold truncate">{agent.name}</h1>
        <OnlineBadge lastSeen={agent.last_seen} />
        <Button
          variant="outline"
          size="sm"
          className="h-7 text-xs ml-auto"
          onClick={handleFullSync}
          disabled={fullSyncMutation.isPending || !isOnline}
        >
          <RefreshCw className={`mr-1 size-3 ${fullSyncMutation.isPending ? "animate-spin" : ""}`} />
          {fullSyncMutation.isPending ? t("syncing") : t("fullSync")}
        </Button>
      </div>

      {/* Info + Runtime — single bordered panel */}
      <div className="rounded-md border">
        <div className="grid grid-cols-3 sm:grid-cols-4 md:grid-cols-6 gap-x-8 gap-y-3 p-4">
          <Stat label={t("agentId")}>
            <CopyableText text={agent.agent_id} />
          </Stat>
          <Stat label={t("lastSeen")}>
            <DateCell timestamp={agent.last_seen} relative />
          </Stat>
          <Stat label={t("tags")}>
            {tags.length > 0 ? (
              <span className="flex flex-wrap gap-1">
                {tags.map((tag: string) => <Badge key={tag} variant="secondary" className="text-xs px-1.5 py-0">{tag}</Badge>)}
              </span>
            ) : "-"}
          </Stat>
          <Stat label={t("proxyUrl")}>{agent.proxy_url || "-"}</Stat>
          {rt && (
            <>
              <Stat label={t("uptime")}>{formatUptime(rt.uptime)}</Stat>
              <Stat label={t("cachedTokens")}>{rt.cached_tokens}</Stat>
              <Stat label={t("cachedChannels")}>{rt.cached_channels}</Stat>
              <Stat label={t("cachedModels")}>{rt.cached_models}</Stat>
              <Stat label={t("version")}>{rt.version}</Stat>
              <Stat label={t("masterVersion")}>{rt.master_version}</Stat>
              <Stat label={t("versionDrift")}>
                {drift === 0
                  ? <Badge variant="secondary" className="text-xs px-1.5 py-0">0</Badge>
                  : <Badge variant="destructive" className="text-xs px-1.5 py-0">{drift}</Badge>}
              </Stat>
              <Stat label={t("activeConnections")}>{rt.active_connections}</Stat>
            </>
          )}
        </div>
        {addresses.length > 0 && (
          <div className="border-t px-4 py-3 flex items-start gap-3">
            <span className="text-xs text-muted-foreground shrink-0 leading-5">{t("httpAddresses")}</span>
            <div className="flex flex-wrap gap-x-4 gap-y-1.5 min-w-0">
              {addresses.map((addr, i) => (
                <span key={i} className="inline-flex items-center gap-1.5">
                  <code className="text-xs bg-muted rounded px-1.5 py-0.5 break-all">{addr.url}</code>
                  {addr.tag && <Badge variant="outline" className="text-xs px-1.5 py-0 shrink-0">{addr.tag}</Badge>}
                </span>
              ))}
            </div>
          </div>
        )}
      </div>

      {/* Cache — matching bordered panel */}
      {rt?.cache_stats && (
        <div className="rounded-md border">
          <div className="px-4 py-3 border-b">
            <h2 className="text-sm font-medium">{t("agentSectionTitle")}</h2>
          </div>
          <div className="p-4">
            <CacheStatsTable data={rt.cache_stats} mode="agent" />
          </div>
        </div>
      )}

      {/* In-Flight Requests */}
      <InflightSection agentId={id} />

      {/* Connectivity — matching bordered panel */}
      <div className="rounded-md border">
        <div className="flex items-center justify-between px-4 py-3">
          <h2 className="text-sm font-medium">{t("connectivity")}</h2>
          <div className="flex items-center gap-3">
            {connectivity && connectivity.checked_at > 0 && (
              <span className="text-xs text-muted-foreground">
                {t("checkedAt")}: <DateCell timestamp={connectivity.checked_at} relative />
              </span>
            )}
            <Button
              variant="outline"
              size="sm"
              className="h-7 text-xs"
              onClick={handleCheck}
              disabled={checkMutation.isPending}
            >
              <RefreshCw className={`mr-1 size-3 ${checkMutation.isPending ? "animate-spin" : ""}`} />
              {checkMutation.isPending ? t("checking") : t("checkConnectivity")}
            </Button>
          </div>
        </div>
        {connectivity && connectivity.checked_at > 0 && connectivity.results && connectivity.results.length > 0 ? (
          <div className="border-t">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="h-8">{t("targetAgent")}</TableHead>
                  <TableHead className="h-8">{t("address")}</TableHead>
                  <TableHead className="h-8">{tc("status")}</TableHead>
                  <TableHead className="h-8">{t("latency")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {connectivity.results.flatMap((cr) =>
                  cr.results.map((r, i) => (
                    <TableRow key={`${cr.target_agent_id}-${i}`}>
                      {i === 0 ? (
                        <TableCell rowSpan={cr.results.length} className="py-1.5">
                          <div className="text-sm font-medium">{cr.target_name}</div>
                          <div className="text-xs text-muted-foreground">{cr.target_agent_id}</div>
                        </TableCell>
                      ) : null}
                      <TableCell className="py-1.5">
                        <code className="text-xs">{r.url}</code>
                        {r.tag && <Badge variant="outline" className="ml-1.5 text-xs px-1.5 py-0">{r.tag}</Badge>}
                      </TableCell>
                      <TableCell className="py-1.5">
                        {r.reachable ? (
                          <Badge className="bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200 text-xs px-1.5 py-0">
                            {t("reachable")}
                          </Badge>
                        ) : (
                          <Badge variant="destructive" className="text-xs px-1.5 py-0">{t("unreachable")}</Badge>
                        )}
                      </TableCell>
                      <TableCell className="py-1.5">
                        {r.reachable ? formatDuration(r.latency_ms) : r.error || "-"}
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </div>
        ) : (
          <div className="border-t px-4 py-3">
            <p className="text-xs text-muted-foreground">{t("noResults")}</p>
          </div>
        )}
      </div>
    </div>
  );
}
