"use client";

import { useState, type ReactNode } from "react";
import Link from "next/link";
import { useTranslations } from "next-intl";
import { CopyableText } from "@/components/business/copyable-text";
import { RateLimitSection } from "@/components/business/rate-limit-section";
import { FallbackChain } from "@/components/business/fallback-chain";
import { Button } from "@/components/ui/button";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { useLogs } from "@/lib/api/logs";
import { inflightChannel, type ObservedInflightRequest } from "@/lib/inflight";
import { formatDuration, formatFileSize, formatDate } from "@/lib/utils/format";
import { EntityLabel } from "@/components/business/entity-label";
import { ModelName } from "@/components/business/model-name";
import { DurationCell } from "@/components/business/duration-cell";
import { StreamBadge } from "@/components/business/status-badge";
import { Badge } from "@/components/ui/badge";
import type { InflightProgress } from "@/lib/types";

function ProgressFields({ progress, title }: { progress?: InflightProgress; title: string }) {
  const t = useTranslations("inflight");
  return <section className="flex min-w-0 flex-col gap-2">
    <h4 className="text-sm font-medium">{title}</h4>
    <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm tabular-nums">
      <div><dt className="text-xs text-muted-foreground">{t("upstreamBytes")}</dt><dd>{progress?.upstream_body_bytes == null ? "—" : formatFileSize(progress.upstream_body_bytes)}</dd></div>
      <div><dt className="text-xs text-muted-foreground">{t("clientBytes")}</dt><dd>{progress?.client_body_bytes == null ? "—" : formatFileSize(progress.client_body_bytes)}</dd></div>
      <div><dt className="text-xs text-muted-foreground">{t("upstreamIdle")}</dt><dd>{progress?.upstream_idle_ms == null ? "—" : formatDuration(progress.upstream_idle_ms)}</dd></div>
      <div><dt className="text-xs text-muted-foreground">{t("clientIdle")}</dt><dd>{progress?.client_idle_ms == null ? "—" : formatDuration(progress.client_idle_ms)}</dd></div>
    </dl>
  </section>;
}

