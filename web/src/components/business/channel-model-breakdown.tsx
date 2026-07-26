"use client";

import { useTranslations } from "next-intl";

import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Skeleton } from "@/components/ui/skeleton";
import { formatMoneyCompact, formatTokensCompact } from "@/lib/utils/format";
import { useChannelModelBreakdown } from "@/lib/api/billing";

interface ChannelModelBreakdownProps {
  channelId: number;
  start: number;
  end: number;
}

/**
 * Billing channel tab 行展开内容：单渠道按 model_name 分组的用量/计费细分。
 * total_cost(billed 折后实付) 与 raw_cost(raw 折前原价) 并列展示，差额即渠道折扣/免费让利。
 */
export function ChannelModelBreakdown({ channelId, start, end }: ChannelModelBreakdownProps) {
  const t = useTranslations("billing");
  const { data, isLoading } = useChannelModelBreakdown(channelId, start, end);
  const rows = data?.rows ?? [];

  if (isLoading) {
    return (
      <div className="space-y-2">
        {Array.from({ length: 3 }).map((_, i) => (
          <Skeleton key={`cmb-skeleton-${i}`} className="h-8 w-full" />
        ))}
      </div>
    );
  }

  if (rows.length === 0) {
    return (
      <div className="py-4 text-center text-sm text-muted-foreground">
        {t("channelModelBreakdown.empty")}
      </div>
    );
  }

  return (
    <div className="rounded-md border bg-background">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("channelModelBreakdown.model")}</TableHead>
            <TableHead>{t("requestCount")}</TableHead>
            <TableHead>{t("promptTokens")}</TableHead>
            <TableHead>{t("completionTokens")}</TableHead>
            <TableHead>{t("cacheReadTokens")}</TableHead>
            <TableHead>{t("cacheWriteTokens")}</TableHead>
            <TableHead>{t("channelModelBreakdown.billedCost")}</TableHead>
            <TableHead>{t("channelModelBreakdown.rawCost")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => (
            <TableRow key={row.model_name}>
              <TableCell className="font-medium">{row.model_name}</TableCell>
              <TableCell>{formatTokensCompact(row.requests)}</TableCell>
              <TableCell>{formatTokensCompact(row.prompt_tokens)}</TableCell>
              <TableCell>{formatTokensCompact(row.completion_tokens)}</TableCell>
              <TableCell>{formatTokensCompact(row.cache_read_tokens)}</TableCell>
              <TableCell>{formatTokensCompact(row.cache_write_tokens)}</TableCell>
              <TableCell>{formatMoneyCompact(row.total_cost)}</TableCell>
              <TableCell>{formatMoneyCompact(row.raw_cost)}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
