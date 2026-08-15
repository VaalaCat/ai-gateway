"use client";

import { useTranslations } from "next-intl";

import { Badge } from "@/components/ui/badge";
import type { TraceRetentionStatus } from "@/lib/types";

interface TraceRetentionNoticeProps {
  status?: TraceRetentionStatus;
}

export function TraceRetentionNotice({ status }: TraceRetentionNoticeProps) {
  const t = useTranslations("logs.traceRetention");
  if (!status) return null;

  const degraded = status !== "full";
  return (
    <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/30 px-3 py-2 text-sm">
      <Badge variant={degraded ? "outline" : "secondary"}>{t(`${status}.label`)}</Badge>
      <span className="text-muted-foreground">{t(`${status}.description`)}</span>
    </div>
  );
}
