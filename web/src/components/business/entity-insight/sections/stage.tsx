"use client";

import { useTranslations } from "next-intl";

import { MetricTile } from "@/components/business/metric-tile";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import type { StageLatency } from "@/lib/api/insights";
import { formatDuration } from "@/lib/utils/format";

export function StageSection({ data }: { data: StageLatency }) {
  const t = useTranslations("insights.section");
  const tc = useTranslations("common");

  if (data.stages.length === 0) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>{t("stageLatency")}</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-muted-foreground">{tc("noData")}</p>
        </CardContent>
      </Card>
    );
  }

  const maxP95 = Math.max(...data.stages.map((s) => s.p95_ms), 1);

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("stageLatency")}</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-6">
          {data.stages.map((s) => (
            <MetricTile
              key={s.name}
              label={s.name}
              value={formatDuration(s.p95_ms)}
              viz={{ kind: "progress", percent: (s.p95_ms / maxP95) * 100 }}
            />
          ))}
        </div>
      </CardContent>
    </Card>
  );
}
