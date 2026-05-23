"use client";

import { useTranslations } from "next-intl";

import { TrendChart } from "@/components/business/trend-chart";
import type { TimeBucket } from "@/lib/types/observability";

export function TrendSection({ buckets }: { buckets: TimeBucket[] }) {
  const t = useTranslations("insights.section");
  return <TrendChart buckets={buckets} title={t("trend")} />;
}
