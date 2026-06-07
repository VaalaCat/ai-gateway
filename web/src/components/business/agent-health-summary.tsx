"use client";

import { useTranslations } from "next-intl";
import { Switch } from "@/components/ui/switch";
import { StatusDot } from "@/components/business/health-status-dot";

/** 集群健康汇总条：在线/告警/离线计数 + 「仅异常」开关。计数来自全量健康，不受分页影响。 */
export function AgentHealthSummary({
  counts,
  anomalyOnly,
  onAnomalyOnlyChange,
}: {
  counts: { total: number; warn: number; down: number };
  anomalyOnly: boolean;
  onAnomalyOnlyChange: (v: boolean) => void;
}) {
  const t = useTranslations("observability.health");
  return (
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div className="flex flex-wrap items-center gap-3 text-sm">
        <span className="text-muted-foreground">{t("summary.online", { count: counts.total })}</span>
        {counts.down > 0 && (
          <span className="inline-flex items-center gap-1 text-red-600 dark:text-red-400">
            <StatusDot status="down" red />
            {t("summary.down", { count: counts.down })}
          </span>
        )}
        {counts.warn > 0 && (
          <span className="inline-flex items-center gap-1 text-amber-600 dark:text-amber-400">
            <StatusDot status="warn" red={false} />
            {t("summary.warn", { count: counts.warn })}
          </span>
        )}
      </div>
      <label className="flex cursor-pointer items-center gap-2 text-sm">
        <Switch checked={anomalyOnly} onCheckedChange={onAnomalyOnlyChange} />
        {t("anomalyOnly")}
      </label>
    </div>
  );
}
