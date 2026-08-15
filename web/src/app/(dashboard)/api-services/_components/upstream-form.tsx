"use client";

import Link from "next/link";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { EntityPicker } from "@/components/business/entity-picker/entity-picker";
import { FormErrorSummary, useFormErrorReport } from "@/components/business/form-error-summary";
import { PageLayout } from "@/components/layout/page-layout";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { useAllAPIRoutes, useAPIBackend, useAPIRoutePreview, useAPIUpstream, useCreateAPIUpstream, useUpdateAPIUpstream, type APIRoute, type APIRoutePreviewInput, type APIUpstream, type APIUpstreamAuthType, type APIUpstreamCredential } from "@/lib/api/api-services";
import { buildEndpointSegmentedURL } from "./route-table/route-preview-url";
import { SegmentedURL } from "./segmented-url";
import { UpstreamCredentialFields } from "./upstream-credential-fields";
import { UpstreamHeaderFields } from "./upstream-header-fields";
import { credentialComplete } from "./upstream-credential";
import { headerRowsObject, invalidOverrideHeader, type ExampleHeaderRow } from "./route-form/route-form-state";
import { DeleteConfirmationDialog } from "../delete-confirmation-dialog";
import { routeReturnQuery, serviceDetailReturnPath, type RouteReturnContext } from "./route-return";
import { apiServiceErrorMessage } from "../api-service-error";
import { FormPageSkeleton } from "./form-entry";

export type APIUpstreamFormMode = ({ kind: "create"; serviceId: number; backendId?: number } | { kind: "copy"; id: number; serviceId: number } | { kind: "edit"; id: number; serviceId: number }) & { returnRoute?: RouteReturnContext };
const authTypes: APIUpstreamAuthType[] = ["none", "bearer", "header", "query", "basic"];
const emptyCredential: APIUpstreamCredential = {};
interface UpstreamValues { backendID: string; name: string; baseURL: string; weight: string; priority: string; authType: APIUpstreamAuthType; credential: APIUpstreamCredential; proxyURL: string; headerRows: ExampleHeaderRow[]; clearCredential: boolean; clearProxy: boolean; enabled: boolean }
const emptyValues: UpstreamValues = { backendID: "", name: "", baseURL: "", weight: "1", priority: "0", authType: "none", credential: emptyCredential, proxyURL: "", headerRows: [], clearCredential: false, clearProxy: false, enabled: true };
function upstreamValues(upstream: APIUpstream): UpstreamValues { return { ...emptyValues, backendID: String(upstream.backend_id), name: upstream.name, baseURL: upstream.base_url, weight: String(upstream.weight), priority: String(upstream.priority), authType: upstream.auth_type, headerRows: Object.entries(upstream.header_override ?? {}).map(([name, value], index) => ({ id: `upstream-header:${index}:${name}`, name, value })), enabled: upstream.status === 1 }; }
function statusOf(error: unknown) { return typeof error === "object" && error !== null && "status" in error ? (error as { status?: number }).status : undefined; }

const emptyExample = { method: "", subpath: "", query: "", headers: {}, body: "" };

function hasSafeEndpointPath(raw: string) {
  const authorityStart = raw.indexOf("://") + 3;
  const queryStart = raw.indexOf("?", authorityStart);
  const pathStart = raw.indexOf("/", authorityStart);
  if (pathStart < 0 || (queryStart >= 0 && pathStart > queryStart)) return true;
  const path = raw.slice(pathStart, queryStart >= 0 ? queryStart : raw.length);
  return path.split("/").every((escapedSegment) => {
    let segment = escapedSegment;
    for (let layer = 0; layer < 4; layer += 1) {
      const decoded = segment.replace(/%([0-9a-f]{2})/gi, (_escape, hex: string) => String.fromCharCode(Number.parseInt(hex, 16)));
      if (decoded === "." || decoded === ".." || /[\\/\x00\r\n]/.test(decoded) || /%(?:2f|5c|00)/i.test(decoded)) return false;
      if (decoded === segment) return true;
      segment = decoded;
    }
    return false;
  });
}

