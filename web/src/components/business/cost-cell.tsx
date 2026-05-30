"use client";

import { BreakdownPopover, type BreakdownRow } from "@/components/business/breakdown-popover";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import {
  formatFactor,
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
  // 完整公式所需(新行才有)。rawInputCost == null/undefined → 走老行降级展示。
  rawInputCost?: number | null;
  rawOutputCost?: number | null;
  rawCacheReadCost?: number | null;
  rawCacheWriteCost?: number | null;
  cacheReadCost?: number;
  cacheWriteCost?: number;
  billingFactor?: number | null;
  modeLabel?: string;
}

export function CostDetailCell({
  amount,
  promptTokens,
  completionTokens,
  cacheReadTokens = 0,
  cacheWriteTokens = 0,
  inputCost,
  outputCost,
  rawInputCost,
  rawOutputCost,
  rawCacheReadCost,
  rawCacheWriteCost,
  cacheReadCost = 0,
  cacheWriteCost = 0,
  billingFactor,
  modeLabel,
}: CostDetailCellProps) {
  // 新行(有原价快照 + 因子)走完整四桶公式;老行降级到只列实付、cache 显示 —。
  const hasFormula = rawInputCost != null && billingFactor != null;
  const rows: BreakdownRow[] = [];

  if (!hasFormula) {
    rows.push({
      label: `Input · ${formatTokensCompact(promptTokens)} tokens`,
      value: formatMoneyCompact(inputCost),
    });
    rows.push({
      label: `Output · ${formatTokensCompact(completionTokens)} tokens`,
      value: formatMoneyCompact(outputCost),
    });
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
  } else {
    const showFactor = billingFactor !== 1;
    const cell = (raw: number, final: number) =>
      showFactor
        ? `${formatMoneyCompact(raw)} ×${formatFactor(billingFactor)} = ${formatMoneyCompact(final)}`
        : formatMoneyCompact(final);
    rows.push({
      label: `Input · ${formatTokensCompact(promptTokens)} tokens`,
      value: cell(rawInputCost as number, inputCost),
    });
    rows.push({
      label: `Output · ${formatTokensCompact(completionTokens)} tokens`,
      value: cell(rawOutputCost ?? 0, outputCost),
    });
    if (cacheReadTokens > 0) {
      rows.push({
        label: `Cache read · ${formatTokensCompact(cacheReadTokens)} tokens`,
        value: cell(rawCacheReadCost ?? 0, cacheReadCost),
        accent: "success",
      });
    }
    if (cacheWriteTokens > 0) {
      rows.push({
        label: `Cache write · ${formatTokensCompact(cacheWriteTokens)} tokens`,
        value: cell(rawCacheWriteCost ?? 0, cacheWriteCost),
        accent: "info",
      });
    }
    if (showFactor && modeLabel) {
      rows.push({ label: modeLabel, value: "", muted: true });
    }
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
