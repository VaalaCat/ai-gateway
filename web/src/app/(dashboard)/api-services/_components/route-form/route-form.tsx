"use client";

import { useCallback, useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { FormErrorSummary, useFormErrorReport } from "@/components/business/form-error-summary";
import { PageLayout } from "@/components/layout/page-layout";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { useAPIBackend, useAPIRoute, useCreateAPIRoute, useUpdateAPIRoute } from "@/lib/api/api-services";
import { apiServiceErrorMessage } from "../../api-service-error";
import { FormPageSkeleton } from "../form-entry";
import { RoutePathMapping } from "../route-editor/route-path-mapping";
import { RouteRequestPolicy } from "../route-editor/route-request-policy";
import { RouteRequestExample } from "../route-editor/route-request-example";
import { RouteTargetPicker } from "../route-editor/route-target-picker";
import { emptyRouteFormValues, hydrateRouteFormValues, routeFieldsForSubmit, routeFormReducer, routeTargetForRequest, validateRouteFormValues, type RouteFormAction, type RouteFormValues } from "./route-form-state";
import { RouteLivePreviewView, useRouteLivePreview, type RouteLivePreviewState, type RoutePreviewStatus } from "./route-live-preview";
import { serviceDetailReturnPath } from "../route-return";

export type APIRouteFormMode = { kind: "create"; serviceId: number; serviceSlug: string } | { kind: "edit"; id: number; serviceId: number; serviceSlug: string };

function statusOf(error: unknown) {
  return typeof error === "object" && error !== null && "status" in error ? (error as { status?: number }).status : undefined;
}

function publicOrigin() {
  return typeof window === "undefined" ? "" : window.location.origin;
}

export function APIRouteForm({ mode }: { mode: APIRouteFormMode }) {
  const identity = mode.kind === "edit" ? `edit:${mode.serviceId}:${mode.id}` : `create:${mode.serviceId}`;
  return <APIRouteFormSession key={identity} mode={mode} />;
}

function RouteFormFields({ mode, values, targetHeaderRows, preview, sampleSubpath, errorKeys, existingTargetLookup, dispatch, onTargetHeaderRowsChange, t }: { mode: APIRouteFormMode; values: RouteFormValues; targetHeaderRows: Parameters<typeof RouteTargetPicker>[0]["headerRows"]; preview: RouteLivePreviewState; sampleSubpath: string; errorKeys: string[]; existingTargetLookup: Parameters<typeof RouteTargetPicker>[0]["existingTargetLookup"]; dispatch: (action: RouteFormAction) => void; onTargetHeaderRowsChange: Parameters<typeof RouteTargetPicker>[0]["onHeaderRowsChange"]; t: (key: string, values?: Record<string, string | number | Date>) => string }) {
  const patch = (next: Partial<RouteFormValues>) => dispatch({ type: "patch", patch: next });
  return <div className="flex flex-col gap-8 rounded-lg border bg-background p-4 sm:p-6">
    <RoutePathMapping serviceSlug={mode.serviceSlug} origin={publicOrigin()} path={values.path} forwardSubpath={values.forwardSubpath} sampleSubpath={sampleSubpath} target={values.target} preview={preview} onPathChange={(path) => patch({ path })} pathError={errorKeys.includes("invalidRouteSlug") ? "invalidRouteSlug" : undefined} t={t} />
    <RouteTargetPicker target={values.target} headerRows={targetHeaderRows} serviceId={mode.serviceId} onTargetChange={(target) => dispatch({ type: "target", target })} onHeaderRowsChange={onTargetHeaderRowsChange} validationErrors={errorKeys} existingTargetLookup={existingTargetLookup} t={t} />
    <RouteRequestPolicy values={values} onChange={patch} validationErrors={errorKeys} t={t} />
    <RouteRequestExample values={values} onChange={patch} t={t} />
  </div>;
}

function previewDraft(mode: APIRouteFormMode, values: RouteFormValues, targetHeaderRows: Parameters<typeof RouteTargetPicker>[0]["headerRows"]) {
  if (values.target.mode === "existing" && values.target.backend_id < 1) return undefined;
  const fields = routeFieldsForSubmit(values);
  return {
    api_service_id: mode.serviceId,
    slug: fields.slug,
    upstream_path: fields.upstream_path,
    forward_subpath: fields.forward_subpath,
    sample: fields.example_request,
    target: routeTargetForRequest(values, targetHeaderRows),
  };
}

function APIRouteFormSession({ mode }: { mode: APIRouteFormMode }) {
  const t = useTranslations("apiServices");
  const tc = useTranslations("common");
  const router = useRouter();
  const route = useAPIRoute(mode.kind === "edit" ? mode.id : 0, { enabled: mode.kind === "edit" });
  const create = useCreateAPIRoute();
  const update = useUpdateAPIRoute();
  const identity = mode.kind === "edit" ? `edit:${mode.serviceId}:${mode.id}` : `create:${mode.serviceId}`;
  const matches = mode.kind === "edit" && route.data?.id === mode.id && route.data.api_service_id === mode.serviceId;
  const [draft, setDraft] = useState<{ identity: string; values: RouteFormValues; targetHeaderRows: Parameters<typeof RouteTargetPicker>[0]["headerRows"] }>();
  const [previewStatus, setPreviewStatus] = useState<RoutePreviewStatus>("valid");
  const values = draft?.identity === identity ? draft.values : matches && route.data ? hydrateRouteFormValues(route.data) : emptyRouteFormValues();
  const selectedBackendID = values.target.mode === "existing" ? values.target.backend_id : 0;
  const selectedBackend = useAPIBackend(selectedBackendID, { enabled: selectedBackendID > 0 });
  const existingTargetState: "loading" | "ready" | "missing" | "error" | "service-mismatch" | undefined = selectedBackendID < 1 ? undefined
    : selectedBackend.isLoading ? "loading"
    : selectedBackend.error ? (statusOf(selectedBackend.error) === 404 ? "missing" : "error")
    : !selectedBackend.data ? "missing"
    : selectedBackend.data.id !== selectedBackendID ? "missing"
    : selectedBackend.data.api_service_id !== mode.serviceId ? "service-mismatch"
    : "ready";
  const existingTargetLookup = existingTargetState ? { state: existingTargetState, retry: selectedBackend.refetch } : undefined;
  const targetHeaderRows = draft?.identity === identity ? draft.targetHeaderRows : [];
  const dispatch = (action: RouteFormAction) => setDraft({ identity, values: routeFormReducer(values, action), targetHeaderRows: action.type === "target" && action.target.mode === "existing" ? [] : targetHeaderRows });
  const setTargetHeaderRows = (rows: typeof targetHeaderRows) => setDraft({ identity, values, targetHeaderRows: rows });
  const { error, clearError, reportError } = useFormErrorReport();
  const detailPath = serviceDetailReturnPath(mode.serviceId, mode.kind === "edit" && route.data ? { id: mode.id, slug: route.data.slug } : undefined);
  const errorKeys = validateRouteFormValues(values, targetHeaderRows);
  const previewBlocksSave = previewStatus === "debouncing" || previewStatus === "pending" || previewStatus === "validation-error";
  const targetLookupBlocksSave = values.target.mode === "existing" && existingTargetState !== "ready";
  const updatePreviewStatus = useCallback((status: RoutePreviewStatus) => setPreviewStatus((current) => current === status ? current : status), []);
  const previewDraftValue = targetLookupBlocksSave ? undefined : previewDraft(mode, values, targetHeaderRows);
  const preview = useRouteLivePreview(previewDraftValue, updatePreviewStatus);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    clearError();
    if (errorKeys.length || previewBlocksSave || targetLookupBlocksSave) return reportError(t(errorKeys[0] ?? (existingTargetState === "missing" ? "targetNotFound" : existingTargetState === "service-mismatch" ? "targetServiceMismatch" : "targetLoadFailed")));
    const fields = routeFieldsForSubmit(values);
    const target = routeTargetForRequest(values, targetHeaderRows);
    try {
      const routeID = mode.kind === "edit"
        ? await update.mutateAsync({ id: mode.id, ...fields, target }).then(() => mode.id)
        : await create.mutateAsync({ api_service_id: mode.serviceId, ...fields, target }).then((result) => result.id);
      toast.success(tc("success"));
      router.push(serviceDetailReturnPath(mode.serviceId, { id: routeID, slug: fields.slug }));
    } catch (reason) {
      reportError(apiServiceErrorMessage(t, reason));
    }
  };

  if (mode.kind === "edit" && route.isLoading) return <FormPageSkeleton titleKey="editRoute" descriptionKey="routeFormDescription" />;
  if (mode.kind === "edit" && (route.error || !matches)) {
    const missing = statusOf(route.error) === 404 || !matches;
    return <PageLayout title={t("editRoute")}><Alert variant={missing ? "default" : "destructive"}><AlertTitle>{t(missing ? "routeNotFound" : "loadFailed")}</AlertTitle><AlertDescription>{t(missing ? "routeNotFoundDescription" : "loadFailedDescription")}</AlertDescription></Alert></PageLayout>;
  }

  return <PageLayout title={t(mode.kind === "edit" ? "editRoute" : "createRoute")} description={t("routeFormDescription")} maxWidth="3xl" footer={<><Button type="button" variant="outline" onClick={() => router.push(detailPath)}>{t("cancel")}</Button><Button type="submit" form="api-route-form" disabled={create.isPending || update.isPending || errorKeys.length > 0 || previewBlocksSave || targetLookupBlocksSave}>{t("save")}</Button></>}>
    <form id="api-route-form" onSubmit={submit} className="flex flex-col gap-6"><FormErrorSummary error={error} title={t("mutationFailed")} /><RouteFormFields mode={mode} values={values} targetHeaderRows={targetHeaderRows} preview={preview} sampleSubpath={previewDraftValue?.sample.subpath ?? ""} errorKeys={errorKeys} existingTargetLookup={existingTargetLookup} dispatch={dispatch} onTargetHeaderRowsChange={setTargetHeaderRows} t={t} /><RouteLivePreviewView draft={previewDraftValue} preview={preview} t={t} /></form>
  </PageLayout>;
}