/** 展示在途请求的进度、元信息、尝试链和日志查询入口。 */
export function InflightRequestDetail({ request, stale }: { request: ObservedInflightRequest; stale: boolean }) {
  const t = useTranslations("inflight");
  const to = useTranslations("observability");
  const ts = useTranslations("common.relayStage");
  const { row } = request;
  const channel = inflightChannel(row);
  const view = row.view;
  const [logsChecked, setLogsChecked] = useState(false);
  const logs = useLogs({ request_id: row.req_id, page_size: 1 }, { enabled: false, retry: false });
  const fields: [string, ReactNode][] = [
    [to("detailAgent"), <EntityLabel key="agent" entity="agent" id={view.route_source_agent_id} snapshotName={row.agent_name} showId={false} />],
    [to("stage"), ts.has(row.stage) ? ts(row.stage) : t.has(`stages.${row.stage}`) ? t(`stages.${row.stage}`) : row.stage || "—"],
    [to("detailUser"), view.user_id > 0 ? <EntityLabel key="user" entity="user" scope="all" id={view.user_id} /> : "—"],
    [to("detailChannel"), channel.id ? <span key="channel" className="inline-flex flex-wrap items-center gap-1">{channel.private ? <Badge variant="secondary">BYOK</Badge> : null}<EntityLabel entity={channel.private ? "byok-channel" : "channel"} scope="all" id={channel.id} snapshotName={channel.name} /></span> : "—"],
    [to("detailModel"), <ModelName key="model" name={view.model_name || "—"} />],
    [to("detailUpstreamModel"), row.current_attempt?.real_model || view.upstream_model ? <ModelName key="upstream" name={row.current_attempt?.real_model || view.upstream_model!} /> : "—"],
    [to("detailInbound"), view.inbound_protocol || "—"],
    [to("detailOutbound"), view.outbound_protocol || "—"],
    [to("detailStream"), <StreamBadge key="stream" isStream={view.is_stream} />],
    [to("detailElapsed"), <DurationCell key="elapsed" ms={row.elapsed_ms} />],
    [to("detailPromptTokens"), (view.prompt_tokens ?? 0) > 0 ? view.prompt_tokens : "—"],
    [to("detailCompletionTokens"), (view.completion_tokens ?? 0) > 0 ? view.completion_tokens : "—"],
    [to("detailFirstResponse"), (view.first_response_ms ?? 0) > 0 ? <DurationCell key="first" ms={view.first_response_ms!} /> : "—"],
    [to("detailRouting"), view.routing_name || "—"],
    [to("detailClientIp"), view.client_ip || "—"],
  ];

  // 1. 在同一行下保留最后快照，明确区分已离开与未获取到最新数据。
  return <div className="flex min-w-0 flex-col gap-4 whitespace-normal p-4" data-testid="inflight-detail">
    {request.status === "departed" || stale ? <Alert><AlertDescription>
      {request.status === "departed" ? t("departedDescription") : t("staleDescription")}
      {" "}{t("observedAt", { time: formatDate(request.observedAt / 1000) })}
    </AlertDescription></Alert> : null}
    <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
      <h3 className="text-sm font-semibold">{to("detailTitle")}</h3>
      <div className="min-w-0 overflow-x-auto"><CopyableText text={row.req_id} /></div>
    </div>
    <div className="grid min-w-0 gap-4 md:grid-cols-2">
      <ProgressFields title={t("requestProgress")} progress={row.progress} />
      {row.attempt_progress ? <ProgressFields title={t("attemptProgress")} progress={row.attempt_progress} /> : null}
    </div>
    <p className="text-xs text-muted-foreground">{t("progressHelp")}</p>
    <dl className="grid min-w-0 grid-cols-2 gap-3 text-sm md:grid-cols-3 xl:grid-cols-4">
      {fields.map(([label, value]) => <div key={label} className="min-w-0"><dt className="text-xs text-muted-foreground">{label}</dt><dd className="break-all tabular-nums">{value}</dd></div>)}
    </dl>
    <p className="text-xs text-muted-foreground">{t("usageHelp")}</p>
    {row.queued_ms > 0 ? <p className="text-sm">{to("detailQueued")}: {formatDuration(row.queued_ms)} · {row.queued_reason || "—"}</p> : null}
    {view.rate_limit_decision ? <RateLimitSection decision={view.rate_limit_decision} waitMs={view.rate_limit_wait_ms} reason={view.rate_limit_reason} hits={view.rate_limit_hits} /> : null}
    {(request.status !== "current" || stale) && row.current_attempt ? <p className="text-sm">{t("lastAttempt")}: <EntityLabel entity={channel.private ? "byok-channel" : "channel"} scope="all" id={row.current_attempt.channel_id} snapshotName={row.current_attempt.channel_name} /> · {row.current_attempt.source} · <ModelName name={row.current_attempt.real_model} /></p> : null}
    {(view.fallback_chain?.length || request.status === "current" && !stale && row.current_attempt) ? <FallbackChain chain={view.fallback_chain ?? []} requestId={row.req_id} pending={request.status !== "current" || stale ? undefined : row.current_attempt} /> : null}
    {row.req_id ? <div className="flex flex-wrap items-center gap-2">
      <Button variant="outline" size="sm" disabled={logs.isFetching} onClick={() => { setLogsChecked(true); void logs.refetch(); }}>{t("findLogs")}</Button>
      {logsChecked && !logs.isFetching ? logs.isError ? <span role="alert" className="text-sm text-destructive">{t("logsFailed")}</span> : logs.data?.data.length ? <Button variant="link" asChild><Link href={`/logs?request_id=${encodeURIComponent(row.req_id)}`}>{t("openLogs")}</Link></Button> : <span className="text-sm text-muted-foreground">{t("noLogs")}</span> : null}
    </div> : null}
  </div>;
}
