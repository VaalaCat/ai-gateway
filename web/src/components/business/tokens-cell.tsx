"use client";

import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { formatTokensCompact, formatTokensExact } from "@/lib/utils/format";

interface TokensCellProps {
  tokens: number;
  className?: string;
}

/**
 * DataTable / KPI 卡内 token 数展示。紧凑 K/M/B 显示，hover 看完整千分位整数。
 * 假定外层已有 TooltipProvider (DataTable 根容器已包)。
 */
export function TokensCell({ tokens, className }: TokensCellProps) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className={className}>{formatTokensCompact(tokens)}</span>
      </TooltipTrigger>
      <TooltipContent>{formatTokensExact(tokens)}</TooltipContent>
    </Tooltip>
  );
}
