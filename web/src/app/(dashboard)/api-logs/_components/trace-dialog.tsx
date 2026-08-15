"use client";

import { useRef } from "react";
import { useTranslations } from "next-intl";

import { DateCell } from "@/components/business/date-cell";
import { DurationCell } from "@/components/business/duration-cell";
import { HTTPStatusBadge, ProtocolBadge } from "@/components/business/api-badges";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Skeleton } from "@/components/ui/skeleton";
import { isLogDatabaseUnavailable, useAPIRequestTrace, type APIRequestLog } from "@/lib/api/api-logs";
import { ApiError } from "@/lib/api/client";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";
import { TraceCaptureDisplay } from "./trace-capture-display";

interface TraceDialogProps {
  request: APIRequestLog | null;
  onOpenChange: (open: boolean) => void;
}

function TraceError({ error }: { error: unknown }) {
  const t = useTranslations("apiLogs");
  const key = error instanceof ApiError && error.status === 404
    ? "traceNotFound"
    : isLogDatabaseUnavailable(error)
      ? "logUnavailable"
      : "traceLoadFailed";

  return (
    <Alert variant="destructive">
      <AlertTitle>{t(key)}</AlertTitle>
      <AlertDescription>{t(`${key}Description`)}</AlertDescription>
    </Alert>
  );
}

export function TraceDialog({ request, onOpenChange }: TraceDialogProps) {
  const t = useTranslations("apiLogs");
  const tc = useTranslations("common");
  const trace = useAPIRequestTrace(request?.request_id ?? null);
  const openerRef = useRef<HTMLElement | null>(null);

  return (
    <Dialog open={Boolean(request)} onOpenChange={onOpenChange}>
      <DialogContent
        showCloseButton={false}
        className="flex h-[90dvh] flex-col overflow-hidden sm:max-w-3xl"
        onOpenAutoFocus={() => {
          openerRef.current = document.activeElement instanceof HTMLElement
            ? document.activeElement
            : null;
        }}
        onCloseAutoFocus={(event) => {
          if (!openerRef.current) return;
          event.preventDefault();
          openerRef.current.focus();
        }}
      >
        <DialogHeader>
          <DialogTitle>{t("trace")}</DialogTitle>
          <DialogDescription>{t("traceDescription")}</DialogDescription>
          {request ? <div className="flex flex-wrap items-center gap-2 text-sm">
            <span className="font-mono">{request.request_id}</span>
            <DateCell timestamp={request.created_at} />
            <ProtocolBadge protocol={request.protocol} />
            <HTTPStatusBadge statusCode={request.status_code} />
            <DurationCell ms={request.duration_ms} />
          </div> : null}
        </DialogHeader>

        <ScrollArea data-slot="trace-dialog-body" className="min-h-0 flex-1">
          <div className="flex flex-col gap-3 pr-3">
            {trace.isLoading ? (
              <div className="flex flex-col gap-3" role="status">
                <span className="text-sm text-muted-foreground">{t("traceLoading")}</span>
                <Skeleton className="h-28 w-full" />
                <Skeleton className="h-28 w-full" />
              </div>
            ) : trace.error ? (
              <TraceError error={trace.error} />
            ) : trace.data ? (
              <>
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
              </>
            ) : (
              <TraceError error={trace.error} />
            )}
          </div>
        </ScrollArea>

        <DialogFooter>
          {request ? <Button type="button" variant="outline" onClick={() => void copyTextWithFeedback(request.request_id, { success: tc("copied"), error: tc("copyFailed") })}>{tc("copy")}</Button> : null}
          <DialogClose asChild>
            <Button variant="outline">{t("close")}</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
