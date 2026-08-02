"use client";

import { useTranslations } from "next-intl";

import { MoneyCell } from "@/components/business/money-cell";
import { TokensCell } from "@/components/business/tokens-cell";
import { MARKETPLACE_PRICE_KEYS } from "@/components/model-marketplace/marketplace-price-grid";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { TooltipProvider } from "@/components/ui/tooltip";
import { chartColorForSeries } from "@/lib/chart-colors";
import type {
  MarketplaceModelOfferDetail,
  MarketplaceUsageAvailability,
  MarketplaceUsageReference,
  MarketplaceUsageScope,
} from "@/lib/api/model-marketplace";
import { cn } from "@/lib/utils";
import { nonNegativeFiniteNumber } from "@/lib/model-marketplace-values";

const TOKEN_UNIT_KEYS = [...MARKETPLACE_PRICE_KEYS, "total"] as const;

function TokenAmount({ value }: { value: unknown }) {
  const tokens = nonNegativeFiniteNumber(value);
  return tokens === null ? "—" : <TokensCell tokens={tokens} />;
}

function TokenUnits({ reference }: { reference: MarketplaceUsageReference }) {
  const t = useTranslations("modelMarketplace.detail");
  return (
    <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm tabular-nums sm:grid-cols-5">
      {TOKEN_UNIT_KEYS.map((key) => {
        const cached = key.startsWith("cache_");
        return (
          <div
            key={key}
            data-testid="usage-token-bucket"
            data-token-key={key}
            className={cn(
              "flex min-w-0 flex-col gap-0.5",
              cached && "text-muted-foreground",
            )}
          >
            <dt className="text-xs font-medium text-muted-foreground">
              {key === "total" ? t("totalTokenUnits") : t(`tokenUnit.${key}`)}
            </dt>
            <dd className={cn(key === "total" && "font-semibold")}>
              <TokenAmount value={reference.token_units[key]} />
            </dd>
          </div>
        );
      })}
    </dl>
  );
}

function CostAmount({ value }: { value: number | null }) {
  const t = useTranslations("modelMarketplace.detail");
  if (value === null) return t("unknownAmount");

  const cost = nonNegativeFiniteNumber(value);
  return cost === null ? "—" : <MoneyCell quota={cost} />;
}

function CostReference({ reference }: { reference: MarketplaceUsageReference }) {
  const t = useTranslations("modelMarketplace.detail");
  return (
    <dl className="grid grid-cols-1 gap-2 border-t pt-3 text-sm tabular-nums sm:grid-cols-3">
      <div className="flex items-center justify-between gap-3 sm:flex-col sm:items-start sm:gap-0.5">
        <dt className="text-xs font-medium text-muted-foreground">{t("usageUpstreamCost")}</dt>
        <dd data-cost="upstream"><CostAmount value={reference.reference_cost} /></dd>
      </div>
      <div className="flex items-center justify-between gap-3 sm:flex-col sm:items-start sm:gap-0.5">
        <dt className="text-xs font-medium text-muted-foreground">{t("usageGatewayCost")}</dt>
        <dd data-cost="gateway"><CostAmount value={reference.gateway_charge_cost} /></dd>
      </div>
      <div className="flex items-center justify-between gap-3 sm:flex-col sm:items-start sm:gap-0.5">
        <dt className="text-xs font-medium text-muted-foreground">{t("usageEstimatedTotal")}</dt>
        <dd data-cost="estimated"><CostAmount value={reference.estimated_total_cost} /></dd>
      </div>
    </dl>
  );
}

interface ScopedUsageReference {
  offer: MarketplaceModelOfferDetail;
  reference: MarketplaceUsageReference;
}

function UsageScopeCard({ offer, reference }: ScopedUsageReference) {
  const t = useTranslations("modelMarketplace.detail");
  return (
    <TooltipProvider delayDuration={100}>
      <Card
        role="article"
        aria-label={t(`usageScope.${reference.scope}`)}
        className="gap-4 py-4"
        style={{ borderTopColor: chartColorForSeries(offer.offer_ref), borderTopWidth: 2 }}
      >
        <CardHeader className="px-4">
          <CardTitle className="flex min-w-0 items-center gap-2 text-sm">
            <span
              aria-hidden="true"
              className="size-2.5 shrink-0 rounded-[2px]"
              style={{ backgroundColor: chartColorForSeries(offer.offer_ref) }}
            />
            <span className="truncate" title={offer.display_name}>{offer.display_name}</span>
          </CardTitle>
          <CardDescription className="flex flex-wrap items-center gap-1.5">
            <Badge variant="outline">{t(`usageScope.${reference.scope}`)}</Badge>
            <Badge variant="secondary">{t(`window.${reference.window}`)}</Badge>
            {reference.includes_shared_usage ? (
              <Badge variant="outline">{t("includesSharedUsage")}</Badge>
            ) : null}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3 px-4">
          <TokenUnits reference={reference} />
          <CostReference reference={reference} />
          <p className="text-xs text-muted-foreground">
            {reference.accuracy === "reference" ? t("usageReferenceNotice") : t("usageExactNotice")}
          </p>
        </CardContent>
      </Card>
    </TooltipProvider>
  );
}

interface UsageReferenceProps {
  offers: MarketplaceModelOfferDetail[];
  usageStatus: MarketplaceUsageAvailability;
}

export function UsageReference({ offers, usageStatus }: UsageReferenceProps) {
  const t = useTranslations("modelMarketplace.detail");
  if (usageStatus === "unavailable") {
    return (
      <Alert variant="destructive">
        <AlertTitle>{t("usageUnavailableTitle")}</AlertTitle>
        <AlertDescription>{t("usageUnavailableDescription")}</AlertDescription>
      </Alert>
    );
  }
  if (usageStatus === "not_applicable") return null;

  const scoped = offers.flatMap((offer) => offer.usage_references.map((reference) => ({
    offer,
    reference,
  })));
  if (scoped.length === 0) {
    return (
      <Alert>
        <AlertTitle>{t("usageEmptyTitle")}</AlertTitle>
        <AlertDescription>{t("usageEmptyDescription")}</AlertDescription>
      </Alert>
    );
  }

  return (
    <section className="flex flex-col gap-3" aria-labelledby="marketplace-usage-title">
      <div className="flex flex-col gap-1">
        <h2 id="marketplace-usage-title" className="text-lg font-semibold tracking-tight">
          {t("usageTitle")}
        </h2>
        <p className="text-sm text-muted-foreground">{t("usageDescription")}</p>
      </div>
      <div className="grid grid-cols-1 gap-3 xl:grid-cols-2">
        {scoped.map(({ offer, reference }, index) => (
          <UsageScopeCard
            key={`${offer.offer_ref}:${reference.scope}:${index}`}
            offer={offer}
            reference={reference}
          />
        ))}
      </div>
    </section>
  );
}

export type { MarketplaceUsageScope };
