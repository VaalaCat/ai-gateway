"use client";

import { Suspense } from "react";
import { useRouter, useSearchParams, usePathname } from "next/navigation";
import { useTranslations } from "next-intl";

import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { OverviewTab } from "@/components/monitoring/overview-tab";
import { InflightTab } from "@/components/monitoring/inflight-tab";
import { LimiterTab } from "@/components/monitoring/limiter-tab";
import { BreakerTab } from "@/components/monitoring/breaker-tab";
import { DeliveryTab } from "@/components/monitoring/delivery-tab";
import { PageHeader } from "@/components/layout/page-header";
import { PageLayoutSkeleton } from "@/components/layout/page-layout-skeleton";

const TABS = ["overview", "inflight", "limiter", "breaker", "delivery"] as const;
type TabKey = (typeof TABS)[number];

export default function MonitoringPage() {
  const t = useTranslations("monitoring");
  return (
    <Suspense
      fallback={<PageLayoutSkeleton title={t("title")} description={t("subtitle")} />}
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
    <div className="flex min-h-full flex-col">
      <PageHeader title={t("title")} description={t("subtitle")} />
      <div className="min-h-0 flex-1">
        <Tabs value={tab} onValueChange={setTab} className="space-y-6">
        <TabsList className="max-w-full justify-start overflow-x-auto">
          <TabsTrigger value="overview">{t("tab.overview")}</TabsTrigger>
          <TabsTrigger value="inflight">{t("tab.inflight")}</TabsTrigger>
          <TabsTrigger value="limiter">{t("tab.limiter")}</TabsTrigger>
          <TabsTrigger value="breaker">{t("tab.breaker")}</TabsTrigger>
          <TabsTrigger value="delivery">{t("tab.delivery")}</TabsTrigger>
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
        <TabsContent value="breaker">
          <BreakerTab />
        </TabsContent>
        <TabsContent value="delivery">
          <DeliveryTab />
        </TabsContent>
        </Tabs>
      </div>
    </div>
  );
}
