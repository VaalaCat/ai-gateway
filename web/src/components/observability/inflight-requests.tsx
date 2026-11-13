"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { CopyableText } from "@/components/business/copyable-text";
import { EntityLabel } from "@/components/business/entity-label";
import { Badge } from "@/components/ui/badge";
import { InflightConveyor } from "@/components/observability/inflight-conveyor";
import { InflightTable } from "@/components/business/inflight-table";
import { LiveTabHeader } from "@/components/monitoring/live-tab-header";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog";
import { useInterruptInflight } from "@/lib/api/agents";
import { ApiError } from "@/lib/api/client";
import { formatErrorToast } from "@/lib/api/error-toast";
import { inflightChannel, inflightKey, reconcileInflightRequests, type ObservedInflightRequest } from "@/lib/inflight";
import type { AllInflightResponse } from "@/lib/types";

interface InflightRequestsProps {
  title: string;
  data?: AllInflightResponse;
  dataUpdatedAt: number;
  error?: unknown;
  isFetching: boolean;
  paused: boolean;
  onPausedChange: (paused: boolean) => void;
  onRefresh: () => unknown;
  showAgent?: boolean;
}

/** 管理在途列表的快照、单条选择和中断确认，两个入口共用同一套交互。 */
export function InflightRequests({ title, data, dataUpdatedAt, error, isFetching, paused, onPausedChange, onRefresh, showAgent }: InflightRequestsProps) {
  const t = useTranslations("inflight");
  const tc = useTranslations("common");
  const to = useTranslations("observability");
  const interrupt = useInterruptInflight();
  const [observed, setObserved] = useState<{ at: number; requests: ObservedInflightRequest[]; selectedKey: string | null }>({ at: 0, requests: [], selectedKey: null });
  const [filter, setFilter] = useState("all");
  const [search, setSearch] = useState("");
  const [targetKey, setTargetKey] = useState<string | null>(null);
  const [cancellationRequested, setCancellationRequested] = useState<Set<string>>(new Set());
  const [unconfirmedInterrupts, setUnconfirmedInterrupts] = useState<Record<string, number>>({});

  // 1. 同一份记录拥有最新详情；仅成功返回的节点可以确认请求已离开。
  if (data && dataUpdatedAt !== observed.at) {
    setObserved({
      at: dataUpdatedAt,
      selectedKey: observed.selectedKey,
      requests: reconcileInflightRequests(observed.requests, data.requests, data.successful_agent_ids ?? [], observed.selectedKey, dataUpdatedAt),
    });
  }
  const stale = Boolean(error);
  const active = observed.requests.filter((request) => request.status !== "departed");
  const staleAgents = new Set(active.filter((request) => request.status === "stale").map((request) => request.row.agent_id));
  const currentCount = active.filter((request) => request.status === "current").length;
  const subtitle = !data ? undefined : stale || paused
    ? t("snapshotCount", { n: active.length })
    : t("counts", { current: currentCount, stale: active.length - currentCount });
  const matched = observed.requests.filter((request) => {
    const channel = inflightChannel(request.row);
    const terms = [request.row.view.user_id, `UID ${request.row.view.user_id}`, channel.name, channel.id, request.row.view.model_name, request.row.req_id];
    if (search.trim() && !terms.join(" ").toLowerCase().includes(search.trim().toLowerCase())) return false;
    switch (filter) {
      case "queued": return request.row.queued_ms > 0;
      case "streaming": return request.row.stage === "client_encode" || request.row.stage === "upstream_decode";
      case "stale": return stale || request.status === "stale";
      default: return true;
    }
  });
  const visible = observed.requests.filter((request) => matched.includes(request) || inflightKey(request.row) === observed.selectedKey);
  const unconfirmedKeys = new Set(observed.requests.filter((request) =>
    (unconfirmedInterrupts[inflightKey(request.row)] ?? -1) >= request.observedAt,
  ).map((request) => inflightKey(request.row)));
  const target = observed.requests.find((request) => inflightKey(request.row) === targetKey);
  const targetChannel = target ? inflightChannel(target.row) : null;
  const canInterruptTarget = Boolean(target && target.status === "current" && !stale && !paused && !cancellationRequested.has(targetKey!) && !unconfirmedKeys.has(targetKey!));

  // 2. 只发送一次中断；不把未命中与超时当成已经取消。
  const confirmInterrupt = async () => {
    if (!target || !canInterruptTarget || interrupt.isPending) return;
    const key = inflightKey(target.row);
    try {
      const result = await interrupt.mutateAsync({ agent_id: target.row.agent_id, id: target.row.id });
      if (result.interrupted) {
        setCancellationRequested((previous) => new Set(previous).add(key));
        toast.success(t("interruptSent"));
      } else {
        toast.info(t("interruptNotFound"));
      }
    } catch (error) {
      if ((error instanceof Error || error instanceof DOMException) && ["TimeoutError", "AbortError", "TypeError"].includes(error.name)
        || error instanceof ApiError && (error.status === 408 || error.status >= 500)) {
        setUnconfirmedInterrupts((previous) => ({ ...previous, [key]: Date.now() }));
        toast.warning(t("interruptUnknown"));
      } else {
        toast.error(formatErrorToast(error, tc("error")));
      }
    } finally {
      setTargetKey(null);
      void onRefresh();
    }
  };

  return <div className="flex min-w-0 flex-col gap-3">
    <LiveTabHeader title={title} subtitle={subtitle} paused={paused} onPausedChange={onPausedChange}
      isFetching={isFetching} error={error} hasData={Boolean(data)} dataUpdatedAt={dataUpdatedAt}
      failedAgents={data?.failed_agents} staleCount={staleAgents.size} onRefresh={() => { void onRefresh(); }} />
    <InflightConveyor rows={active.map((request) => request.row)} selectedKey={observed.selectedKey}
      paused={paused} stale={stale} staleKeys={new Set(active.filter((request) => request.status === "stale").map((request) => inflightKey(request.row)))}
      observedAt={new Map(active.map((request) => [inflightKey(request.row), request.observedAt]))}
      onSelect={(row) => {
        const key = inflightKey(row);
        setObserved((previous) => ({ ...previous, selectedKey: key, requests: previous.requests.filter((request) => request.status !== "departed") }));
        requestAnimationFrame(() => {
          const target = document.querySelector(`[data-testid="inflight-row-${key}"]`)?.closest("tr");
          target?.scrollIntoView({ block: "nearest" });
          target?.focus({ preventScroll: true });
        });
      }} />
    <div className="flex flex-wrap items-center gap-2">
      <div className="flex w-full min-w-0 items-center gap-2 md:w-auto md:max-w-md md:flex-1">
        <Input type="search" value={search} onChange={(event) => setSearch(event.target.value)} aria-label={t("search")} placeholder={t("search")} />
        {search ? <Button variant="outline" size="sm" onClick={() => setSearch("")}>{t("clearSearch")}</Button> : null}
      </div>
    <ToggleGroup type="single" value={filter} onValueChange={(value) => { if (value) setFilter(value); }} variant="outline" size="sm" className="max-w-full flex-wrap" aria-label={t("filterLabel")}>
      {["all", "queued", "streaming", "stale"].map((value) => <ToggleGroupItem key={value} value={value}>{t(`filter.${value}`)}</ToggleGroupItem>)}
    </ToggleGroup>
    </div>
    {matched.length === 0 && visible.length > 0 ? <p className="text-sm text-muted-foreground">{t("noMatchesSelected")}</p> : null}
    {!data && !error ? <div aria-label={tc("loading")} className="flex flex-col gap-2"><Skeleton className="h-16 w-full" /><Skeleton className="h-16 w-full" /></div> : data ? <div className="min-w-0">
      <InflightTable requests={visible} selectedKey={observed.selectedKey}
        onSelect={(key) => setObserved((previous) => ({ ...previous, selectedKey: previous.selectedKey === key ? null : key,
          requests: previous.requests.filter((request) => request.status !== "departed" || inflightKey(request.row) === key && previous.selectedKey !== key) }))}
        onInterrupt={setTargetKey} interruptPending={interrupt.isPending} cancellationRequested={cancellationRequested} unconfirmedInterrupts={unconfirmedInterrupts}
        stale={stale} paused={paused} showAgent={showAgent} emptyText={search.trim() || filter !== "all" ? t("noMatches") : data.failed_agents.length > 0 ? t("noCurrentData") : t("empty")} />
    </div> : null}
    <AlertDialog open={targetKey !== null} onOpenChange={(open) => { if (!open && !interrupt.isPending) setTargetKey(null); }}>
      <AlertDialogContent><AlertDialogHeader>
        <AlertDialogTitle>{t("interruptConfirm")}</AlertDialogTitle>
        <AlertDialogDescription>{t("interruptDescription")}
          {!canInterruptTarget ? <span className="mt-2 block">{t("interruptUnavailable")}</span> : null}
        </AlertDialogDescription>
        {target ? <div className="flex min-w-0 flex-col gap-2 text-sm">
          <div className="min-w-0 overflow-auto"><CopyableText text={target.row.req_id} /></div>
          <div className="flex items-center gap-2"><span className="text-muted-foreground">{to("detailUser")}</span><EntityLabel entity="user" scope="all" id={target.row.view.user_id > 0 ? target.row.view.user_id : undefined} /></div>
          <div className="flex flex-wrap items-center gap-2"><span className="text-muted-foreground">{to("detailChannel")}</span>{targetChannel?.id ? <>{targetChannel.private ? <Badge variant="secondary">BYOK</Badge> : null}<EntityLabel entity={targetChannel.private ? "byok-channel" : "channel"} scope="all" id={targetChannel.id} snapshotName={targetChannel.name} /></> : t("channelPending")}</div>
        </div> : null}
      </AlertDialogHeader><AlertDialogFooter>
        <AlertDialogCancel disabled={interrupt.isPending}>{tc("cancel")}</AlertDialogCancel>
        <AlertDialogAction variant="destructive" disabled={!canInterruptTarget || interrupt.isPending} onClick={(event) => { event.preventDefault(); void confirmInterrupt(); }}>{t(interrupt.isPending ? "interruptPending" : "interrupt")}</AlertDialogAction>
      </AlertDialogFooter></AlertDialogContent>
    </AlertDialog>
  </div>;
}
