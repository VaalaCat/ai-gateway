"use client";

import { useTranslations } from "next-intl";

import { TraceCaptureBadge } from "@/components/business/api-badges";
import { TraceContentBlock } from "@/components/business/trace-content-block";
import { Badge } from "@/components/ui/badge";
import type { APIBodyCapture } from "@/lib/api/api-logs";

interface TraceCaptureDisplayProps {
  title: string;
  headers?: Record<string, string[]>;
  trailers?: Record<string, string[]>;
  headersTruncated?: boolean;
  trailersTruncated?: boolean;
  body?: APIBodyCapture;
}

const SKIP_REASON_KEYS: Record<string, string> = {
  trace_headers_only: "skipReason.traceHeadersOnly",
  content_encoded: "skipReason.contentEncoded",
  content_encoding: "skipReason.contentEncoded",
  multipart: "skipReason.multipart",
  binary_content_type: "skipReason.binaryContentType",
  binary_detected: "skipReason.binaryDetected",
  websocket: "skipReason.websocket",
  capture_read_failed: "skipReason.captureReadFailed",
};

function hasEntries(value?: Record<string, string[]>): value is Record<string, string[]> {
  return Boolean(value && Object.keys(value).length > 0);
}

function captureStateOf(body: APIBodyCapture) {
  if (body.status === "skipped") return "skipped";
  if (body.status === "unavailable") return "unavailable";
  if (body.truncated) return "truncated";
  if (body.captured && typeof body.captured_bytes === "number" && body.captured_bytes > 0) {
    return "captured";
  }
  return "empty";
}

function JSONBlock({ label, value }: { label: string; value: Record<string, string[]> }) {
  return (
    <div data-slot="trace-capture-json" className="flex min-w-0 flex-col gap-1">
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
      <TraceContentBlock content={JSON.stringify(value)} className="rounded-md bg-muted p-2" />
    </div>
  );
}

function BodyCapture({ body }: { body: APIBodyCapture }) {
  const t = useTranslations("apiLogs");
  const state = captureStateOf(body);
  const hasByteCounts = typeof body.captured_bytes === "number" && typeof body.total_bytes === "number";
  const showsData = state !== "skipped" && state !== "unavailable" && Boolean(body.data);
  const reason = body.skip_reason
    ? SKIP_REASON_KEYS[body.skip_reason]
      ? t(SKIP_REASON_KEYS[body.skip_reason])
      : body.skip_reason
    : null;

  return (
    <div data-slot="trace-capture-body" className="flex min-w-0 flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xs font-medium text-muted-foreground">{t("body")}</span>
        <TraceCaptureBadge status={state} />
        {state === "skipped" && reason ? <span className="text-xs text-muted-foreground">{reason}</span> : null}
        {hasByteCounts ? (
          <span className="font-mono text-xs text-muted-foreground">
            {body.captured_bytes} / {body.total_bytes}
          </span>
        ) : null}
      </div>
      {showsData ? <TraceContentBlock content={body.data ?? ""} className="rounded-md bg-muted p-2" /> : null}
    </div>
  );
}

export function TraceCaptureDisplay({
  title,
  headers,
  trailers,
  headersTruncated,
  trailersTruncated,
  body,
}: TraceCaptureDisplayProps) {
  const t = useTranslations("apiLogs");

  return (
    <section className="flex w-full min-w-0 flex-col gap-3 rounded-md border p-3">
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="font-medium">{title}</h3>
        {headersTruncated ? <Badge variant="secondary">{t("headersTruncated")}</Badge> : null}
        {trailersTruncated ? <Badge variant="secondary">{t("trailersTruncated")}</Badge> : null}
      </div>
      {hasEntries(headers) ? <JSONBlock label={t("headers")} value={headers} /> : null}
      {hasEntries(trailers) ? <JSONBlock label={t("trailers")} value={trailers} /> : null}
      {body ? <BodyCapture body={body} /> : null}
    </section>
  );
}
