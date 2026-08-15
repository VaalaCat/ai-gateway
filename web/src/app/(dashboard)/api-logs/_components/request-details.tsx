"use client";

import { useState, type ReactNode } from "react";
import { useTranslations } from "next-intl";
import { ChevronRight, ScanSearch } from "lucide-react";

import { DateCell } from "@/components/business/date-cell";
import { DurationCell } from "@/components/business/duration-cell";
import { EntityLabel } from "@/components/business/entity-label";
import { HTTPStatusBadge, ProtocolBadge } from "@/components/business/api-badges";
import { RateLimitSection } from "@/components/business/rate-limit-section";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { ApiError } from "@/lib/api/client";
import { useAPIRequestTrace, type APIRequestLog, type APIRequestLogScope } from "@/lib/api/api-logs";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";
import { formatMoneyCompact } from "@/lib/utils/format";
import { TraceCaptureDisplay } from "./trace-capture-display";
import { APILogTokenIdentity } from "./token-identity";

function DetailItem({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-0.5 break-words text-sm font-medium">{children}</div>
    </div>
  );
}

function RequestIDValue({ value }: { value: string }) {
  const t = useTranslations("common");

  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            aria-label={`${t("copy")}: ${value}`}
            className="block w-full max-w-56 truncate text-left font-mono text-xs underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            onClick={() => void copyTextWithFeedback(value, {
              success: t("copied"),
              error: t("copyFailed"),
            })}
          >
            {value}
          </button>
        </TooltipTrigger>
        <TooltipContent className="max-w-md break-all font-mono">{value}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

function valueOrDash(value: string | number | null | undefined) {
  return value === "" || value == null ? "-" : String(value);
}

function bytes(value: number) {
  return `${new Intl.NumberFormat().format(value)} B`;
}

function TraceError({ error }: { error: unknown }) {
  const t = useTranslations("apiLogs");
  const key = error instanceof ApiError && error.status === 404 ? "traceNotFound" : "traceLoadFailed";
  return (
    <Alert variant="destructive">
      <AlertTitle>{t(key)}</AlertTitle>
      <AlertDescription>{t(`${key}Description`)}</AlertDescription>
    </Alert>
  );
}

