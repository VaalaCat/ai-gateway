"use client";

import { useEffect, useRef } from "react";
import { useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";

import { PageLayout } from "@/components/layout/page-layout";
import { useSearchParamPatch } from "@/components/data-table/use-search-param-patch";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useCapabilities } from "@/lib/api/capabilities";
import { useAuth } from "@/lib/auth";

import { AccessGrantsTable } from "./_components/access-grants-table";
import { RolesTable } from "./_components/roles-table";

type AccessTab = "roles" | "grants";

function readTab(value: string | null): AccessTab {
  return value === "roles" ? "roles" : "grants";
}

function statusOf(error: unknown) {
  return typeof error === "object" && error !== null && "status" in error
    ? (error as { status?: number }).status
    : undefined;
}

function PageSkeleton() {
  return <div className="flex flex-col gap-4"><Skeleton className="h-8 w-48" /><Skeleton className="h-10 w-full" /><Skeleton className="h-64 w-full" /></div>;
}

function PageError({ error }: { error: unknown }) {
  const t = useTranslations("apiAccess");
  const key = statusOf(error) === 403 ? "permissionDenied" : "loadFailed";
  return <Alert variant="destructive"><AlertTitle>{t(key)}</AlertTitle><AlertDescription>{t(`${key}Description`)}</AlertDescription></Alert>;
}

export default function APIAccessPage() {
  const t = useTranslations("apiAccess");
  const { user } = useAuth();
  const capability = useCapabilities(user?.user_id);
  const searchParams = useSearchParams();
  const patchSearchParams = useSearchParamPatch();
  const rawTab = searchParams.get("tab");
  const tab = readTab(rawTab);
  const selectedTabRef = useRef<AccessTab>(tab);
  const enabled = capability.data?.generic_api?.access === true;

  useEffect(() => { selectedTabRef.current = tab; }, [tab]);

  useEffect(() => {
    if (rawTab !== null && rawTab !== "roles" && rawTab !== "grants") {
      patchSearchParams({ tab: undefined, page: undefined });
    }
  }, [patchSearchParams, rawTab]);

  return (
    <PageLayout title={t("title")} description={t("description")} maxWidth="full">
      {capability.isPending || capability.isLoading ? <PageSkeleton /> : capability.error ? (
        <PageError error={capability.error} />
      ) : !enabled ? (
        <Alert><AlertTitle>{t("unavailable")}</AlertTitle><AlertDescription>{t("permissionRequired")}</AlertDescription></Alert>
      ) : (
        <Tabs
          value={tab}
          onValueChange={(value) => {
            const nextTab = value === "grants" ? "grants" : "roles";
            if (selectedTabRef.current === nextTab) return;
            selectedTabRef.current = nextTab;
            patchSearchParams({ tab: nextTab === "roles" ? "roles" : undefined, page: undefined });
          }}
          className="flex flex-col gap-4"
        >
          <TabsList aria-label={t("tabs")}>
            <TabsTrigger value="roles">{t("roles")}</TabsTrigger>
            <TabsTrigger value="grants">{t("grants")}</TabsTrigger>
          </TabsList>
          <TabsContent value="roles" className="mt-0"><RolesTable enabled={tab === "roles"} /></TabsContent>
          <TabsContent value="grants" className="mt-0"><AccessGrantsTable enabled={tab === "grants"} /></TabsContent>
        </Tabs>
      )}
    </PageLayout>
  );
}
