"use client";

import { BreakdownPopover, type BreakdownRow } from "@/components/business/breakdown-popover";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import {
  formatMoneyCompact,
  formatMoneyExact,
  formatPrice,
  formatTokensCompact,
} from "@/lib/utils/format";

interface CostCellProps {
  amount: number;
  /** @deprecated 保留参数签名兼容, 实际不再使用; 显示走 formatMoneyCompact + hover formatMoneyExact */
  decimals?: number;
}

export function CostCell({ amount }: CostCellProps) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span>{formatMoneyCompact(amount)}</span>
      </TooltipTrigger>
      <TooltipContent>{formatMoneyExact(amount)}</TooltipContent>
    </Tooltip>
  );
}

interface CostDetailCellProps {
  amount: number;
  promptTokens: number;
  completionTokens: number;
  cacheReadTokens?: number;
  cacheWriteTokens?: number;
  inputCost: number;
  outputCost: number;
}

export function CostDetailCell({
  amount,
  promptTokens,
  completionTokens,
  cacheReadTokens = 0,
  cacheWriteTokens = 0,
  inputCost,
  outputCost,
}: CostDetailCellProps) {
  const rows: BreakdownRow[] = [
    {
      label: `Input · ${formatTokensCompact(promptTokens)} tokens`,
      value: formatMoneyCompact(inputCost),
    },
    {
      label: `Output · ${formatTokensCompact(completionTokens)} tokens`,
      value: formatMoneyCompact(outputCost),
    },
  ];
  if (cacheReadTokens > 0) {
    rows.push({
      label: `Cache read · ${formatTokensCompact(cacheReadTokens)} tokens`,
      value: "—",
      accent: "success",
    });
  }
  if (cacheWriteTokens > 0) {
    rows.push({
      label: `Cache write · ${formatTokensCompact(cacheWriteTokens)} tokens`,
      value: "—",
      accent: "info",
    });
  }
  return (
    <BreakdownPopover
      trigger={formatMoneyCompact(amount)}
      rows={rows}
      total={{ label: "Total", value: formatMoneyExact(amount) }}
    />
  );
}

interface PriceCellProps {
  price: number;
}

export function PriceCell({ price }: PriceCellProps) {
  return <span>{formatPrice(price)}</span>;
}
