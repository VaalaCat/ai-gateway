"use client";

import { useTranslations } from "next-intl";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { UsageLog } from "@/lib/types";

interface RateLimitSectionProps {
  decision: string;
  waitMs?: number;
  reason?: string;
  hits?: NonNullable<UsageLog["rate_limit_hits"]>;
}

/** 决策值 → 徽标外观 + i18n key 后缀。rejected 红 / queued 黄 / allow 灰。 */
function decisionBadge(decision: string): {
  className?: string;
  variant?: "secondary" | "destructive";
  labelKey: string;
} {
  switch (decision) {
    case "rejected":
      return { variant: "destructive", labelKey: "rateLimit.decisionRejected" };
    case "queued":
      return {
        className:
          "bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200",
        labelKey: "rateLimit.decisionQueued",
      };
    default:
      return { variant: "secondary", labelKey: "rateLimit.decisionAllow" };
  }
}

/** logs 详情里的"限流"段：决策徽标 + 等待 + 原因 + 命中 limiter 列表。 */
export function RateLimitSection({
  decision,
  waitMs,
  reason,
  hits,
}: RateLimitSectionProps) {
  const t = useTranslations("logs");
  const badge = decisionBadge(decision);

  return (
    <div className="rounded-md border p-3 space-y-3">
      <div className="flex flex-wrap items-center gap-2 text-sm font-medium">
        <span>{t("rateLimit.title")}</span>
        <Badge
          variant={badge.variant}
          className={`text-xs font-normal ${badge.className ?? ""}`}
        >
          {t(badge.labelKey)}
        </Badge>
        {(waitMs ?? 0) > 0 && (
          <span className="text-muted-foreground font-normal text-xs">
            {t("rateLimit.waitMs")}: {waitMs}ms
          </span>
        )}
      </div>

      {reason && (
        <div className="text-sm">
          <span className="text-muted-foreground">{t("rateLimit.reason")}: </span>
          <span className="font-medium">{reason}</span>
        </div>
      )}

      {(hits?.length ?? 0) > 0 && (
        <div className="space-y-1">
          <div className="text-xs text-muted-foreground">{t("rateLimit.hits")}</div>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("rateLimit.limiter")}</TableHead>
                <TableHead>{t("rateLimit.dimension")}</TableHead>
                <TableHead>{t("rateLimit.bucket")}</TableHead>
                <TableHead>{t("rateLimit.decision")}</TableHead>
                <TableHead className="text-right">{t("rateLimit.waitMs")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {hits!.map((hit, idx) => {
                const hitBadge = decisionBadge(hit.decision);
                return (
                  <TableRow key={`${hit.limiter_id}-${idx}`}>
                    <TableCell className="font-medium">{hit.name}</TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {hit.dimension}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {hit.bucket || "-"}
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant={hitBadge.variant}
                        className={`text-xs font-normal ${hitBadge.className ?? ""}`}
                      >
                        {t(hitBadge.labelKey)}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-right text-xs">
                      {hit.wait_ms > 0 ? `${hit.wait_ms}ms` : "-"}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}
