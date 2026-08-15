"use client";

import { useEffect, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useAPIRoutePreview, type APIRoutePreview, type APIRoutePreviewInput } from "@/lib/api/api-services";
import { apiServiceDiagnosticMessage } from "../../api-service-error";

export type RoutePreviewStatus = "debouncing" | "pending" | "valid" | "validation-error" | "network-error";

export interface RouteLivePreviewState {
  active: boolean;
  data: APIRoutePreview | undefined;
  error: unknown;
  isLoading: boolean;
  refetch: () => Promise<unknown>;
}

export function useRouteLivePreview(draft: APIRoutePreviewInput | undefined, onStatus?: (status: RoutePreviewStatus) => void): RouteLivePreviewState {
  const key = draft === undefined ? undefined : JSON.stringify(draft);
  const [debounced, setDebounced] = useState<{ key: string; draft: APIRoutePreviewInput }>();
  useEffect(() => {
    if (!key) return;
    const timer = window.setTimeout(() => setDebounced({ key, draft: JSON.parse(key) as APIRoutePreviewInput }), 300);
    return () => window.clearTimeout(timer);
  }, [key]);
  const activeDraft = debounced && debounced.key === key ? debounced.draft : undefined;
  const preview = useAPIRoutePreview(activeDraft);
  useEffect(() => { if (!draft) return; if (!activeDraft) onStatus?.("debouncing"); else if (preview.isLoading) onStatus?.("pending"); else if (preview.error) onStatus?.((typeof preview.error === "object" && preview.error !== null && "status" in preview.error && Number((preview.error as { status?: number }).status) < 500) ? "validation-error" : "network-error"); else if (preview.data) onStatus?.("valid"); }, [activeDraft, draft, onStatus, preview.data, preview.error, preview.isLoading]);
  return { active: Boolean(draft && activeDraft), data: preview.data, error: preview.error, isLoading: preview.isLoading, refetch: preview.refetch };
}

export function RouteLivePreviewView({ draft, preview, t }: { draft: APIRoutePreviewInput | undefined; preview: RouteLivePreviewState; t: (key: string, values?: Record<string, string | number | Date>) => string }) {
  if (!draft) return null;
  if (!preview.active || preview.isLoading) return <Skeleton className="h-20 w-full" />;
  if (preview.error) { const validation = typeof preview.error === "object" && preview.error !== null && "status" in preview.error && Number((preview.error as { status?: number }).status) < 500; return <Alert variant="destructive"><AlertTitle>{t(validation ? "routingPreviewValidationFailed" : "routingPreviewFailed")}</AlertTitle><AlertDescription className="flex flex-col items-start gap-3"><span>{t(validation ? "routingPreviewValidationFailedDescription" : "routingPreviewFailedDescription")}</span><Button type="button" variant="outline" size="sm" onClick={() => void preview.refetch()}>{t("retryPreview")}</Button></AlertDescription></Alert>; }
  if (!preview.data) return null;
  const allDisabled = preview.data.endpoints.length > 0 && preview.data.endpoints.every((endpoint) => endpoint.status === 0);
  const diagnostics = preview.data.diagnostics
    .filter((code) => !(allDisabled && code === "no_available_upstream"))
    .map((code) => apiServiceDiagnosticMessage(t, code));
  return <div className="flex flex-col gap-2" aria-label={t("routingPreview")}><p className="text-sm font-medium">{t("routingPreview")}</p>{preview.data.endpoints.length === 0 ? <Alert><AlertTitle>{t("routingPreviewEmpty")}</AlertTitle><AlertDescription>{diagnostics.join(" · ") || t("routingPreviewEmptyDescription")}</AlertDescription></Alert> : <><Alert><AlertDescription>{t("singleEndpointNoFallback")}</AlertDescription></Alert>{allDisabled ? <Alert variant="destructive"><AlertTitle>{t("endpointUnavailable503")}</AlertTitle><AlertDescription>{t("routingPreviewStaticOnlyDisabled")}</AlertDescription></Alert> : null}{preview.data.endpoints.map((endpoint) => <div key={endpoint.upstream_id} className="flex min-w-0 flex-col gap-1 rounded-md border p-3"><span className="font-medium">{endpoint.upstream_name}{endpoint.status === 0 ? ` · ${t("endpointDisabled")}` : ""}</span><code className="break-all text-xs text-muted-foreground">{endpoint.final_url}</code></div>)}</>}{preview.data.endpoints.length > 0 && diagnostics.length > 0 ? <Alert><AlertTitle>{t("routingPreviewDiagnostics")}</AlertTitle><AlertDescription>{diagnostics.join(" · ")}</AlertDescription></Alert> : null}</div>;
}

export function RouteLivePreview({ draft, t, onStatus }: { draft: APIRoutePreviewInput | undefined; t: (key: string, values?: Record<string, string | number | Date>) => string; onStatus?: (status: RoutePreviewStatus) => void }) {
  const preview = useRouteLivePreview(draft, onStatus);
  return <RouteLivePreviewView draft={draft} preview={preview} t={t} />;
}
