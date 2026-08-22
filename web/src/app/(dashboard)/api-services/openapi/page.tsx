"use client";

import { Suspense, useState, useSyncExternalStore } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { LoaderCircle } from "lucide-react";

import { PageLayout } from "@/components/layout/page-layout";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { ApiError } from "@/lib/api/client";
import { useGetOpenAPIDocument, useUpdateOpenAPIDocument } from "@/lib/api/api-services";

import { APIServiceFormEntryGuard, InvalidFormEntry, RetryableFormEntryError, readPositiveID } from "../_components/form-entry";
import { apiServiceErrorMessage } from "../api-service-error";
import { OpenAPIDocumentEditor } from "../_components/openapi-editor/openapi-document-editor";
import { buildOpenAPIUpdate, type OpenAPIDocumentSnapshot } from "../_components/openapi-editor/openapi-editor-state";

function useCurrentOrigin() { return useSyncExternalStore(() => () => {}, () => window.location.origin, () => ""); }

function EditorSkeleton() {
  const t = useTranslations("apiServices");
  return <PageLayout title={t("editOpenAPIDocument")} description={t("editOpenAPIDocumentDescription")} maxWidth="full"><div className="flex flex-col gap-4"><Skeleton className="h-9 w-64" /><Skeleton className="h-80 w-full" /></div></PageLayout>;
}

export function OpenAPIEditorWorkspace({ serviceID }: { serviceID: number }) {
  return <OpenAPIEditorWorkspaceState key={serviceID} serviceID={serviceID} />;
}

function OpenAPIEditorWorkspaceState({ serviceID }: { serviceID: number }) {
  const t = useTranslations("apiServices");
  const router = useRouter();
  const origin = useCurrentOrigin();
  const document = useGetOpenAPIDocument(serviceID);
  const update = useUpdateOpenAPIDocument(serviceID);
  const [draft, setDraft] = useState<OpenAPIDocumentSnapshot>();
  const [saveError, setSaveError] = useState<string>();
  const [conflict, setConflict] = useState(false);
  const [confirmReload, setConfirmReload] = useState(false);
  const [documentValid, setDocumentValid] = useState(true);
  const [refreshWarning, setRefreshWarning] = useState(false);
  if (document.isLoading && !document.data) return <EditorSkeleton />;
  const current = draft ?? document.data;
  if (!current) return <RetryableFormEntryError titleKey="editOpenAPIDocument" onRetry={() => void document.refetch()} />;
  const reload = async () => {
    const refreshed = await document.refetch();
    if (!refreshed.error && refreshed.data) { setDraft(refreshed.data); setConflict(false); setConfirmReload(false); setSaveError(undefined); setRefreshWarning(false); }
  };
  const save = async () => {
    if (update.isPending) return;
    setSaveError(undefined);
    setConflict(false);
    setRefreshWarning(false);
    try {
      const saved = await update.mutateAsync(buildOpenAPIUpdate(current));
      setDraft(saved);
      try {
        const refreshed = await document.refetch();
        if (!refreshed.error && refreshed.data) setDraft(refreshed.data);
        else setRefreshWarning(true);
      } catch { setRefreshWarning(true); }
    } catch (reason) {
      if (reason instanceof ApiError && reason.status === 409) { setConflict(true); return; }
      setSaveError(apiServiceErrorMessage(t, reason, "openAPIUpdateFailed"));
    }
  };
  return <PageLayout title={t("editOpenAPIDocument")} description={t("editOpenAPIDocumentDescription")} maxWidth="full" footer={<><Button type="button" variant="outline" size="lg" disabled={update.isPending} onClick={() => router.push(`/api-services/detail?id=${serviceID}`)}>{t("cancel")}</Button><Button type="button" size="lg" disabled={update.isPending || !documentValid} onClick={() => void save()}>{update.isPending ? <><LoaderCircle data-icon="inline-start" className="animate-spin" />{t("savingOpenAPIDocument")}</> : t("saveOpenAPIDocument")}</Button></>}>
    <div className="flex min-w-0 flex-col gap-4 pb-4">
      {conflict ? <Alert variant="destructive"><AlertTitle>{t("openAPIUpdateConflict")}</AlertTitle><AlertDescription className="flex flex-col items-start gap-3"><span>{t("openAPIUpdateConflictDescription")}</span>{confirmReload ? <span className="flex flex-wrap gap-2"><Button type="button" variant="destructive" size="sm" onClick={() => void reload()}>{t("confirmReloadOpenAPIDocument")}</Button><Button type="button" variant="outline" size="sm" onClick={() => setConfirmReload(false)}>{t("keepOpenAPIDraft")}</Button></span> : <Button type="button" variant="outline" size="sm" onClick={() => setConfirmReload(true)}>{t("reloadOpenAPIDocument")}</Button>}</AlertDescription></Alert> : null}
      {saveError ? <Alert variant="destructive"><AlertTitle>{t("openAPIUpdateFailed")}</AlertTitle><AlertDescription>{saveError}</AlertDescription></Alert> : null}
      {refreshWarning ? <Alert><AlertTitle>{t("openAPISavedRefreshFailed")}</AlertTitle><AlertDescription>{t("openAPISavedRefreshFailedDescription")}</AlertDescription></Alert> : null}
      <OpenAPIDocumentEditor snapshot={current} origin={origin} disabled={update.isPending} onChange={setDraft} onValidityChange={setDocumentValid} />
    </div>
  </PageLayout>;
}

function OpenAPIEditorContent() {
  const params = useSearchParams();
  const serviceID = readPositiveID(params.get("id"));
  if (!serviceID) return <InvalidFormEntry titleKey="editOpenAPIDocument" subjectKey="serviceNotFound" />;
  return <APIServiceFormEntryGuard permission={{ kind: "manage", serviceId: serviceID }} titleKey="editOpenAPIDocument" descriptionKey="editOpenAPIDocumentDescription"><OpenAPIEditorWorkspace serviceID={serviceID} /></APIServiceFormEntryGuard>;
}

export default function OpenAPIEditorPage() { return <Suspense fallback={<EditorSkeleton />}><OpenAPIEditorContent /></Suspense>; }
