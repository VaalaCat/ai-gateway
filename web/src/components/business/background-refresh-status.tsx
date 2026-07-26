"use client";

import { LoaderCircle } from "lucide-react";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

export interface BackgroundRefreshStatusProps {
  refreshing: boolean;
  label: string;
  testId?: string;
  className?: string;
}

export function BackgroundRefreshStatus({
  refreshing,
  label,
  testId,
  className,
}: BackgroundRefreshStatusProps) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          className={cn("inline-flex size-8 shrink-0 items-center justify-center", className)}
          data-testid={testId}
          role={refreshing ? "status" : undefined}
          aria-label={refreshing ? label : undefined}
        >
          <LoaderCircle
            aria-hidden="true"
            className={cn("size-4", refreshing ? "visible animate-spin" : "invisible")}
          />
        </span>
      </TooltipTrigger>
      {refreshing && <TooltipContent>{label}</TooltipContent>}
    </Tooltip>
  );
}
