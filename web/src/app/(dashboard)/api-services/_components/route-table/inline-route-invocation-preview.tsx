"use client";

import { useMemo, useState } from "react";
import { ChevronDown, CircleOff, Copy, RefreshCw } from "lucide-react";
import { useTranslations } from "next-intl";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Textarea } from "@/components/ui/textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { type APIBackend, type APIProtocol, type APIRequestExample, type APIRoute, type APIRoutePreviewInput, useAPIRoutePreview } from "@/lib/api/api-services";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";

import { apiServiceDiagnosticMessage } from "../../api-service-error";
import { buildInvocationCommands } from "../invocation-command";
import { STANDARD_ROUTE_HTTP_METHODS } from "../route-editor/standard-http-methods";
import { buildPublicSegmentedURL, buildRoutePreviewViewModel } from "./route-preview-url";
import { SegmentedURL } from "../segmented-url";

export interface InlineRouteInvocationPreviewProps {
  origin: string;
  serviceSlug: string;
  route: APIRoute;
  target: APIBackend;
  dependencyRevision: string;
  defaultOpen?: boolean;
}

const emptyExample: APIRequestExample = { method: "GET", subpath: "", query: "", headers: {}, body: "" };
type InvocationProtocol = "http" | "websocket";

function selectedInvocationProtocol(protocols: APIProtocol[]): InvocationProtocol {
  return protocols.includes("http") ? "http" : "websocket";
}

export function effectiveRouteExample(route: APIRoute): APIRequestExample {
  const saved = routeExample(route);
  const method = saved.method || route.allowed_methods[0] || "GET";
  return { ...saved, method, subpath: route.forward_subpath ? saved.subpath : "", headers: { ...saved.headers } };
}

function effectiveInvocationExample(route: APIRoute, example: APIRequestExample, protocol: InvocationProtocol): APIRequestExample {
  if (protocol !== "websocket" || Object.keys(example.headers).some((name) => name.toLowerCase() === "sec-websocket-protocol")) return example;
  const subprotocol = route.websocket_subprotocols?.[0];
  return subprotocol ? { ...example, headers: { ...example.headers, "Sec-WebSocket-Protocol": subprotocol } } : example;
}

export function routeExample(route: APIRoute): APIRequestExample {
  return route.example_request || emptyExample;
}

export function copyExample(example: APIRequestExample): APIRequestExample {
  return { ...example, headers: { ...example.headers } };
}

export function sameExample(left: APIRequestExample, right: APIRequestExample) {
  if (left.method !== right.method || left.subpath !== right.subpath || left.query !== right.query || left.body !== right.body) return false;
  const leftHeaders = Object.entries(left.headers);
  const rightHeaders = Object.entries(right.headers);
  return leftHeaders.length === rightHeaders.length && leftHeaders.every(([name, value]) => right.headers[name] === value);
}

export function parsedHeaders(raw: string): Record<string, string> | undefined {
  try {
    const value: unknown = JSON.parse(raw || "{}");
    if (typeof value !== "object" || value === null || Array.isArray(value)) return undefined;
    if (!Object.values(value).every((item) => typeof item === "string")) return undefined;
    return value as Record<string, string>;
  } catch {
    return undefined;
  }
}

export function previewInputForInvocation(route: APIRoute, sample: APIRequestExample | undefined): APIRoutePreviewInput | undefined {
  if (!sample) return undefined;
  return {
    api_service_id: route.api_service_id,
    slug: route.slug,
    upstream_path: route.upstream_path,
    forward_subpath: route.forward_subpath,
    sample,
    target: { mode: "existing", backend_id: route.backend_id },
  };
}

function endpointSubprotocol(headers: Record<string, string>) {
  return Object.entries(headers).find(([name]) => name.toLowerCase() === "sec-websocket-protocol")?.[1];
}

function EndpointRows({ model }: { model: ReturnType<typeof buildRoutePreviewViewModel> }) {
  const t = useTranslations("apiServices");
  return <div className="flex min-w-0 flex-col gap-3">{model.endpoints.map((endpoint) => (
    <section key={endpoint.id} className="flex min-w-0 flex-col gap-2 rounded-md border p-3" aria-label={endpoint.name}>
      <div className="flex items-center justify-between gap-2"><span className="font-medium">{endpoint.name}</span>{endpoint.status === 0 ? <Badge variant="destructive">{t("endpointDisabled")}</Badge> : null}</div>
      <div className="min-w-0 overflow-x-auto [&_[data-slot=button]]:size-11"><SegmentedURL {...endpoint.finalURL} copyLabel={t("copyEndpointURL", { name: endpoint.name })} copySuccess={t("finalUrlCopied")} /></div>
    </section>
  ))}</div>;
}

export function InlineRouteInvocationPreview(props: InlineRouteInvocationPreviewProps): React.ReactNode {
  const sessionKey = `${props.route.id}:${JSON.stringify({ example: routeExample(props.route), protocols: props.route.protocols, forwardSubpath: props.route.forward_subpath, allowedMethods: props.route.allowed_methods, websocketSubprotocols: props.route.websocket_subprotocols })}`;
  return <InlineRouteInvocationPreviewSession key={sessionKey} {...props} />;
}

