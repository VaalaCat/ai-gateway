"use client";

import { useTranslations } from "next-intl";

import { LiveTabHeader } from "@/components/monitoring/live-tab-header";
import { LimiterDashboard } from "@/components/observability/limiter-dashboard";

export function LimiterTab() {
  const t = useTranslations("monitoring");
  return (
    <div className="space-y-4">
      <LiveTabHeader title={t("tab.limiter")} />
      <LimiterDashboard />
    </div>
  );
}
