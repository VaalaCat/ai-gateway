"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { Loader2, Bug } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { useLogTrace } from "@/lib/api/logs";

interface TraceDetailProps {
  requestId: string;
}

function CollapsibleSection({
  title,
  content,
}: {
  title: string;
  content: string;
}) {
  const [open, setOpen] = useState(false);
  if (!content || content === "{}" || content === "null") return null;

  return (
    <div>
      <button
        type="button"
        className="text-sm font-medium text-muted-foreground hover:text-foreground"
        onClick={() => setOpen(!open)}
      >
        {open ? "▼" : "▶"} {title}
      </button>
      {open && (
        <pre className="mt-1 max-h-60 overflow-auto whitespace-pre-wrap break-all rounded-md border bg-muted/50 p-2 text-xs font-mono">
          {tryFormatJSON(content)}
        </pre>
      )}
    </div>
  );
}

function tryFormatJSON(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}

export function TraceDetail({ requestId }: TraceDetailProps) {
  const t = useTranslations("logs");
  const [loaded, setLoaded] = useState(false);
  const { data: trace, isLoading, isError } = useLogTrace(loaded ? requestId : null);

  if (!loaded) {
    return (
      <Button
        variant="outline"
        size="sm"
        className="mt-2"
        onClick={() => setLoaded(true)}
      >
        <Bug className="mr-1 size-3" />
        {t("debugDetails")}
      </Button>
    );
  }

  if (isLoading) {
    return (
      <div className="mt-2 flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="size-3 animate-spin" />
        {t("loadingTrace")}
      </div>
    );
  }

  if (isError || !trace) {
    return (
      <div className="mt-2 text-sm text-muted-foreground">
        {t("traceNotFound")}
      </div>
    );
  }

  return (
    <div className="mt-2 space-y-2 rounded-md border p-3">
      <div className="flex flex-wrap items-center gap-4 text-sm">
        {trace.inbound_path && (
          <div>
            <span className="text-muted-foreground">{t("inboundPath")}: </span>
            <code className="font-mono text-xs">{trace.inbound_path}</code>
          </div>
        )}
        {trace.outbound_path && (
          <div>
            <span className="text-muted-foreground">{t("outboundPath")}: </span>
            <code className="font-mono text-xs">{trace.outbound_path}</code>
          </div>
        )}
        {trace.upstream_status > 0 && (
          <div>
            <span className="text-muted-foreground">{t("upstreamStatus")}: </span>
            <Badge variant={trace.upstream_status >= 400 ? "destructive" : "secondary"}>
              {trace.upstream_status}
            </Badge>
          </div>
        )}
      </div>

      <div className="space-y-1">
        <CollapsibleSection title={t("inboundHeaders")} content={trace.inbound_headers} />
        <CollapsibleSection title={t("outboundHeaders")} content={trace.outbound_headers} />
        <CollapsibleSection title={t("inboundBody")} content={trace.inbound_body} />
        <CollapsibleSection title={t("outboundBody")} content={trace.outbound_body} />
        <CollapsibleSection title={t("responseHeaders")} content={trace.response_headers} />
        <CollapsibleSection title={t("responseBody")} content={trace.response_body} />
        <CollapsibleSection title={t("clientResponseBody")} content={trace.client_response_body} />
      </div>
    </div>
  );
}