function InlineRouteInvocationPreviewSession({ origin, serviceSlug, route, target, dependencyRevision, defaultOpen = false }: InlineRouteInvocationPreviewProps): React.ReactNode {
  const t = useTranslations("apiServices");
  const defaultExample = useMemo(() => copyExample(effectiveRouteExample(route)), [route]);
  const [local, setLocal] = useState(() => ({ example: defaultExample, headersText: JSON.stringify(defaultExample.headers, null, 2) }));
  const [detailsOpen, setDetailsOpen] = useState(defaultOpen);
  const [selectedProtocol, setSelectedProtocol] = useState<InvocationProtocol>(() => selectedInvocationProtocol(route.protocols));

  const headers = useMemo(() => parsedHeaders(local.headersText), [local.headersText]);
  const savedExample = useMemo(() => headers ? { ...local.example, headers } : undefined, [headers, local.example]);
  const example = useMemo(() => savedExample ? effectiveInvocationExample(route, savedExample, selectedProtocol) : undefined, [route, savedExample, selectedProtocol]);
  const isDefault = savedExample !== undefined && sameExample(savedExample, defaultExample);
  const previewDraft = useMemo(() => previewInputForInvocation(route, example), [example, route]);
  const previewCacheKey = previewDraft && isDefault ? `${route.id}:invocation:${selectedProtocol}:${dependencyRevision}` : undefined;
  const preview = useAPIRoutePreview(previewDraft, { cacheKey: previewCacheKey });
  const fullRoute = example ? { ...route, example_request: example } : undefined;
  const model = fullRoute && preview.data ? buildRoutePreviewViewModel({ origin, serviceSlug, route: fullRoute, backend: target, preview: preview.data, invocationProtocol: selectedProtocol }) : undefined;
  const endpoints = example ? preview.data?.endpoints ?? [] : [];
  const allDisabled = endpoints.length > 0 && endpoints.every((endpoint) => endpoint.status === 0);
  const diagnostics = example ? preview.data?.diagnostics.filter((code) => !(allDisabled && code === "no_available_upstream")).map((code) => apiServiceDiagnosticMessage(t, code)) ?? [] : [];
  const commands = example ? buildInvocationCommands({ origin, serviceSlug, routeSlug: route.slug, protocols: route.protocols, example, token: "${API_TOKEN}" }) : [];
  const command = commands.find((candidate) => candidate.kind === (selectedProtocol === "http" ? "curl" : "websocat"));
  const methods = route.allowed_methods.length === 0 ? STANDARD_ROUTE_HTTP_METHODS : route.allowed_methods;
  const websocket = selectedProtocol === "websocket";
  const supportsMultipleProtocols = route.protocols.includes("http") && route.protocols.includes("websocket");
  const publicURL = buildPublicSegmentedURL(origin, serviceSlug, { ...route, protocols: [selectedProtocol], example_request: example ?? local.example }, example ?? local.example);
  const updateExample = (patch: Partial<APIRequestExample>) => setLocal((current) => ({ ...current, example: { ...current.example, ...patch } }));
  const updateHeaders = (headersText: string) => setLocal((current) => ({ ...current, headersText }));
  const subprotocol = example ? endpointSubprotocol(example.headers) : undefined;

  return (
    <section className="grid min-w-0 max-w-full grid-cols-1 gap-4 border-t pt-4" aria-label={t("previewInvocation")}>
      <div className="grid min-w-0 gap-4 sm:grid-cols-3">
        <Field><FieldLabel htmlFor={`inline-route-method-${route.id}`}>{t("method")}</FieldLabel><Select value={local.example.method || "GET"} onValueChange={(method) => updateExample({ method })}><SelectTrigger id={`inline-route-method-${route.id}`} aria-label={t("method")} className="min-h-11 w-full"><SelectValue /></SelectTrigger><SelectContent>{methods.map((method) => <SelectItem key={method} value={method}>{method}</SelectItem>)}</SelectContent></Select></Field>
        {route.forward_subpath ? <Field><FieldLabel htmlFor={`inline-route-subpath-${route.id}`}>{t("subpath")}</FieldLabel><Input id={`inline-route-subpath-${route.id}`} aria-label={t("subpath")} className="min-h-11" value={local.example.subpath} onChange={(event) => updateExample({ subpath: event.target.value })} /></Field> : null}
        <Field><FieldLabel htmlFor={`inline-route-query-${route.id}`}>{t("query")}</FieldLabel><Input id={`inline-route-query-${route.id}`} aria-label={t("query")} className="min-h-11" value={local.example.query} onChange={(event) => updateExample({ query: event.target.value })} /></Field>
      </div>

      {supportsMultipleProtocols ? <ToggleGroup type="single" value={selectedProtocol} onValueChange={(value) => { if (value === "http" || value === "websocket") setSelectedProtocol(value); }}><ToggleGroupItem value="http" className="min-h-11 min-w-11">{t("httpProtocol")}</ToggleGroupItem><ToggleGroupItem value="websocket" className="min-h-11 min-w-11">{t("websocketProtocol")}</ToggleGroupItem></ToggleGroup> : null}
      {websocket ? <div className="flex flex-wrap gap-3 text-sm text-muted-foreground"><span>{t("invocationProtocol")}: {t("websocketProtocol")}</span>{subprotocol ? <span>{t("websocketSubprotocols")}: {subprotocol}</span> : null}</div> : null}

      <section className="flex min-w-0 flex-col gap-2" aria-label={t("clientRequest")}><span className="text-xs font-medium text-muted-foreground">{t("clientRequest")}</span><div className="min-w-0 overflow-x-auto rounded-md border bg-muted/30 p-3 [&_[data-slot=button]]:size-11"><SegmentedURL {...publicURL} copyLabel={t("copyClientRequest")} copySuccess={t("publicUrlCopied")} /></div></section>

      {example && preview.error ? <Alert variant="destructive"><AlertTitle>{t("routingPreviewFailed")}</AlertTitle><AlertDescription className="flex flex-col items-start gap-3"><span>{t("routingPreviewFailedDescription")}</span><Button type="button" variant="outline" size="sm" className="min-h-11 min-w-11" onClick={() => void preview.refetch()}><RefreshCw data-icon="inline-start" />{t("retryPreview")}</Button></AlertDescription></Alert> : null}
      {example && preview.isLoading ? <div className="flex flex-col gap-2" role="status" aria-live="polite" aria-label={t("routingPreviewLoading")}><span className="text-sm text-muted-foreground">{t("routingPreviewLoading")}</span><Skeleton aria-hidden="true" className="h-16 w-full" /><Skeleton aria-hidden="true" className="h-16 w-full" /></div> : null}
      {example && !preview.isLoading && !preview.error && endpoints.length === 0 ? <Empty className="border"><EmptyHeader><EmptyMedia variant="icon"><CircleOff /></EmptyMedia><EmptyTitle>{t("noEndpointCandidates")}</EmptyTitle><EmptyDescription>{diagnostics.join(" · ") || t("routingPreviewEmptyDescription")}</EmptyDescription></EmptyHeader></Empty> : null}
      {example && !preview.isLoading && !preview.error && allDisabled ? <Alert variant="destructive"><CircleOff /><AlertTitle>{t("endpointUnavailable503")}</AlertTitle><AlertDescription>{t("routingPreviewStaticOnlyDisabled")}</AlertDescription></Alert> : null}
      {model && model.endpoints.length > 0 ? <section className="flex min-w-0 flex-col gap-2" aria-label={t("endpointSummary")}><span className="text-xs font-medium text-muted-foreground">{t("endpointSummary")}</span><EndpointRows model={model} /></section> : null}
      {endpoints.length > 0 && diagnostics.length ? <Alert><AlertTitle>{t("routingPreviewDiagnostics")}</AlertTitle><AlertDescription>{diagnostics.join(" · ")}</AlertDescription></Alert> : null}

      <Collapsible open={detailsOpen} onOpenChange={setDetailsOpen}><CollapsibleTrigger asChild><Button type="button" variant="outline" size="sm" className="min-h-11 min-w-11"><ChevronDown data-icon="inline-start" />{t("requestDetails")}</Button></CollapsibleTrigger><CollapsibleContent className="pt-4"><FieldGroup className="gap-4"><Field data-invalid={headers === undefined}><FieldLabel htmlFor={`inline-route-headers-${route.id}`}>{t("headers")}</FieldLabel><Textarea id={`inline-route-headers-${route.id}`} aria-label={t("headers")} aria-invalid={headers === undefined} value={local.headersText} onChange={(event) => updateHeaders(event.target.value)} />{headers === undefined ? <span className="text-xs text-destructive">{t("requestHeadersJSONInvalid")}</span> : null}</Field>{!websocket ? <Field><FieldLabel htmlFor={`inline-route-body-${route.id}`}>{t("body")}</FieldLabel><Textarea id={`inline-route-body-${route.id}`} aria-label={t("body")} value={local.example.body} onChange={(event) => updateExample({ body: event.target.value })} /></Field> : null}</FieldGroup></CollapsibleContent></Collapsible>

      {command ? <div className="flex min-w-0 items-start gap-2 overflow-x-auto rounded-md bg-muted p-3"><pre className="min-w-max flex-1 whitespace-pre-wrap break-all text-xs"><code>{command.command}</code></pre><Button type="button" variant="ghost" size="icon-sm" className="size-11 shrink-0" aria-label={t("copyCommand")} onClick={() => void copyTextWithFeedback(command.command, { success: t("templateCommandCopied"), error: t("copyCommandFailed") })}><Copy /></Button></div> : null}
    </section>
  );
}
