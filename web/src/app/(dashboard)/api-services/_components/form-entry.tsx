"use client";

import type { ReactNode } from "react";
import { useTranslations } from "next-intl";

import { PageLayout } from "@/components/layout/page-layout";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { canCreateAPIService, canManageAPIService, useCapabilities } from "@/lib/api/capabilities";
import { useAuth } from "@/lib/auth";

export function readPositiveID(raw: string | null) {
  const value = Number(raw);
  return Number.isSafeInteger(value) && value > 0 ? value : undefined;
}

export function FormPageSkeleton({ titleKey, descriptionKey }: { titleKey: string; descriptionKey?: string }) {
  const t = useTranslations("apiServices");
  return <PageLayout title={t(titleKey)} description={descriptionKey ? t(descriptionKey) : undefined} maxWidth="3xl"><div className="flex flex-col gap-4"><Skeleton className="h-8 w-48" /><Skeleton className="h-80 w-full" /></div></PageLayout>;
}

export function InvalidFormEntry({ titleKey, subjectKey }: { titleKey: string; subjectKey: string }) {
  const t = useTranslations("apiServices");
  return <PageLayout title={t(titleKey)}><Alert><AlertTitle>{t(subjectKey)}</AlertTitle><AlertDescription>{t(`${subjectKey}Description`)}</AlertDescription></Alert></PageLayout>;
}

export function RetryableFormEntryError({ titleKey, onRetry }: { titleKey: string; onRetry: () => void }) {
  const t = useTranslations("apiServices");
  return <PageLayout title={t(titleKey)}><Alert variant="destructive"><AlertTitle>{t("loadFailed")}</AlertTitle><AlertDescription className="flex flex-col items-start gap-3"><span>{t("loadFailedDescription")}</span><Button type="button" variant="outline" size="sm" onClick={onRetry}>{t("retry")}</Button></AlertDescription></Alert></PageLayout>;
}

type FormEntryPermission = { kind: "create" } | { kind: "manage"; serviceId: number };

function errorStatus(error: unknown) {
  return typeof error === "object" && error !== null && "status" in error
    ? (error as { status?: number }).status
    : undefined;
}

function FormEntryMessage({ pageTitleKey, pageDescriptionKey, titleKey, descriptionKey, destructive = false }: { pageTitleKey: string; pageDescriptionKey?: string; titleKey: string; descriptionKey: string; destructive?: boolean }) {
  const t = useTranslations("apiServices");
  return <PageLayout title={t(pageTitleKey)} description={pageDescriptionKey ? t(pageDescriptionKey) : undefined} maxWidth="3xl"><Alert variant={destructive ? "destructive" : "default"}><AlertTitle>{t(titleKey)}</AlertTitle><AlertDescription>{t(descriptionKey)}</AlertDescription></Alert></PageLayout>;
}

export function APIServiceFormEntryGuard({ permission, titleKey, descriptionKey, children }: { permission: FormEntryPermission; titleKey: string; descriptionKey?: string; children: ReactNode }) {
  const { user } = useAuth();
  const capability = useCapabilities(user?.user_id);
  if (capability.isPending || capability.isLoading) return <FormPageSkeleton titleKey={titleKey} descriptionKey={descriptionKey} />;
  if (capability.error) {
    const forbidden = errorStatus(capability.error) === 403;
    return <FormEntryMessage pageTitleKey={titleKey} pageDescriptionKey={descriptionKey} titleKey={forbidden ? "permissionDenied" : "loadFailed"} descriptionKey={forbidden ? "permissionDeniedDescription" : "loadFailedDescription"} destructive />;
  }
  if (capability.data?.generic_api?.services !== true) return <FormEntryMessage pageTitleKey={titleKey} pageDescriptionKey={descriptionKey} titleKey="unavailable" descriptionKey="permissionRequired" />;
  const allowed = permission.kind === "create"
    ? canCreateAPIService(capability.data)
    : canManageAPIService(capability.data, permission.serviceId);
  if (!allowed) return <FormEntryMessage pageTitleKey={titleKey} pageDescriptionKey={descriptionKey} titleKey="permissionDenied" descriptionKey="permissionDeniedDescription" destructive />;
  return children;
}
