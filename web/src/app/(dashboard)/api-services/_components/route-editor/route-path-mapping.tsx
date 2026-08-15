"use client";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import type { APIRouteTargetCommand } from "@/lib/api/api-services";
import { buildEndpointSegmentedURL, buildPublicSegmentedURL } from "../route-table/route-preview-url";
import { SegmentedURL, type SegmentedURLValue } from "../segmented-url";
import { normalizeRoutePath } from "../route-form/route-form-state";
import type { RouteLivePreviewState } from "../route-form/route-live-preview";

interface RoutePathMappingProps {
  serviceSlug: string;
  origin: string;
  path: string;
  forwardSubpath: boolean;
  sampleSubpath: string;
  target: APIRouteTargetCommand;
  preview: RouteLivePreviewState;
  onPathChange: (path: string) => void;
  pathError?: string;
  t: (key: string, values?: Record<string, string | number | Date>) => string;
}

export function appendForwardSubpathMarker(value: SegmentedURLValue): SegmentedURLValue {
  const suffixStarts = [value.text.indexOf("?"), value.text.indexOf("#")].filter((index) => index >= 0);
  const insertAt = suffixStarts.length ? Math.min(...suffixStarts) : value.text.length;
  const marker = value.text.slice(0, insertAt).endsWith("/") ? "…" : "/…";
  return {
    text: `${value.text.slice(0, insertAt)}${marker}${value.text.slice(insertAt)}`,
    segments: value.segments.map((segment) => {
      if (segment.end <= insertAt) return segment;
      if (segment.start >= insertAt) return { ...segment, start: segment.start + marker.length, end: segment.end + marker.length };
      return { ...segment, end: segment.end + marker.length };
    }),
  };
}

function PublicRouteURL({ origin, serviceSlug, path, forwardSubpath, t }: Pick<RoutePathMappingProps, "origin" | "serviceSlug" | "path" | "forwardSubpath" | "t">) {
  const normalized = normalizeRoutePath(path);
  const baseValue = buildPublicSegmentedURL(origin, serviceSlug, {
    slug: normalized,
    protocols: ["http"],
    example_request: { method: "", subpath: "", query: "", headers: {}, body: "" },
  });
  const segmented = { ...baseValue, segments: baseValue.segments.filter((segment) => segment.end > segment.start) };
  const value = forwardSubpath ? appendForwardSubpathMarker(segmented) : segmented;
  return <SegmentedURL {...value} copyLabel={t("copyClientRequest")} />;
}

function EndpointRouteURL({ endpoint, path, forwardSubpath, sampleSubpath, t }: { endpoint: NonNullable<RouteLivePreviewState["data"]>["endpoints"][number]; path: string; forwardSubpath: boolean; sampleSubpath: string; t: RoutePathMappingProps["t"] }) {
  const baseValue = buildEndpointSegmentedURL(endpoint.final_url, endpoint.upstream_name, normalizeRoutePath(path), forwardSubpath ? sampleSubpath : "");
  const value = forwardSubpath ? appendForwardSubpathMarker(baseValue) : baseValue;
  return <div className="flex min-w-0 flex-col gap-1 rounded-md border p-3"><span className="text-xs font-medium text-muted-foreground">{endpoint.upstream_name}</span><SegmentedURL {...value} copyLabel={t("copyEndpointURL", { name: endpoint.upstream_name })} /></div>;
}

function EndpointResults({ target, preview, path, forwardSubpath, sampleSubpath, t }: Pick<RoutePathMappingProps, "target" | "preview" | "path" | "forwardSubpath" | "sampleSubpath" | "t">) {
  if (target.mode === "existing" && target.backend_id < 1) return <Empty className="p-3"><EmptyHeader><EmptyTitle>{t("targetRequired")}</EmptyTitle><EmptyDescription>{t("targetExistingDescription")}</EmptyDescription></EmptyHeader></Empty>;
  if (target.mode === "create" && !target.first_upstream.base_url) return <Empty className="p-3"><EmptyHeader><EmptyTitle>{t("endpointUrlRequired")}</EmptyTitle><EmptyDescription>{t("targetInlineDescription")}</EmptyDescription></EmptyHeader></Empty>;
  if (!preview.active || preview.isLoading) return <FieldGroup className="gap-3"><Skeleton className="h-16 w-full" /><Skeleton className="h-16 w-full" /></FieldGroup>;
  if (preview.error) return <Alert variant="destructive"><AlertTitle>{t("routingPreviewFailed")}</AlertTitle><AlertDescription className="flex flex-col items-start gap-3"><span>{t("routingPreviewFailedDescription")}</span><Button type="button" variant="outline" size="sm" onClick={() => void preview.refetch()}>{t("retryPreview")}</Button></AlertDescription></Alert>;
  if (!preview.data?.endpoints.length) return <Empty className="p-3"><EmptyHeader><EmptyTitle>{t("noEndpointCandidates")}</EmptyTitle><EmptyDescription>{t("routingPreviewEmptyDescription")}</EmptyDescription></EmptyHeader></Empty>;
  return <FieldGroup className="gap-3">{preview.data.endpoints.map((endpoint) => <EndpointRouteURL key={endpoint.upstream_id} endpoint={endpoint} path={path} forwardSubpath={forwardSubpath} sampleSubpath={sampleSubpath} t={t} />)}</FieldGroup>;
}

export function RoutePathMapping({ serviceSlug, origin, path, forwardSubpath, sampleSubpath, target, preview, onPathChange, pathError, t }: RoutePathMappingProps) {
  return <FieldSet><FieldLegend>{t("pathMapping")}</FieldLegend><FieldGroup>
    <Field data-invalid={Boolean(pathError) || undefined}><FieldLabel htmlFor="api-route-path">{t("path")}</FieldLabel><Input id="api-route-path" value={path} onChange={(event) => onPathChange(event.target.value)} aria-invalid={Boolean(pathError)} required /><FieldDescription>{t("pathDescription")}</FieldDescription>{pathError ? <FieldError>{t(pathError)}</FieldError> : null}</Field>
    <Field><FieldLabel>{t("publicRequestResult")}</FieldLabel><PublicRouteURL origin={origin} serviceSlug={serviceSlug} path={path} forwardSubpath={forwardSubpath} t={t} /></Field>
    <Field><FieldLabel>{t("upstreamRequestResults")}</FieldLabel><EndpointResults target={target} preview={preview} path={path} forwardSubpath={forwardSubpath} sampleSubpath={sampleSubpath} t={t} /></Field>
  </FieldGroup></FieldSet>;
}
