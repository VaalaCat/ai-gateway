"use client";

import { useTranslations } from "next-intl";
import { RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { InflightTable } from "@/components/business/inflight-table";
import { useAllAgentsInflight, useInterruptInflight } from "@/lib/api/agents";
import { formatErrorToast } from "@/lib/api/error-toast";

export function InflightMonitorCard() {
  const t = useTranslations("monitoring");
  const tc = useTranslations("common");
  const { data, isFetching, refetch } = useAllAgentsInflight();
  const interrupt = useInterruptInflight();

  const rows = data?.requests ?? [];
  const failed = data?.failed_agents ?? [];

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0">
        <CardTitle>{t("inflight.title")}</CardTitle>
        <Button variant="outline" size="sm" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={`mr-1 size-3 ${isFetching ? "animate-spin" : ""}`} />
          {t("inflight.refresh")}
        </Button>
      </CardHeader>
      <CardContent className="space-y-2">
        {failed.length > 0 && (
          <p className="text-xs text-muted-foreground">
            {t("inflight.failedNote", { count: failed.length })}
          </p>
        )}
        <InflightTable
          rows={rows}
          showAgent
          emptyText={t("inflight.empty")}
          onInterrupt={(row) =>
            interrupt.mutate(
              { agent_id: row.agent_id ?? 0, id: row.id },
              {
                onSuccess: () => { toast.success(t("inflight.interrupted")); },
                onError: (e) => toast.error(formatErrorToast(e, tc("error"))),
              },
            )
          }
        />
      </CardContent>
    </Card>
  );
}
