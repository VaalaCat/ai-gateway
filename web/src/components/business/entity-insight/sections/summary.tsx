"use client";

import { useTranslations } from "next-intl";

import { KpiGrid, type KpiItem } from "@/components/business/kpi-grid";
import type { SummaryKpis } from "@/lib/api/insights";
import { formatDuration, formatMoneyCompact } from "@/lib/utils/format";

export function SummarySection({ data }: { data: SummaryKpis }) {
  const t = useTranslations("insights.kpi");
  const successPct = data.success_rate * 100;
  const errorPct = 100 - successPct;

  const items: KpiItem[] = [
    {
      key: "requests",
      label: t("requests"),
      value: data.requests,
    },
    {
      key: "cost",
      label: t("cost"),
      value: formatMoneyCompact(data.cost),
    },
    {
      key: "tokens",
      label: t("tokens"),
      value: data.tokens,
    },
    {
      key: "ttftP95",
      label: t("ttftP95"),
      value: formatDuration(data.ttft_p95_ms),
    },
    {
      key: "tpsAvg",
      label: t("tpsAvg"),
      value: data.tps_avg.toFixed(1),
    },
    {
      key: "successRate",
      label: t("successRate"),
      value: `${successPct.toFixed(1)}%`,
      ratio: errorPct,
      threshold: { warn: 5, critical: 10 },
    },
  ];

  return <KpiGrid items={items} />;
}
