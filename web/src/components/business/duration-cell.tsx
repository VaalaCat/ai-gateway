"use client";

import { formatDuration } from "@/lib/utils/format";

interface DurationCellProps {
  ms: number;
}

export function DurationCell({ ms }: DurationCellProps) {
  return <span>{formatDuration(ms)}</span>;
}
