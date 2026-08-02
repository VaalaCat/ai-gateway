"use client";

import { useTranslations } from "next-intl";

import type { MarketplacePrices } from "@/lib/api/model-marketplace";
import { cn } from "@/lib/utils";
import { formatPriceValue } from "@/lib/utils/format";

export const MARKETPLACE_PRICE_KEYS = [
  "input",
  "cache_read",
  "output",
  "cache_write",
] as const;

type MarketplacePriceKey = (typeof MARKETPLACE_PRICE_KEYS)[number];
type MarketplacePriceRanges = Record<MarketplacePriceKey, readonly number[]>;

type MarketplacePriceGridProps =
  | { prices: MarketplacePrices; mode: "values" }
  | { prices: MarketplacePriceRanges; mode: "range" };

function formatPriceRange(values: readonly number[]): string {
  const validValues = values.filter((value) => Number.isFinite(value) && value >= 0);
  if (validValues.length === 0) return "—";

  const minimum = Math.min(...validValues);
  const maximum = Math.max(...validValues);
  if (minimum === maximum) return formatPriceValue(minimum);
  return `${formatPriceValue(minimum)} – ${formatPriceValue(maximum)}`;
}

export function MarketplacePriceGrid({ prices, mode }: MarketplacePriceGridProps) {
  const t = useTranslations("modelMarketplace");

  return (
    <dl className="grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-4">
      {MARKETPLACE_PRICE_KEYS.map((key) => (
        <div
          key={key}
          data-testid="marketplace-price-item"
          data-price-key={key}
          className={cn(
            "min-w-0 tabular-nums",
            key.startsWith("cache_") && "text-muted-foreground",
          )}
        >
          <dt className="text-label">{t(`priceBucket.${key}`)}</dt>
          <dd className="text-body font-medium">
            {mode === "values"
              ? formatPriceValue(prices[key])
              : formatPriceRange(prices[key])}
          </dd>
        </div>
      ))}
    </dl>
  );
}
