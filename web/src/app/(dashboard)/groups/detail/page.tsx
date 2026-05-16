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
import { useUserGroup, DEFAULT_GROUP_ID } from "@/lib/api/user-groups";

import { OverviewTab } from "./overview-tab";
import { MembersTab } from "./members-tab";

export default function GroupDetailPage() {
  return (
    <Suspense fallback={
      <div className="flex items-center justify-center py-12 text-muted-foreground">Loading...</div>
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

  if (isError) {
    return (
      <div className="space-y-4">
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="icon" className="size-8" onClick={() => router.push("/groups")}>
            <ArrowLeft className="size-4" />
          </Button>
        </div>
        <p className="text-muted-foreground">{t("notFound")}</p>
        <Button variant="outline" onClick={() => router.push("/groups")}>← {t("title")}</Button>
      </div>
    );
  }

  if (isLoading || !group) {
    return (
      <div className="flex items-center justify-center py-12 text-muted-foreground">
        {tc("loading")}
      </div>
    );
  }

  const isDefault = group.id === DEFAULT_GROUP_ID;

  return (
    <div className="space-y-4">
      {/* Top info bar */}
      <div className="flex items-start gap-2">
        <Button variant="ghost" size="icon" className="size-8 shrink-0" onClick={() => router.push("/groups")}>
          <ArrowLeft className="size-4" />
        </Button>
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <h1 className="text-lg font-semibold truncate">{group.name}</h1>
            {isDefault && <Badge variant="outline">{t("default")}</Badge>}
            <StatusBadge status={group.status} />
          </div>
          {group.description && (
            <p className="text-sm text-muted-foreground mt-0.5 truncate">{group.description}</p>
          )}
          <p className="text-xs text-muted-foreground mt-1">
            <DateCell timestamp={group.created_at} />
          </p>
        </div>
      </div>

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
