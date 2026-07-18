"use client";

import { useTranslations } from "next-intl";

import { LiveTabHeader } from "@/components/monitoring/live-tab-header";
import { DeliveryBoard } from "@/components/observability/delivery-board";

export function DeliveryTab() {
  const t = useTranslations("monitoring");
  return (
    <div className="space-y-4">
      <LiveTabHeader title={t("tab.delivery")} />
      <DeliveryBoard />
    </div>
  );
}
