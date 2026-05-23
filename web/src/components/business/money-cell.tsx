"use client";

import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { formatMoneyCompact, formatMoneyExact } from "@/lib/utils/format";

interface MoneyCellProps {
  /** quota 单位 (1 USD = UNIT_QUOTA_SCALE = 100000 quota) */
  quota: number;
  className?: string;
}

/**
 * DataTable / KPI 卡内金额展示。紧凑 K/M/B 显示，hover 看完整 6 位精度。
 * 假定外层已有 TooltipProvider (DataTable 根容器已包)。
 */
export function MoneyCell({ quota, className }: MoneyCellProps) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className={className}>{formatMoneyCompact(quota)}</span>
      </TooltipTrigger>
      <TooltipContent>{formatMoneyExact(quota)}</TooltipContent>
    </Tooltip>
  );
}
