"use client";

import { useState, useSyncExternalStore, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { FormErrorSummary, useFormErrorReport } from "@/components/business/form-error-summary";
import { NumberUnitInput } from "@/components/business/number-unit-input";
import { PageLayout } from "@/components/layout/page-layout";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { useAPIService, useCreateAPIService, useUpdateAPIService, type APIService } from "@/lib/api/api-services";
import { humanizeNumberUnit } from "@/lib/utils/number-unit";

import { FormPageSkeleton } from "./form-entry";
import { SegmentedURL } from "./segmented-url";

export type APIServiceFormMode = { kind: "create" } | { kind: "edit"; id: number };

interface ServiceValues { name: string; slug: string; description: string; price: string; enabled: boolean }
const emptyValues: ServiceValues = { name: "", slug: "", description: "", price: "", enabled: true };

function valuesFrom(service: APIService): ServiceValues {
  return { name: service.name, slug: service.slug, description: service.description, price: String(service.price_per_call), enabled: service.status === 1 };
}

function statusOf(error: unknown) {
  return typeof error === "object" && error !== null && "status" in error ? (error as { status?: number }).status : undefined;
}

function useCurrentOrigin() {
  return useSyncExternalStore(() => () => {}, () => window.location.origin, () => "");
}

function serviceBaseURL(origin: string, slug: string) {
  const prefix = origin ? `${origin}/v1/api/` : "/v1/api/";
  const encodedSlug = encodeURIComponent(slug);
  return {
    text: `${prefix}${encodedSlug}`,
    segments: encodedSlug ? [{ start: prefix.length, end: prefix.length + encodedSlug.length, kind: "service" as const, label: slug }] : [],
  };
}

export function APIServiceForm({ mode }: { mode: APIServiceFormMode }) {
  const t = useTranslations("apiServices");
  const tc = useTranslations("common");
  const router = useRouter();
  const origin = useCurrentOrigin();
  const query = useAPIService(mode.kind === "edit" ? mode.id : 0, { enabled: mode.kind === "edit" });
  const create = useCreateAPIService();
  const update = useUpdateAPIService();
  const identity = mode.kind === "edit" ? `edit:${mode.id}` : "create";
  const queryMatches = mode.kind === "edit" && query.data?.id === mode.id;
  const [draft, setDraft] = useState<{ identity: string; values: ServiceValues }>();
  const values = draft?.identity === identity ? draft.values : queryMatches && query.data ? valuesFrom(query.data) : emptyValues;
  const setValues = (update: (current: ServiceValues) => ServiceValues) => setDraft({ identity, values: update(values) });
  const { error, clearError, reportError } = useFormErrorReport();

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    clearError();
    const price = Number(values.price);
    if (values.price.trim() === "" || !Number.isSafeInteger(price) || price < 0) { reportError(t("invalidPrice")); return; }
    const body = { name: values.name.trim(), slug: values.slug.trim(), description: values.description, price_per_call: price, status: values.enabled ? 1 : 0 };
    try {
      if (mode.kind === "edit") { await update.mutateAsync({ id: mode.id, ...body }); toast.success(tc("success")); router.push(`/api-services/detail?id=${mode.id}`); }
      else { const service = await create.mutateAsync(body); toast.success(tc("success")); router.push(`/api-services/detail?id=${service.id}`); }
    } catch (reason) { reportError(reason instanceof Error ? reason.message : t("mutationFailed")); }
  };

  if (mode.kind === "edit" && query.isLoading) return <FormPageSkeleton titleKey="editService" descriptionKey="serviceFormDescription" />;
  if (mode.kind === "edit" && statusOf(query.error) === 404) return <PageLayout title={t("editService")}><Alert><AlertTitle>{t("serviceNotFound")}</AlertTitle><AlertDescription>{t("serviceNotFoundDescription")}</AlertDescription></Alert></PageLayout>;
  if (mode.kind === "edit" && query.error) return <PageLayout title={t("editService")}><Alert variant="destructive"><AlertTitle>{t(statusOf(query.error) === 403 ? "permissionDenied" : "loadFailed")}</AlertTitle><AlertDescription>{t("loadFailedDescription")}</AlertDescription></Alert></PageLayout>;
  if (mode.kind === "edit" && !queryMatches) return <PageLayout title={t("editService")}><Alert><AlertTitle>{t("serviceNotFound")}</AlertTitle><AlertDescription>{t("serviceNotFoundDescription")}</AlertDescription></Alert></PageLayout>;
  const pending = create.isPending || update.isPending;
  const cancelPath = mode.kind === "edit" ? `/api-services/detail?id=${mode.id}` : "/api-services";
  return (
    <PageLayout
      title={t(mode.kind === "edit" ? "editService" : "createService")}
      description={t("serviceFormDescription")}
      maxWidth="3xl"
      footer={<><Button type="button" variant="outline" onClick={() => router.push(cancelPath)}>{t("cancel")}</Button><Button type="submit" form="api-service-form" disabled={pending}>{t("save")}</Button></>}
    >
      <form id="api-service-form" onSubmit={submit} className="flex flex-col gap-6">
        <FormErrorSummary error={error} title={t("mutationFailed")} />
        <FieldSet><FieldLegend>{t("serviceIdentity")}</FieldLegend><FieldDescription>{t("serviceIdentityDescription")}</FieldDescription><FieldGroup>
          <Field><FieldLabel htmlFor="api-service-name">{t("name")}</FieldLabel><Input id="api-service-name" value={values.name} onChange={(event) => setValues((current) => ({ ...current, name: event.target.value }))} required /></Field>
          <Field><FieldLabel htmlFor="api-service-slug">{t("slug")}</FieldLabel><Input id="api-service-slug" value={values.slug} onChange={(event) => setValues((current) => ({ ...current, slug: event.target.value }))} required /></Field>
          <Field><FieldLabel>{t("serviceBaseUrlResult")}</FieldLabel><div data-testid="service-base-url-result" className="min-w-0 rounded-md border bg-muted/30 p-3 [&_[data-slot=button]]:size-11"><SegmentedURL {...serviceBaseURL(origin, values.slug)} copyLabel={t("copyBaseUrl")} /></div><FieldDescription>{t("serviceBaseUrlDescription")}</FieldDescription></Field>
          <Field><FieldLabel htmlFor="api-service-description">{t("descriptionField")}</FieldLabel><Textarea id="api-service-description" value={values.description} onChange={(event) => setValues((current) => ({ ...current, description: event.target.value }))} /></Field>
        </FieldGroup></FieldSet>
        <FieldSet><FieldLegend>{t("servicePolicy")}</FieldLegend><FieldGroup>
          <Field data-invalid={error?.message === t("invalidPrice") || undefined}><FieldLabel htmlFor="api-service-price">{t("pricePerCall")}</FieldLabel><NumberUnitInput id="api-service-price" inputMode="numeric" unit="quota" value={values.price} humanReadable={humanizeNumberUnit(values.price, "quota")} onChange={(event) => setValues((current) => ({ ...current, price: event.target.value }))} aria-invalid={error?.message === t("invalidPrice")} /></Field>
          <Field orientation="horizontal"><Switch id="api-service-enabled" checked={values.enabled} onCheckedChange={(enabled) => setValues((current) => ({ ...current, enabled }))} /><FieldLabel htmlFor="api-service-enabled">{t("enabled")}</FieldLabel></Field>
        </FieldGroup></FieldSet>
      </form>
    </PageLayout>
  );
}
