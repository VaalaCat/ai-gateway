"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ChannelModelBreakdown } from "@/components/business/channel-model-breakdown";
import { useChannelModelBreakdown } from "@/lib/api/billing";
import { formatMoneyCompact, formatTokensCompact } from "@/lib/utils/format";

export interface UsageSectionProps {
  channelId?: number;
}

const SECONDS_PER_DAY = 86400;

type WindowDays = 7 | 30;

/**
 * 只读展示 stage:渠道近 N 天用量/成本汇总 + 按模型细分(复用 ChannelModelBreakdown)。
 * create 态(channelId undefined)渠道还不存在,没有用量可查,显示占位提示。
 */
export function UsageSection({ channelId }: UsageSectionProps) {
  const t = useTranslations("channels");
  const tb = useTranslations("billing");
  const [windowDays, setWindowDays] = useState<WindowDays>(30);
  const [now] = useState(() => Math.floor(Date.now() / 1000));

  // start/end 为 unix 秒,与 useChannelModelBreakdown / ChannelModelBreakdown 的约定一致
  // (非 date string,别套 localDateRangeToUTCRange)。
  const start = now - windowDays * SECONDS_PER_DAY;
  const end = now;

  const { data } = useChannelModelBreakdown(channelId, start, end);
  const rows = data?.rows ?? [];

  if (channelId === undefined) {
    return (
      <div className="py-10 text-center text-sm text-muted-foreground">
        {t("usageSavedPlaceholder")}
      </div>
    );
  }

  const totals = rows.reduce(
    (acc, row) => ({
      requests: acc.requests + row.requests,
      tokens:
        acc.tokens +
        row.prompt_tokens +
        row.completion_tokens +
        row.cache_read_tokens +
        row.cache_write_tokens,
      billedCost: acc.billedCost + row.total_cost,
      rawCost: acc.rawCost + row.raw_cost,
    }),
    { requests: 0, tokens: 0, billedCost: 0, rawCost: 0 },
  );

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <Tabs
          value={String(windowDays)}
          onValueChange={(v) => setWindowDays(v === "7" ? 7 : 30)}
        >
          <TabsList>
            <TabsTrigger value="7">{t("usageWindow7d")}</TabsTrigger>
            <TabsTrigger value="30">{t("usageWindow30d")}</TabsTrigger>
          </TabsList>
        </Tabs>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>{t("usageSummary")}</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <div className="space-y-1">
              <p className="text-xs text-muted-foreground">{tb("requestCount")}</p>
              <p className="text-lg font-semibold">{formatTokensCompact(totals.requests)}</p>
            </div>
            <div className="space-y-1">
              <p className="text-xs text-muted-foreground">{t("usageTotalTokens")}</p>
              <p className="text-lg font-semibold">{formatTokensCompact(totals.tokens)}</p>
            </div>
            <div className="space-y-1">
              <p className="text-xs text-muted-foreground">
                {tb("channelModelBreakdown.billedCost")}
              </p>
              <p className="text-lg font-semibold">{formatMoneyCompact(totals.billedCost)}</p>
            </div>
            <div className="space-y-1">
              <p className="text-xs text-muted-foreground">
                {tb("channelModelBreakdown.rawCost")}
              </p>
              <p className="text-lg font-semibold">{formatMoneyCompact(totals.rawCost)}</p>
            </div>
          </div>
        </CardContent>
      </Card>
      <ChannelModelBreakdown channelId={channelId} start={start} end={end} />
    </div>
  );
}
