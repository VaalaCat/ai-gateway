"use client";

import { useTranslations } from "next-intl";
import { RefreshCw } from "lucide-react";
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
import { OnlineBadge } from "@/components/business/status-badge";
import { DateCell } from "@/components/business/date-cell";
import { CopyableText } from "@/components/business/copyable-text";

import { useAgentDetail, useConnectivityReport, useCheckConnectivity } from "@/lib/api/agents";
import { formatErrorToast } from "@/lib/api/error-toast";
import { formatDuration, formatUptime } from "@/lib/utils/format";
import type { Agent, AgentAddress } from "@/lib/types";

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

interface AgentExpandedRowProps {
  agent: Agent;
}

export function AgentExpandedRow({ agent }: AgentExpandedRowProps) {
  const t = useTranslations("agents");
  const tc = useTranslations("common");

  const { data: detail, isLoading } = useAgentDetail(agent.id);
  const { data: connectivity } = useConnectivityReport(agent.id);
  const checkMutation = useCheckConnectivity();

  const handleCheck = async () => {
    try {
      await checkMutation.mutateAsync(agent.id);
      toast.success(tc("success"));
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  if (isLoading || !detail) {
    return (
      <div className="flex items-center justify-center py-6 text-sm text-muted-foreground">
        {t("loadingDetail")}
      </div>
    );
  }

  const addresses = parseAddresses(detail.effective_http_addresses || detail.http_addresses);
  const tags = detail.tags ? detail.tags.split(",").map((s: string) => s.trim()).filter(Boolean) : [];
  const rt = detail.runtime;
  const drift = rt ? rt.master_version - rt.version : 0;

  return (
    <div className="space-y-3">
      {/* Info + Runtime panel */}
      <div className="rounded-md border">
        <div className="grid grid-cols-3 sm:grid-cols-4 md:grid-cols-6 gap-x-8 gap-y-3 p-4">
          <Stat label={t("agentId")}>
            <CopyableText text={detail.agent_id} />
          </Stat>
          <Stat label={t("lastSeen")}>
            <DateCell timestamp={detail.last_seen} relative />
          </Stat>
          <Stat label={tc("status")}>
            <OnlineBadge lastSeen={detail.last_seen} />
          </Stat>
          <Stat label={t("tags")}>
            {tags.length > 0 ? (
              <span className="flex flex-wrap gap-1">
                {tags.map((tag: string) => <Badge key={tag} variant="secondary" className="text-xs px-1.5 py-0">{tag}</Badge>)}
              </span>
            ) : "-"}
          </Stat>
          <Stat label={t("proxyUrl")}>{detail.proxy_url || "-"}</Stat>
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

      {/* Connectivity panel */}
      <div className="rounded-md border">
        <div className="flex items-center justify-between px-4 py-3">
          <h3 className="text-sm font-medium">{t("connectivity")}</h3>
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
            <Table className="text-body">
              <TableHeader>
                <TableRow>
                  <TableHead className="h-8 text-xs">{t("targetAgent")}</TableHead>
                  <TableHead className="h-8 text-xs">{t("address")}</TableHead>
                  <TableHead className="h-8 text-xs">{tc("status")}</TableHead>
                  <TableHead className="h-8 text-xs">{t("latency")}</TableHead>
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
                      <TableCell className="py-1.5 text-xs">
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
