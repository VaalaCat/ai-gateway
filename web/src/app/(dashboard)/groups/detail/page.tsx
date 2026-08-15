"use client";

import { Suspense } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { ArrowLeft } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { StatusBadge } from "@/components/business/status-badge";
import { DateCell } from "@/components/business/date-cell";
import { PageHeader } from "@/components/layout/page-header";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import { useUserGroup, DEFAULT_GROUP_ID } from "@/lib/api/user-groups";

import { OverviewTab } from "./overview-tab";
import { MembersTab } from "./members-tab";

export default function GroupDetailPage() {
  const router = useRouter();
  const t = useTranslations("userGroups");
  const tc = useTranslations("common");
  const backAction = (
    <Button variant="ghost" size="icon" className="size-8 shrink-0" aria-label={tc("back")} onClick={() => router.push("/groups")}>
      <ArrowLeft className="size-4" />
    </Button>
  );
  return (
    <Suspense fallback={
      <div className="flex min-w-0 flex-col">
        <PageHeader title={t("title")} backAction={backAction} />
        <div className="flex flex-col gap-3 py-8" aria-label={tc("loading")}><Skeleton className="h-8 w-48" /><Skeleton className="h-56 w-full" /></div>
      </div>
    }>
      <GroupDetailContent />
    </Suspense>
  );
}

function GroupDetailContent() {
  const sp = useSearchParams();
  const router = useRouter();
  const t = useTranslations("userGroups");
  const tc = useTranslations("common");

  const id = Number(sp.get("id"));
  const tab = sp.get("tab") || "overview";

  const { data: group, isLoading, isError } = useUserGroup(id);

  const onTabChange = (next: string) => {
    const params = new URLSearchParams(sp);
    params.set("tab", next);
    router.replace(`/groups/detail?${params.toString()}`);
  };
  const backAction = (
    <Button variant="ghost" size="icon" className="size-8 shrink-0" aria-label={tc("back")} onClick={() => router.push("/groups")}>
      <ArrowLeft className="size-4" />
    </Button>
  );

  if (isError) {
    return (
      <div className="flex min-w-0 flex-col">
        <PageHeader title={t("title")} backAction={backAction} />
        <Alert variant="destructive" role="alert"><AlertDescription>{t("notFound")}</AlertDescription></Alert>
      </div>
    );
  }

  if (isLoading) {
    return (
      <div className="flex min-w-0 flex-col">
        <PageHeader title={t("title")} backAction={backAction} />
        <div className="flex flex-col gap-3 py-8" aria-label={tc("loading")}><Skeleton className="h-8 w-48" /><Skeleton className="h-56 w-full" /></div>
      </div>
    );
  }
  if (!group) {
    return (
      <div className="flex min-w-0 flex-col">
        <PageHeader title={t("title")} backAction={backAction} />
        <Alert role="alert"><AlertDescription>{tc("noData")}</AlertDescription></Alert>
      </div>
    );
  }

  const isDefault = group.id === DEFAULT_GROUP_ID;

  return (
    <div className="flex min-w-0 flex-col">
      <PageHeader
        title={group.name}
        description={group.description}
        backAction={backAction}
        metadata={<><StatusBadge status={group.status} /><span className="font-mono text-xs text-muted-foreground">{tc("id")}: {group.id}</span>{isDefault && <Badge variant="outline">{t("default")}</Badge>}<span className="text-xs text-muted-foreground">{t("userCount")}: {group.user_count ?? 0}</span><span className="text-xs text-muted-foreground"><DateCell timestamp={group.created_at} /></span></>}
      />

      <Tabs value={tab} onValueChange={onTabChange}>
        <TabsList>
          <TabsTrigger value="overview">{t("overviewTab")}</TabsTrigger>
          <TabsTrigger value="members">
            {t("membersTab")}{group.user_count != null ? ` (${group.user_count})` : ""}
          </TabsTrigger>
        </TabsList>
        <TabsContent value="overview" className="mt-4">
          <OverviewTab group={group} isDefault={isDefault} />
        </TabsContent>
        <TabsContent value="members" className="mt-4">
          <MembersTab groupId={group.id} />
        </TabsContent>
      </Tabs>
    </div>
  );
}