function isStructurallyBracketedEndpointAuthority(authority: string) {
  if (!authority.startsWith("[")) return false;
  const bracketEnd = authority.indexOf("]");
  if (bracketEnd < 2 || authority.indexOf("]", bracketEnd + 1) >= 0) return false;
  const address = authority.slice(1, bracketEnd);
  const suffix = authority.slice(bracketEnd + 1);
  return address.includes(":") && !address.includes("[") && (suffix === "" || /^:[0-9]*$/.test(suffix));
}

function isPreviewableEndpointBaseURL(raw: string) {
  if (!/^https?:\/\//i.test(raw)) return false;
  const authorityTail = raw.slice(raw.indexOf("://") + 3);
  const authorityEnd = authorityTail.search(/[/?#]/);
  const authority = authorityTail.slice(0, authorityEnd < 0 ? authorityTail.length : authorityEnd);
  if (!authority || authority.includes("@") || authority.includes("\\") || raw !== raw.trim() || /[\x00-\x1f\x7f]/.test(raw) || /%(?![0-9a-f]{2})/i.test(raw) || raw.includes("#")) return false;
  try {
    const url = new URL(raw);
    return /^https?:$/.test(url.protocol) && Boolean(url.host) && !url.username && !url.password && !url.hash && hasSafeEndpointPath(raw);
  } catch {
    return isStructurallyBracketedEndpointAuthority(authority) && hasSafeEndpointPath(raw);
  }
}

function endpointRoutePreviewDraft(route: APIRoute, endpoint: Pick<UpstreamValues, "baseURL" | "enabled">, serviceID: number): APIRoutePreviewInput | undefined {
  if (!isPreviewableEndpointBaseURL(endpoint.baseURL)) return undefined;
  return {
    api_service_id: serviceID,
    slug: route.slug,
    upstream_path: route.slug,
    forward_subpath: false,
    sample: emptyExample,
    target: {
      mode: "create",
      backend: { name: "endpoint-route-preview" },
      first_upstream: {
        name: "endpoint-route-preview",
        base_url: endpoint.baseURL,
        weight: 1,
        priority: 0,
        auth_type: "none",
        status: endpoint.enabled ? 1 : 0,
      },
    },
  };
}

function useEndpointRoutePreviewDraft(route: APIRoute, values: UpstreamValues, serviceID: number, publishImmediately: boolean) {
  const baseURLState = values.baseURL === "" ? "required" as const : isPreviewableEndpointBaseURL(values.baseURL) ? "valid" as const : "invalid" as const;
  const pendingDraft = useMemo(
    () => endpointRoutePreviewDraft(route, { baseURL: values.baseURL, enabled: values.enabled }, serviceID),
    [route, serviceID, values.baseURL, values.enabled],
  );
  const [published, setPublished] = useState<{ source: APIRoutePreviewInput; draft: APIRoutePreviewInput } | undefined>(
    () => publishImmediately && pendingDraft ? { source: pendingDraft, draft: pendingDraft } : undefined,
  );
  useEffect(() => {
    if (pendingDraft === undefined || published?.source === pendingDraft) return;
    const timer = window.setTimeout(() => setPublished({ source: pendingDraft, draft: pendingDraft }), 300);
    return () => window.clearTimeout(timer);
  }, [pendingDraft, published?.source]);
  if (baseURLState !== "valid") return { draft: undefined, state: baseURLState };
  if (!published || published.source !== pendingDraft) return { draft: undefined, state: "preparing" as const };
  return { draft: published.draft, state: "published" as const };
}

function EndpointRouteResult({ route, values, serviceID, publishImmediately, t }: { route: APIRoute; values: UpstreamValues; serviceID: number; publishImmediately: boolean; t: (key: string, values?: Record<string, string | number | Date>) => string }) {
  const draftState = useEndpointRoutePreviewDraft(route, values, serviceID, publishImmediately);
  const draft = draftState.draft;
  const preview = useAPIRoutePreview(draft, { enabled: draft !== undefined });
  const endpoint = preview.data?.endpoints[0];
  return <div data-testid="endpoint-route-result" className="flex min-w-0 flex-col gap-2 rounded-md border p-3 [&_[data-slot=button]]:size-11"><code className="text-xs font-medium">/{route.slug}</code>{draftState.state === "required" ? <p className="text-sm text-muted-foreground">{t("endpointPreviewRequired")}</p> : draftState.state === "invalid" ? <p className="text-sm text-destructive">{t("endpointPreviewInvalid")}</p> : draftState.state === "preparing" ? <p className="text-sm text-muted-foreground">{t("endpointPreviewPreparing")}</p> : preview.isLoading ? <div role="status" aria-label={t("endpointPreviewLoading")}><Skeleton className="h-8 w-full" /></div> : preview.error ? <Alert variant="destructive"><AlertTitle>{t("endpointPreviewFailed")}</AlertTitle><AlertDescription className="flex flex-col items-start gap-3"><span>{t("endpointPreviewFailedDescription")}</span><Button type="button" variant="outline" size="sm" onClick={() => void preview.refetch()}>{t("retryEndpointPreview")}</Button></AlertDescription></Alert> : endpoint ? <SegmentedURL {...buildEndpointSegmentedURL(endpoint.final_url, values.name, route.slug)} copyLabel={t("copyEndpointURL", { name: values.name })} /> : <Empty className="p-3"><EmptyHeader><EmptyTitle>{t("endpointPreviewEmpty")}</EmptyTitle><EmptyDescription>{t("endpointPreviewEmptyDescription")}</EmptyDescription></EmptyHeader></Empty>}</div>;
}

function EndpointRouteResults({ routes, values, serviceID, backendID, session, publishImmediately, t }: { routes: APIRoute[]; values: UpstreamValues; serviceID: number; backendID: number; session: string; publishImmediately: boolean; t: (key: string, values?: Record<string, string | number | Date>) => string }) {
  if (!routes.length) return <Empty className="p-3"><EmptyHeader><EmptyTitle>{t("targetNoRoutes")}</EmptyTitle><EmptyDescription>{t("endpointRouteResultsEmpty")}</EmptyDescription></EmptyHeader></Empty>;
  return <FieldGroup className="gap-3">{routes.map((route) => <EndpointRouteResult key={`${session}:${backendID}:${route.id}`} route={route} values={values} serviceID={serviceID} publishImmediately={publishImmediately} t={t} />)}</FieldGroup>;
}

export function APIUpstreamForm({ mode }: { mode: APIUpstreamFormMode }) {
  const t = useTranslations("apiServices");
  const tc = useTranslations("common");
  const router = useRouter();
  const sourceID = mode.kind === "create" ? 0 : mode.id;
  const query = useAPIUpstream(sourceID, { enabled: mode.kind !== "create" });
  const identity = mode.kind === "create" ? `create:${mode.serviceId}:${mode.backendId ?? 0}` : `${mode.kind}:${mode.serviceId}:${mode.id}`;
  const [draft, setDraft] = useState<{ identity: string; values: UpstreamValues; previewChanged: boolean }>();
  const selectedBackendID = mode.kind === "create" ? Number(draft?.identity === identity ? draft.values.backendID : mode.backendId ?? 0) : (query.data?.backend_id ?? 0);
  const backend = useAPIBackend(selectedBackendID, { enabled: selectedBackendID > 0 });
  const routes = useAllAPIRoutes(mode.serviceId);
  const create = useCreateAPIUpstream();
  const update = useUpdateAPIUpstream();
  const queryMatches = mode.kind !== "create" && query.data?.id === mode.id && backend.data?.id === query.data.backend_id && backend.data.api_service_id === mode.serviceId;
  const loadedValues = queryMatches && query.data ? upstreamValues(query.data) : undefined;
  const values = draft?.identity === identity ? draft.values : loadedValues ? (mode.kind === "copy" ? { ...loadedValues, name: "" } : loadedValues) : mode.kind === "create" && mode.backendId ? { ...emptyValues, backendID: String(mode.backendId) } : emptyValues;
  const setValues = (update: (current: UpstreamValues) => UpstreamValues, previewChanged = false) => setDraft({ identity, values: update(values), previewChanged: (draft?.identity === identity && draft.previewChanged) || previewChanged });
  const { error, clearError, reportError } = useFormErrorReport();
  const backendID = Number(values.backendID);
  const detailPath = serviceDetailReturnPath(mode.serviceId, mode.returnRoute);
  const targetRoutes = (routes.data ?? []).filter((route) => route.backend_id === backendID);
  const configuredCredential = mode.kind === "edit" && queryMatches && query.data?.credential_configured === true && values.authType === query.data.auth_type;
  const [confirmLastEndpoint, setConfirmLastEndpoint] = useState(false);
  const baseURLInvalid = values.baseURL !== "" && !isPreviewableEndpointBaseURL(values.baseURL);

  const save = async (throwOnMutationFailure = false) => {
    clearError();
    const weight = Number(values.weight); const priority = Number(values.priority);
    if (values.baseURL.length > 2048) { reportError(t("baseUrlTooLong")); return false; }
    if (!Number.isSafeInteger(weight) || weight < 1 || !Number.isSafeInteger(priority)) { reportError(t("invalidUpstreamWeight")); return false; }
    const backendID = Number(values.backendID);
    if (mode.kind !== "edit" && (!Number.isSafeInteger(backendID) || backendID < 1)) { reportError(t("backendRequired")); return false; }
    const credential = values.authType === "none" || !credentialComplete(values.authType, values.credential) ? undefined : values.credential;
    if (values.authType !== "none" && !values.clearCredential && !configuredCredential && !credentialComplete(values.authType, credential)) { reportError(t("credentialRequired")); return false; }
    if (values.headerRows.some((row) => invalidOverrideHeader(row, values.headerRows))) { reportError(t("invalidHeaderOverride")); return false; }
    const authChangedToNone = queryMatches && query.data?.auth_type !== "none" && values.authType === "none";
    const clearCredential = values.clearCredential || authChangedToNone;
    const common = { name: values.name.trim(), base_url: values.baseURL, weight, priority, auth_type: clearCredential ? "none" as const : values.authType, header_override: headerRowsObject(values.headerRows), status: values.enabled ? 1 : 0 };
    try {
      if (mode.kind === "edit") await update.mutateAsync({ id: mode.id, ...common, ...(clearCredential ? { credential: {} } : credential ? { credential } : {}), ...(values.clearProxy ? { proxy_url: "" } : values.proxyURL ? { proxy_url: values.proxyURL } : {}) });
      else await create.mutateAsync({ backend_id: backendID, ...common, ...(credential ? { credential } : {}), ...(values.proxyURL ? { proxy_url: values.proxyURL } : {}) });
      toast.success(tc("success"));
      router.push(detailPath);
      return true;
    } catch (reason) {
      reportError(apiServiceErrorMessage(t, reason));
      if (throwOnMutationFailure) throw reason;
      return false;
    }
  };
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const disablingLastEndpoint = mode.kind === "edit" && query.data?.status === 1 && !values.enabled && backend.data?.enabled_upstream_count === 1;
    if (disablingLastEndpoint) { setConfirmLastEndpoint(true); return; }
    await save();
  };
  if ((mode.kind !== "create" && (query.isLoading || (query.data !== undefined && backend.isLoading))) || (mode.kind === "create" && mode.backendId !== undefined && backend.isLoading)) return <FormPageSkeleton titleKey={mode.kind === "edit" ? "editUpstream" : mode.kind === "copy" ? "copyEndpoint" : "createUpstream"} descriptionKey="upstreamFormDescription" />;
  const loadError = query.error ?? backend.error;
  if (mode.kind === "create" && mode.backendId !== undefined && (loadError || backend.data?.id !== mode.backendId || backend.data.api_service_id !== mode.serviceId)) return <PageLayout title={t("createUpstream")}><Alert><AlertTitle>{t("upstreamNotFound")}</AlertTitle><AlertDescription>{t("upstreamNotFoundDescription")}</AlertDescription></Alert></PageLayout>;
  if (mode.kind !== "create" && loadError) { const missing = statusOf(loadError) === 404; return <PageLayout title={t(mode.kind === "copy" ? "copyEndpoint" : "editUpstream")}><Alert variant={missing ? "default" : "destructive"}><AlertTitle>{t(missing ? "upstreamNotFound" : statusOf(loadError) === 403 ? "permissionDenied" : "loadFailed")}</AlertTitle><AlertDescription>{t(missing ? "upstreamNotFoundDescription" : "loadFailedDescription")}</AlertDescription></Alert></PageLayout>; }
  if (mode.kind !== "create" && !queryMatches) return <PageLayout title={t(mode.kind === "copy" ? "copyEndpoint" : "editUpstream")}><Alert><AlertTitle>{t("upstreamNotFound")}</AlertTitle><AlertDescription>{t("upstreamNotFoundDescription")}</AlertDescription></Alert></PageLayout>;
  const returnQuery = routeReturnQuery(mode.returnRoute);
  return (
    <PageLayout title={t(mode.kind === "edit" ? "editUpstream" : mode.kind === "copy" ? "copyEndpoint" : "createUpstream")} description={t("upstreamFormDescription")} maxWidth="3xl" actions={mode.kind === "edit" ? <Button asChild variant="outline"><Link href={`/api-services/upstreams/new?service_id=${mode.serviceId}&copy_id=${mode.id}${returnQuery ? `&${returnQuery}` : ""}`}>{t("copyEndpoint")}</Link></Button> : undefined} footer={<><Button type="button" variant="outline" onClick={() => router.push(detailPath)}>{t("cancel")}</Button><Button type="submit" form="api-upstream-form" disabled={create.isPending || update.isPending}>{t("save")}</Button></>}>
      <form id="api-upstream-form" onSubmit={submit} className="flex flex-col gap-6">
        <FormErrorSummary error={error} title={t("mutationFailed")} />
        <FieldSet><FieldLegend>{t("endpointSection")}</FieldLegend><FieldGroup>
          {mode.kind === "create" && mode.backendId === undefined ? <Field><FieldLabel htmlFor="api-upstream-backend">{t("backend")}</FieldLabel><EntityPicker id="api-upstream-backend" entity="api-backend" apiServiceId={mode.serviceId} value={values.backendID} onChange={(backendID) => setValues((current) => ({ ...current, backendID }), true)} /></Field> : <Field><FieldLabel>{t("backend")}</FieldLabel><div className="rounded-md border bg-muted/30 px-3 py-2 text-sm"><span className="font-medium">{backend.data?.name ?? t("backend")}</span><FieldDescription>{t(mode.kind === "create" ? "backendLocked" : "backendImmutable")}</FieldDescription></div></Field>}
          <Field><FieldLabel htmlFor="api-upstream-name">{t("name")}</FieldLabel><Input id="api-upstream-name" value={values.name} onChange={(event) => setValues((current) => ({ ...current, name: event.target.value }))} required /></Field>
          <Field data-invalid={baseURLInvalid || undefined}><FieldLabel htmlFor="api-upstream-base-url">{t("baseUrl")}</FieldLabel><Input id="api-upstream-base-url" value={values.baseURL} maxLength={2048} aria-invalid={baseURLInvalid} onChange={(event) => setValues((current) => ({ ...current, baseURL: event.target.value }), true)} required />{baseURLInvalid ? <FieldError>{t("endpointPreviewInvalid")}</FieldError> : null}</Field>
          <Field><FieldLabel>{t("endpointRouteResults")}</FieldLabel>{routes.isLoading ? <FieldGroup className="gap-3"><Skeleton className="h-20 w-full" /><Skeleton className="h-20 w-full" /></FieldGroup> : routes.error ? <Alert variant="destructive"><AlertTitle>{t("routesLoadFailed")}</AlertTitle><AlertDescription>{t("routesLoadFailedDescription")}</AlertDescription></Alert> : <EndpointRouteResults routes={targetRoutes} values={values} serviceID={mode.serviceId} backendID={backendID} session={identity} publishImmediately={mode.kind !== "create" && !(draft?.identity === identity && draft.previewChanged)} t={t} />}</Field>
        </FieldGroup></FieldSet>
        <FieldSet><FieldLegend>{t("upstreamAuthentication")}</FieldLegend><FieldDescription>{t("upstreamAuthenticationDescription")}</FieldDescription><FieldGroup>
          <Field><FieldLabel htmlFor="api-upstream-auth-type">{t("authType")}</FieldLabel><Select value={values.authType} onValueChange={(authType) => setValues((current) => ({ ...current, authType: authType as APIUpstreamAuthType, credential: emptyCredential, clearCredential: false }))} disabled={values.clearCredential}><SelectTrigger id="api-upstream-auth-type" aria-label={t("authType")}><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{authTypes.map((type) => <SelectItem key={type} value={type}>{type}</SelectItem>)}</SelectGroup></SelectContent></Select></Field>
          {!values.clearCredential ? <UpstreamCredentialFields idPrefix="api-upstream" authType={values.authType} credential={values.credential} onChange={(credential) => setValues((current) => ({ ...current, credential }))} /> : null}
          {mode.kind === "edit" && query.data?.credential_configured ? <Field orientation="horizontal"><Switch id="api-upstream-clear-credential" checked={values.clearCredential} onCheckedChange={(clearCredential) => setValues((current) => ({ ...current, clearCredential }))} /><FieldLabel htmlFor="api-upstream-clear-credential">{t("clearCredential")}</FieldLabel><Badge variant="secondary">{t("credentialConfigured")}</Badge></Field> : null}
        </FieldGroup></FieldSet>
        <FieldSet><FieldLegend>{t("upstreamConnection")}</FieldLegend><FieldGroup>
          <Field orientation="horizontal"><Switch id="api-upstream-enabled" checked={values.enabled} onCheckedChange={(enabled) => setValues((current) => ({ ...current, enabled }), true)} /><FieldLabel htmlFor="api-upstream-enabled">{t("enabled")}</FieldLabel></Field>
        </FieldGroup></FieldSet>
        <FieldSet><FieldLegend>{t("upstreamAdvanced")}</FieldLegend><FieldDescription>{t("upstreamAdvancedDescription")}</FieldDescription><Collapsible><CollapsibleTrigger asChild><Button type="button" variant="outline">{t("showAdvanced")}</Button></CollapsibleTrigger><CollapsibleContent><FieldGroup className="pt-4">
          <FieldGroup className="grid grid-cols-1 gap-4 sm:grid-cols-2"><Field><FieldLabel htmlFor="api-upstream-weight">{t("weight")}</FieldLabel><Input id="api-upstream-weight" inputMode="numeric" value={values.weight} onChange={(event) => setValues((current) => ({ ...current, weight: event.target.value }))} /></Field><Field><FieldLabel htmlFor="api-upstream-priority">{t("priority")}</FieldLabel><Input id="api-upstream-priority" inputMode="numeric" value={values.priority} onChange={(event) => setValues((current) => ({ ...current, priority: event.target.value }))} /></Field></FieldGroup>
          <Field><FieldLabel htmlFor="api-upstream-proxy-url">{t("proxyUrl")}</FieldLabel><Input id="api-upstream-proxy-url" value={values.proxyURL} disabled={values.clearProxy} onChange={(event) => setValues((current) => ({ ...current, proxyURL: event.target.value, clearProxy: false }))} /><FieldDescription>{mode.kind === "edit" && query.data?.proxy_url_configured ? t("proxyConfigured") : t("proxyOptional")}</FieldDescription></Field>
          {mode.kind === "edit" && query.data?.proxy_url_configured ? <Field orientation="horizontal"><Switch id="api-upstream-clear-proxy" checked={values.clearProxy} onCheckedChange={(clearProxy) => setValues((current) => ({ ...current, clearProxy }))} /><FieldLabel htmlFor="api-upstream-clear-proxy">{t("clearProxy")}</FieldLabel></Field> : null}
          <UpstreamHeaderFields idPrefix="api-upstream" rows={values.headerRows} onChange={(headerRows) => setValues((current) => ({ ...current, headerRows }))} t={t} />
        </FieldGroup></CollapsibleContent></Collapsible></FieldSet>
      </form>
      {confirmLastEndpoint ? <DeleteConfirmationDialog open onOpenChange={setConfirmLastEndpoint} subject={query.data?.name ?? values.name} title={t("confirmDisableEndpointTitle")} confirmLabel={t("confirmDisableEndpoint")} description={t("lastEndpointDisableDescription", { count: backend.data?.route_count ?? 0 })} onConfirm={() => save(true)} /> : null}
    </PageLayout>
  );
}
