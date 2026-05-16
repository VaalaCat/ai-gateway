"use client";

import { formatDate, formatRelativeTime } from "@/lib/utils/format";

interface DateCellProps {
  timestamp: number;
  relative?: boolean;
}

export function DateCell({ timestamp, relative = false }: DateCellProps) {
  if (!timestamp) return <span>-</span>;
  return <span>{relative ? formatRelativeTime(timestamp) : formatDate(timestamp)}</span>;
}
