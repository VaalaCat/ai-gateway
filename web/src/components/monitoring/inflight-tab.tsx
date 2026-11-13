"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { InflightRequests } from "@/components/observability/inflight-requests";
import { useAllInflight } from "@/lib/api/observability";

export function InflightTab() {
  const t = useTranslations("monitoring");
  const [paused, setPaused] = useState(false);
  const query = useAllInflight({ paused });
  // 1. 全局列表和节点详情使用相同的请求列表交互。
  return <InflightRequests title={t("tab.inflight")} data={query.data} dataUpdatedAt={query.dataUpdatedAt}
    error={query.error} isFetching={query.isFetching} paused={paused} onPausedChange={setPaused}
    onRefresh={query.refetch} showAgent />;
}
