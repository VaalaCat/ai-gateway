"use client";

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { FormErrorSummary, useFormErrorReport } from "@/components/business/form-error-summary";
import { PageLayout } from "@/components/layout/page-layout";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { ApiError } from "@/lib/api/client";
import { useAllAPIRoutes, useAPIBackend, useCreateAPIBackend, useUpdateAPIBackend } from "@/lib/api/api-services";

import { BackendImpactAlert } from "./backend-impact-alert";
import { FormPageSkeleton } from "./form-entry";
import { serviceDetailReturnPath, type RouteReturnContext } from "./route-return";

export type APIBackendFormMode = ({ kind: "create"; serviceId: number } | { kind: "edit"; id: number; serviceId: number }) & { returnRoute?: RouteReturnContext };

export function targetReturnPath(serviceID: number, route?: RouteReturnContext) {
  return serviceDetailReturnPath(serviceID, route);
}

function TargetRouteList({ routes, backendID, onRetry, t }: { routes: ReturnType<typeof useAllAPIRoutes>; backendID: number; onRetry: () => void; t: (key: string) => string }) {
  const referencedRoutes = (routes.data ?? []).filter((route) => route.backend_id === backendID);
  return <div data-testid="target-route-list">{
    routes.isLoading
      ? <div role="status" aria-label={t("targetRoutesLoading")}><Skeleton className="h-6 w-full" /></div>
      : routes.error
        ? <Alert variant="destructive"><AlertTitle>{t("targetRoutesLoadFailed")}</AlertTitle><AlertDescription className="flex flex-col items-start gap-3"><span>{t("targetRoutesLoadFailedDescription")}</span><Button type="button" variant="outline" size="sm" onClick={onRetry}>{t("retry")}</Button></AlertDescription></Alert>
        : referencedRoutes.length
          ? <ul className="flex flex-wrap gap-2 text-sm text-muted-foreground">{referencedRoutes.map((route) => <li key={route.id}><code>/{route.slug}</code></li>)}</ul>
          : <p className="text-sm text-muted-foreground">{t("targetNoRoutes")}</p>
  }</div>;
}

export function APIBackendForm({ mode }: { mode: APIBackendFormMode }) {
  const t = useTranslations("apiServices"); const tc = useTranslations("common"); const router = useRouter();
  const query = useAPIBackend(mode.kind === "edit" ? mode.id : 0, { enabled: mode.kind === "edit" }); const create = useCreateAPIBackend(); const update = useUpdateAPIBackend();
  const routes = useAllAPIRoutes(mode.serviceId, { enabled: mode.kind === "edit" });
  const identity = mode.kind === "edit" ? `edit:${mode.serviceId}:${mode.id}` : `create:${mode.serviceId}`;
  const [draft, setDraft] = useState<{ identity: string; name: string }>(); const value = draft?.identity === identity ? draft.name : mode.kind === "edit" && query.data ? query.data.name : ""; const { error, clearError, reportError } = useFormErrorReport();
  const cancelPath = targetReturnPath(mode.serviceId, mode.returnRoute);
  if (mode.kind === "edit" && query.isLoading) return <FormPageSkeleton titleKey="editTarget" descriptionKey="targetFormDescription" />;
  if (mode.kind === "edit" && (query.error || query.data?.api_service_id !== mode.serviceId)) return <PageLayout title={t("editTarget")}><Alert variant="destructive"><AlertTitle>{t("targetNotFound")}</AlertTitle><AlertDescription>{t("targetNotFoundDescription")}</AlertDescription></Alert></PageLayout>;
  const submit = async (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); clearError(); const name = value.trim(); if (!name || name.length > 64) { reportError(t("invalidTargetName")); return; } try { if (mode.kind === "edit") { await update.mutateAsync({ id: mode.id, name }); toast.success(tc("success")); router.push(targetReturnPath(mode.serviceId, mode.returnRoute)); } else { await create.mutateAsync({ api_service_id: mode.serviceId, name }); toast.success(tc("success")); router.push(targetReturnPath(mode.serviceId, mode.returnRoute)); } } catch (reason) { if (reason instanceof ApiError && reason.body?.code === "backend_name_conflict") reportError(t("backendNameConflict")); else reportError(reason instanceof Error ? reason.message : t("mutationFailed")); } };
  return <PageLayout title={t(mode.kind === "edit" ? "editTarget" : "createTarget")} description={t("targetFormDescription")} maxWidth="3xl" footer={<><Button type="button" variant="outline" onClick={() => router.push(cancelPath)}>{t("cancel")}</Button><Button type="submit" form="api-backend-form" disabled={create.isPending || update.isPending}>{mode.kind === "edit" && query.data?.route_count ? t("saveTargetImpact", { count: query.data.route_count }) : t("save")}</Button></>}><form id="api-backend-form" onSubmit={submit} className="flex flex-col gap-6"><FormErrorSummary error={error} title={t("mutationFailed")} />{mode.kind === "edit" && query.data?.route_count ? <BackendImpactAlert title={t("sharedTargetImpact", { count: query.data.route_count })} description={t("sharedTargetImpactDescription", { count: query.data.route_count })} /> : null}<FieldSet><FieldLegend>{t("targetIdentity")}</FieldLegend><FieldGroup><Field><FieldLabel htmlFor="api-backend-name">{t("name")}</FieldLabel><Input id="api-backend-name" value={value} maxLength={64} onChange={(event) => setDraft({ identity, name: event.target.value })} required /></Field>{mode.kind === "edit" && query.data ? <Field><FieldLabel>{t("targetCurrentImpact")}</FieldLabel><div className="flex min-w-0 flex-col gap-2 rounded-md border bg-muted/30 p-3"><p className="text-sm font-medium">{t("targetImpactSummary", { routes: query.data.route_count, endpoints: query.data.upstream_count })}</p><TargetRouteList routes={routes} backendID={mode.id} onRetry={() => void routes.refetch()} t={t} /></div></Field> : null}</FieldGroup></FieldSet></form></PageLayout>;
}
