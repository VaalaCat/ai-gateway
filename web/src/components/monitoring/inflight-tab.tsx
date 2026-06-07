"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { LiveTabHeader } from "@/components/monitoring/live-tab-header";
import { InflightConveyor } from "@/components/observability/inflight-conveyor";
import { InflightBlockDetail } from "@/components/observability/inflight-block-detail";
import { InflightTable } from "@/components/business/inflight-table";
import { useAllInflight } from "@/lib/api/observability";
import { useInterruptInflight } from "@/lib/api/agents";
import { formatErrorToast } from "@/lib/api/error-toast";
import type { GlobalInflightRow } from "@/lib/types";

export function InflightTab() {
  const t = useTranslations("observability");
  const tm = useTranslations("monitoring");
  const tc = useTranslations("common");
  const { data } = useAllInflight();
  const interrupt = useInterruptInflight();
  const [selected, setSelected] = useState<GlobalInflightRow | null>(null);

  const rows = data?.requests ?? [];
  const failed = data?.failed_agents ?? [];
  const queuedCount = rows.filter((r) => r.queued_ms > 0).length;

  const subtitle = `${t("totalCount", { n: rows.length })}${
    queuedCount > 0 ? ` · ${t("queuedCount", { n: queuedCount })}` : ""
  }${failed.length > 0 ? ` · ${t("failedAgents", { n: failed.length })}` : ""}`;

  return (
    <div className="space-y-4">
      <LiveTabHeader title={tm("tab.inflight")} subtitle={subtitle} />

      <InflightConveyor rows={rows} onSelect={setSelected} />

      <InflightBlockDetail row={selected} onClose={() => setSelected(null)} />

      <div className="rounded-md border">
        <InflightTable
          rows={rows}
          showAgent
          onSelectRow={(row) => setSelected(row as GlobalInflightRow)}
          emptyText={t("empty")}
          onInterrupt={(row) =>
            interrupt.mutate(
              { agent_id: row.agent_id ?? 0, id: row.id },
              {
                onSuccess: () => {
                  toast.success(t("interrupted"));
                },
                onError: (e) => toast.error(formatErrorToast(e, tc("error"))),
              },
            )
          }
        />
      </div>
    </div>
  );
}
