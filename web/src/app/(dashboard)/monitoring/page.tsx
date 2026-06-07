"use client";

import { Suspense } from "react";
import { useRouter, useSearchParams, usePathname } from "next/navigation";
import { useTranslations } from "next-intl";

import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { OverviewTab } from "@/components/monitoring/overview-tab";
import { InflightTab } from "@/components/monitoring/inflight-tab";
import { LimiterTab } from "@/components/monitoring/limiter-tab";

const TABS = ["overview", "inflight", "limiter"] as const;
type TabKey = (typeof TABS)[number];

export default function MonitoringPage() {
  return (
    <Suspense
      fallback={
        <div className="py-12 text-center text-muted-foreground">Loading...</div>
      }
    >
      <Inner />
    </Suspense>
  );
}

function Inner() {
  const t = useTranslations("monitoring");
  const router = useRouter();
  const pathname = usePathname();
  const params = useSearchParams();

  const raw = params.get("tab") ?? "";
  const tab: TabKey = (TABS as readonly string[]).includes(raw)
    ? (raw as TabKey)
    : "overview";

  const setTab = (next: string) => {
    const sp = new URLSearchParams(params.toString());
    sp.set("tab", next);
    router.replace(`${pathname}?${sp.toString()}`, { scroll: false });
  };

  return (
    <Tabs value={tab} onValueChange={setTab} className="space-y-6">
      <TabsList>
        <TabsTrigger value="overview">{t("tab.overview")}</TabsTrigger>
        <TabsTrigger value="inflight">{t("tab.inflight")}</TabsTrigger>
        <TabsTrigger value="limiter">{t("tab.limiter")}</TabsTrigger>
      </TabsList>
      <TabsContent value="overview">
        <OverviewTab />
      </TabsContent>
      <TabsContent value="inflight">
        <InflightTab />
      </TabsContent>
      <TabsContent value="limiter">
        <LimiterTab />
      </TabsContent>
    </Tabs>
  );
}