function APIRequestTraceDetails({ requestID, scope }: { requestID: string; scope: APIRequestLogScope }) {
  const t = useTranslations("apiLogs");
  const [loaded, setLoaded] = useState(false);
  const trace = useAPIRequestTrace(loaded ? requestID : null, scope);

  if (!loaded) {
    return (
      <Button type="button" variant="outline" size="sm" className="self-start" onClick={() => setLoaded(true)}>
        <ScanSearch data-icon="inline-start" />
        {t("loadTrace")}
      </Button>
    );
  }
  if (trace.isLoading) {
    return (
      <div className="flex flex-col gap-3" role="status">
        <span className="text-sm text-muted-foreground">{t("traceLoading")}</span>
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }
  if (trace.error || !trace.data) return <TraceError error={trace.error} />;

  return (
    <div className="flex w-full min-w-0 flex-col gap-3">
      <TraceCaptureDisplay
        title={t("sourceRequestCapture")}
        headers={trace.data.source_request_headers}
        trailers={trace.data.source_request_trailers}
        headersTruncated={trace.data.source_request_headers_truncated}
        trailersTruncated={trace.data.source_request_trailers_truncated}
        body={trace.data.source_request_body}
      />
      <TraceCaptureDisplay
        title={t("requestCapture")}
        headers={trace.data.request_headers}
        trailers={trace.data.request_trailers}
        headersTruncated={trace.data.request_headers_truncated}
        trailersTruncated={trace.data.request_trailers_truncated}
        body={trace.data.request_body}
      />
      <TraceCaptureDisplay
        title={t("responseCapture")}
        headers={trace.data.response_headers}
        trailers={trace.data.response_trailers}
        headersTruncated={trace.data.response_headers_truncated}
        trailersTruncated={trace.data.response_trailers_truncated}
        body={trace.data.response_body}
      />
    </div>
  );
}

export function APIRequestDetails({
  request,
  showInternal = true,
}: {
  request: APIRequestLog;
  showInternal?: boolean;
}) {
  const t = useTranslations("apiLogs");
  const dispatch = !request.provider_dispatch_known
    ? t("unknown")
    : request.provider_dispatched
      ? t("yes")
      : t("no");

  return (
    <div className="flex min-w-0 flex-col gap-4 p-1 text-body">
      <div
        data-testid="api-log-result-summary"
        className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-md bg-muted/40 px-3 py-2.5"
      >
        <HTTPStatusBadge statusCode={request.status_code} />
        <code className="text-xs font-semibold">{valueOrDash(request.method)}</code>
        <ProtocolBadge protocol={request.protocol} />
        <span className="text-sm tabular-nums">
          <span className="text-muted-foreground">{t("duration")}: </span>
          <span className="font-medium"><DurationCell ms={request.duration_ms} /></span>
        </span>
        <span className="text-sm tabular-nums">
          <span className="text-muted-foreground">{t("firstByte")}: </span>
          <span className="font-medium"><DurationCell ms={request.first_byte_ms} /></span>
        </span>
      </div>

      {request.error_stage || request.error_code || request.service_missing_at_settlement ? (
        <Alert variant="destructive">
          <AlertTitle>{t("failureDetails")}</AlertTitle>
          <AlertDescription className="flex flex-wrap gap-x-6 gap-y-1">
            <span>{t("errorStage")}: {valueOrDash(request.error_stage)}</span>
            <span>{t("errorCode")}: {valueOrDash(request.error_code)}</span>
            {request.service_missing_at_settlement ? <span>{t("serviceMissingAtSettlement")}</span> : null}
          </AlertDescription>
        </Alert>
      ) : null}

      <div
        data-testid="api-log-route-chain"
        className="flex min-w-0 flex-wrap items-center gap-2 border-y border-border/60 py-3"
      >
        <span className="text-xs text-muted-foreground">{t("routingSnapshot")}</span>
        <span className="min-w-0 break-words text-sm font-semibold">{valueOrDash(request.api_service_name)}</span>
        <ChevronRight className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
        <span className="min-w-0 break-words text-sm font-medium">{valueOrDash(request.api_route_name)}</span>
        {showInternal ? (
          <>
            <ChevronRight className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span className="min-w-0 break-words text-sm font-medium">{valueOrDash(request.api_upstream_name)}</span>
          </>
        ) : null}
      </div>

      <section aria-label={t("requestIdentity")}>
        <div className="grid min-w-0 grid-cols-2 gap-x-6 gap-y-3 md:grid-cols-3 xl:grid-cols-4">
          <DetailItem label={t("requestID")}><RequestIDValue value={request.request_id} /></DetailItem>
          {showInternal ? <DetailItem label={t("userID")}>
            <EntityLabel entity="user" id={request.user_id ?? 0} />
          </DetailItem> : null}
          <DetailItem label={t("token")}>
            <APILogTokenIdentity tokenID={request.token_id} tokenName={request.token_name} />
          </DetailItem>
          {showInternal ? <DetailItem label={t("clientIP")}><code className="text-xs">{valueOrDash(request.client_ip)}</code></DetailItem> : null}
          <DetailItem label={t("createdAt")}><DateCell timestamp={request.created_at} /></DetailItem>
          <DetailItem label={t("subpath")}><code className="text-xs">{valueOrDash(request.subpath)}</code></DetailItem>
          {showInternal ? <>
            <DetailItem label={t("sourceAgent")}>{valueOrDash(request.source_agent_id)}</DetailItem>
            <DetailItem label={t("executionAgent")}>{valueOrDash(request.execution_agent_id)}</DetailItem>
            <DetailItem label={t("agentRouteID")}>{valueOrDash(request.agent_route_id)}</DetailItem>
            <DetailItem label={t("agentRoutePath")}><code className="text-xs">{valueOrDash(request.agent_route_path)}</code></DetailItem>
          </> : null}
          <DetailItem label={t("requestBytes")}>{bytes(request.request_bytes)}</DetailItem>
          <DetailItem label={t("responseBytes")}>{bytes(request.response_bytes)}</DetailItem>
          {request.websocket_close_code != null ? (
            <DetailItem label={t("websocketCloseCode")}>{request.websocket_close_code}</DetailItem>
          ) : null}
          {showInternal ? <DetailItem label={t("providerDispatch")}>{dispatch}</DetailItem> : null}
          <DetailItem label={t("quotaGateDecision")}>
            <Badge variant="secondary">{valueOrDash(request.quota_gate_decision)}</Badge>
          </DetailItem>
          <DetailItem label={t("unitPrice")}>{formatMoneyCompact(request.unit_price)}</DetailItem>
          <DetailItem label={t("totalCost")}>{formatMoneyCompact(request.total_cost)}</DetailItem>
        </div>
      </section>

      {request.rate_limit_decision ? (
        <RateLimitSection
          decision={request.rate_limit_decision}
          waitMs={request.rate_limit_wait_ms}
          reason={showInternal ? request.rate_limit_reason : undefined}
          hits={showInternal ? request.rate_limit_hits : undefined}
        />
      ) : null}

      <section className="flex min-w-0 flex-col gap-3 border-t border-border/60 pt-4">
        <h3 className="text-sm font-semibold">{t("trace")}</h3>
        <APIRequestTraceDetails requestID={request.request_id} scope={showInternal ? "admin" : "portal"} />
      </section>
    </div>
  );
}
