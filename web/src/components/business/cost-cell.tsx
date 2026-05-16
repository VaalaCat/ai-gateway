"use client";

import { formatCurrency, formatPrice } from "@/lib/utils/format";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";

interface CostCellProps {
  amount: number;
  decimals?: number;
}

export function CostCell({ amount, decimals = 6 }: CostCellProps) {
  return <span>{formatCurrency(amount, decimals)}</span>;
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
  const hasCache = cacheReadTokens > 0 || cacheWriteTokens > 0;

  return (
    <TooltipProvider delayDuration={200}>
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="cursor-help border-b border-dotted border-muted-foreground/50">
            {formatCurrency(amount)}
          </span>
        </TooltipTrigger>
        <TooltipContent side="top" className="text-xs space-y-1">
          <div>Input: {promptTokens.toLocaleString()} tokens → {formatCurrency(inputCost)}</div>
          <div>Output: {completionTokens.toLocaleString()} tokens → {formatCurrency(outputCost)}</div>
          {hasCache && (
            <>
              {cacheReadTokens > 0 && (
                <div className="text-green-500">Cache read: {cacheReadTokens.toLocaleString()} tokens</div>
              )}
              {cacheWriteTokens > 0 && (
                <div className="text-blue-500">Cache write: {cacheWriteTokens.toLocaleString()} tokens</div>
              )}
            </>
          )}
          <div className="font-medium border-t pt-1">Total: {formatCurrency(amount)}</div>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

interface PriceCellProps {
  price: number;
}

export function PriceCell({ price }: PriceCellProps) {
  return <span>{formatPrice(price)}</span>;
}
