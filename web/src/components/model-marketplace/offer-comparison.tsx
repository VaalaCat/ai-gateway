"use client";

import { useMemo, useState } from "react";
import { useTranslations } from "next-intl";

import { ChannelAvatar } from "@/components/model-marketplace/channel-avatar-group";
import {
  MARKETPLACE_STATUS_PRESENTATION,
  marketplaceOfferStatus,
} from "@/components/model-marketplace/marketplace-status";
import {
  MARKETPLACE_PRICE_KEYS,
  MarketplacePriceGrid,
} from "@/components/model-marketplace/marketplace-price-grid";
import { OfferStatusMatrix } from "@/components/model-marketplace/offer-status-matrix";
import { OfferTrendWorkspace } from "@/components/model-marketplace/offer-trend-workspace";
import { UsageReference } from "@/components/model-marketplace/usage-reference";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { chartColorForSeries } from "@/lib/chart-colors";
import type {
  MarketplaceEndpoint,
  MarketplaceModelOfferDetail,
  MarketplaceUsageAvailability,
  MarketplaceUsageWindow,
} from "@/lib/api/model-marketplace";
import { cn } from "@/lib/utils";
import {
  formatDuration,
  formatPercentValue,
  formatPriceValue,
  formatTpsValue,
} from "@/lib/utils/format";

const MAX_SELECTED_OFFERS = 5;
const MAX_DEFAULT_OFFERS = 3;
const INVOCATION_ENDPOINTS: MarketplaceEndpoint[] = [
  "chat_completions",
  "responses",
  "messages",
];

type OfferMetric = (offer: MarketplaceModelOfferDetail) => number | null | undefined;

function finiteMetric(value: number | null | undefined): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

function metricWinner(
  offers: readonly MarketplaceModelOfferDetail[],
  metric: OfferMetric,
  direction: "lowest" | "highest",
) {
  return offers
    .map((offer) => ({ offer, value: metric(offer) }))
    .filter((item): item is { offer: MarketplaceModelOfferDetail; value: number } =>
      finiteMetric(item.value),
    )
    .sort((left, right) => {
      const byMetric = direction === "lowest"
        ? left.value - right.value
        : right.value - left.value;
      return byMetric || left.offer.offer_ref.localeCompare(right.offer.offer_ref);
    })[0]?.offer;
}

export function defaultMarketplaceOfferRefs(
  offers: readonly MarketplaceModelOfferDetail[],
) {
  const availableOffers = offers.filter((offer) => offer.available);
  const criteria: Array<{ metric: OfferMetric; direction: "lowest" | "highest" }> = [
    { metric: (offer) => offer.pricing.estimated_total.input, direction: "lowest" },
    { metric: (offer) => offer.performance.ttft_avg_ms, direction: "lowest" },
    { metric: (offer) => offer.performance.success_rate, direction: "highest" },
    { metric: (offer) => offer.pricing.estimated_total.output, direction: "lowest" },
    { metric: (offer) => offer.performance.tps_avg, direction: "highest" },
  ];
  const result: string[] = [];
  for (const criterion of criteria) {
    const winner = metricWinner(availableOffers, criterion.metric, criterion.direction);
    if (winner && !result.includes(winner.offer_ref)) result.push(winner.offer_ref);
    if (result.length === MAX_DEFAULT_OFFERS) break;
  }
  return result;
}

function formatOptionalMetric(
  value: number | null | undefined,
  formatter: (metric: number) => string,
) {
  return finiteMetric(value) && value >= 0 ? formatter(value) : "—";
}

function ByokPriceBreakdown({
  offer,
  position,
}: {
  offer: MarketplaceModelOfferDetail;
  position: number;
}) {
  const t = useTranslations("modelMarketplace.detail");
  if (offer.kind !== "private") return null;
  const prices = [
    ["byokUpstreamPriceLabel", offer.pricing.reference_price],
    ["gatewayChargeLabel", offer.pricing.gateway_charge],
    ["estimatedTotalLabel", offer.pricing.estimated_total],
  ] as const;

  return (
    <Collapsible>
      <CollapsibleTrigger asChild>
        <Button
          variant="ghost"
          size="xs"
          aria-label={t("byokPriceBreakdownTriggerLabel", {
            offer: offer.display_name,
            position,
          })}
        >
          {t("byokPriceBreakdownTrigger")}
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent className="flex flex-col gap-3 pt-2">
        {prices.map(([label, values]) => (
          <div key={label} data-testid="byok-price-layer" className="flex flex-col gap-1">
            <span className="flex flex-wrap items-baseline gap-x-1 text-label text-muted-foreground">
              <span>{t(label)}</span>
              <span className="font-normal">{t("priceUnitLabel")}</span>
            </span>
            <MarketplacePriceGrid prices={values} mode="values" />
          </div>
        ))}
      </CollapsibleContent>
    </Collapsible>
  );
}

function EndpointBadges({ offer, discovery }: {
  offer: MarketplaceModelOfferDetail;
  discovery: boolean;
}) {
  const t = useTranslations("modelMarketplace");
  const endpoints = discovery
    ? (["models"] as MarketplaceEndpoint[]).filter((endpoint) =>
        offer.supported_endpoints.includes(endpoint),
      )
    : INVOCATION_ENDPOINTS.filter((endpoint) =>
        offer.supported_endpoints.includes(endpoint),
      );
  if (endpoints.length === 0) return <span className="text-muted-foreground">—</span>;
  return (
    <div className="flex flex-wrap gap-1">
      {endpoints.map((endpoint) => (
        <Badge key={endpoint} variant="outline">{t(`endpoint.${endpoint}`)}</Badge>
      ))}
    </div>
  );
}

function OfferIdentity({ offer }: { offer: MarketplaceModelOfferDetail }) {
  const t = useTranslations("modelMarketplace");
  const td = useTranslations("modelMarketplace.detail");
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div className="flex min-w-0 items-center gap-2">
        <ChannelAvatar offer={offer} />
        <span className="truncate font-semibold" title={offer.display_name}>
          {offer.display_name}
        </span>
      </div>
      <div className="flex flex-wrap gap-1">
        <Badge variant={offer.kind === "platform" ? "default" : "secondary"}>
          {t(`kind.${offer.kind === "platform" ? "platform" : "byok"}`)}
        </Badge>
        <Badge variant="outline">{t(`ownership.${offer.ownership}`)}</Badge>
        {!offer.available ? <Badge variant="destructive">{td("offerUnavailable")}</Badge> : null}
      </div>
    </div>
  );
}

export interface AdminOfferDiagnosticsView {
  publicDisplayName: string;
  internalName: string;
  baseUrl: string;
  channelId: number;
  privateChannelId: number;
  ownerId: number;
  endpointPaths: Array<{ endpoint: MarketplaceEndpoint; path: string }>;
  disabledReasons: string[];
  requestCount: number;
  successCount: number;
  failureCount: number;
  streamRequestCount: number;
  ttftSampleCount: number;
  tpsSampleCount: number;
  durationSampleCount: number;
}

const KNOWN_DISABLED_REASONS = new Set(["disabled", "endpoints_not_configured"]);

function DiagnosticDefinition({
  label,
  value,
  valueClassName,
}: {
  label: React.ReactNode;
  value: React.ReactNode;
  valueClassName?: string;
}) {
  return (
    <div className="grid min-w-0 grid-cols-[minmax(0,auto)_minmax(0,1fr)] gap-x-1">
      <dt className="min-w-0 text-muted-foreground">{label}</dt>
      <dd className={cn("min-w-0", valueClassName)}>{value}</dd>
    </div>
  );
}

function AdminDiagnostics({ diagnostics }: { diagnostics: AdminOfferDiagnosticsView }) {
  const t = useTranslations("modelMarketplace.detail");
  const identity = [
    ["adminPublicName", diagnostics.publicDisplayName, "break-words"],
    ["adminInternalName", diagnostics.internalName, "[overflow-wrap:anywhere]"],
    ["adminBaseUrl", diagnostics.baseUrl, "break-all"],
    ["adminChannelId", diagnostics.channelId, "tabular-nums"],
    ["adminPrivateChannelId", diagnostics.privateChannelId, "tabular-nums"],
    ["adminOwnerId", diagnostics.ownerId, "tabular-nums"],
  ] as const;
  const counts = [
    ["adminRequestCount", diagnostics.requestCount],
    ["adminSuccessCount", diagnostics.successCount],
    ["adminFailureCount", diagnostics.failureCount],
    ["adminStreamRequestCount", diagnostics.streamRequestCount],
    ["adminTtftSampleCount", diagnostics.ttftSampleCount],
    ["adminTpsSampleCount", diagnostics.tpsSampleCount],
    ["adminDurationSampleCount", diagnostics.durationSampleCount],
  ] as const;
  return (
    <div className="flex flex-col gap-3 text-xs">
      <dl className="grid grid-cols-1 gap-x-3 gap-y-1 sm:grid-cols-2">
        {identity.map(([label, value, valueClassName]) => (
          <DiagnosticDefinition
            key={label}
            label={t(label)}
            value={value === "" ? "—" : value}
            valueClassName={valueClassName}
          />
        ))}
      </dl>
      {diagnostics.disabledReasons.length > 0 ? (
        <div className="flex flex-wrap gap-1">
          {diagnostics.disabledReasons.map((reason, index) => {
            const safeReason = KNOWN_DISABLED_REASONS.has(reason) ? reason : "unknown";
            return (
              <Badge key={`${safeReason}:${index}`} variant="destructive">
                {t(`disabledReason.${safeReason}`)}
              </Badge>
            );
          })}
        </div>
      ) : null}
      {diagnostics.endpointPaths.length > 0 ? (
        <dl className="grid min-w-0 grid-cols-1 gap-y-1">
          {diagnostics.endpointPaths.map((endpoint) => (
            <DiagnosticDefinition
              key={`${endpoint.endpoint}:${endpoint.path}`}
              label={endpoint.endpoint}
              value={endpoint.path}
              valueClassName="break-all"
            />
          ))}
        </dl>
      ) : null}
      <dl className="grid grid-cols-2 gap-x-3 gap-y-1 tabular-nums lg:grid-cols-4">
        {counts.map(([label, value]) => (
          <DiagnosticDefinition
            key={label}
            label={t(label)}
            value={Number.isFinite(value) ? value : "—"}
            valueClassName="tabular-nums"
          />
        ))}
      </dl>
    </div>
  );
}

interface ComparisonRow {
  key: string;
  label: string;
  priceKey?: (typeof MARKETPLACE_PRICE_KEYS)[number];
  muted?: boolean;
  render: (offer: MarketplaceModelOfferDetail) => React.ReactNode;
}

function PriceBucketLabel({ row }: { row: ComparisonRow }) {
  const t = useTranslations("modelMarketplace.detail");
  if (!row.priceKey) return row.label;
  return (
    <span className="flex flex-col">
      <span>{row.label}</span>
      <span className="text-2xs font-normal text-muted-foreground">
        {t("estimatedTotalLabel")} · {t("priceUnitLabel")}
      </span>
    </span>
  );
}

function useComparisonRows(offers: MarketplaceModelOfferDetail[]): ComparisonRow[] {
  const t = useTranslations("modelMarketplace");
  const td = useTranslations("modelMarketplace.detail");
  const supportsModelDiscovery = offers.some((offer) =>
    offer.supported_endpoints.includes("models"),
  );
  return useMemo(() => {
    const rows: ComparisonRow[] = [
      {
        key: "status",
        label: t("statusLabel"),
        render: (offer) => {
          const status = marketplaceOfferStatus(offer);
          const label = status === "stale" || status === "unavailable"
            ? td(`statusState.${status}`)
            : t(`status.${status}`);
          return (
            <Badge variant="outline" className={MARKETPLACE_STATUS_PRESENTATION[status].badge}>
              {label}
            </Badge>
          );
        },
      },
      ...MARKETPLACE_PRICE_KEYS.map((priceKey): ComparisonRow => ({
        key: `price-${priceKey}`,
        label: t(`priceBucket.${priceKey}`),
        priceKey,
        muted: priceKey.startsWith("cache_"),
        render: (offer) => formatPriceValue(offer.pricing.estimated_total[priceKey]),
      })),
      {
        key: "ttft",
        label: td("ttftLabel"),
        render: (offer) => formatOptionalMetric(offer.performance.ttft_avg_ms, formatDuration),
      },
      {
        key: "tps",
        label: td("tpsLabel"),
        render: (offer) => formatOptionalMetric(offer.performance.tps_avg, formatTpsValue),
      },
      {
        key: "sla",
        label: td("slaLabel"),
        render: (offer) => formatOptionalMetric(offer.performance.success_rate, formatPercentValue),
      },
      {
        key: "invocation",
        label: t("invocationEndpointsLabel"),
        render: (offer) => <EndpointBadges offer={offer} discovery={false} />,
      },
    ];
    if (supportsModelDiscovery) {
      rows.push({
        key: "discovery",
        label: t("modelDiscoveryLabel"),
        render: (offer) => <EndpointBadges offer={offer} discovery />,
      });
    }
    return rows;
  }, [supportsModelDiscovery, t, td]);
}

function publicOfferPosition(
  offers: readonly MarketplaceModelOfferDetail[],
  offerRef: string,
): number {
  const index = offers.findIndex((offer) => offer.offer_ref === offerRef);
  return index >= 0 ? index + 1 : offers.length + 1;
}

function DesktopComparison({
  offers,
  allOffers,
}: {
  offers: MarketplaceModelOfferDetail[];
  allOffers: MarketplaceModelOfferDetail[];
}) {
  const t = useTranslations("modelMarketplace.detail");
  const rows = useComparisonRows(offers);
  return (
      <Table
        aria-label={t("comparisonTableLabel")}
        className="min-w-max"
        containerProps={{
          "data-testid": "offer-comparison-scroll",
          className: "hidden min-w-0 max-w-full rounded-md border md:block",
        }}
      >
        <TableHeader>
          <TableRow>
            <TableHead className="sticky left-0 min-w-40 bg-background">{t("metricColumn")}</TableHead>
            {offers.map((offer) => (
              <TableHead
                key={offer.offer_ref}
                className="min-w-56 border-t-2 align-top"
                style={{ borderTopColor: chartColorForSeries(offer.offer_ref) }}
              >
                <OfferIdentity offer={offer} />
                <ByokPriceBreakdown
                  offer={offer}
                  position={publicOfferPosition(allOffers, offer.offer_ref)}
                />
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => (
            <TableRow key={row.key}>
              <TableCell
                className={cn(
                  "sticky left-0 bg-background font-medium",
                  row.muted && "text-muted-foreground",
                )}
                data-testid={row.priceKey ? "price-bucket-label" : undefined}
                data-price-key={row.priceKey}
              >
                <PriceBucketLabel row={row} />
              </TableCell>
              {offers.map((offer) => (
                <TableCell
                  key={offer.offer_ref}
                  className={cn(
                    "max-w-80 whitespace-normal tabular-nums",
                    row.muted && "text-muted-foreground",
                  )}
                >
                  {row.render(offer)}
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
  );
}

function MobileOfferFactSheet({
  offer,
  position,
  rows,
}: {
  offer: MarketplaceModelOfferDetail;
  position: number;
  rows: ComparisonRow[];
}) {
  const t = useTranslations("modelMarketplace.detail");
  return (
    <Card
      data-testid="mobile-offer-fact-sheet"
      className="gap-4 border-t-2 py-4"
      style={{ borderTopColor: chartColorForSeries(offer.offer_ref) }}
    >
      <CardHeader className="px-4">
        <CardTitle><OfferIdentity offer={offer} /></CardTitle>
        <ByokPriceBreakdown offer={offer} position={position} />
      </CardHeader>
      <CardContent className="flex flex-col gap-3 px-4">
        {rows.map((row) => (
          <div
            key={row.key}
            className="grid grid-cols-[minmax(7rem,0.8fr)_minmax(0,1.2fr)] gap-3 text-meta"
          >
            <span
              className="font-medium text-muted-foreground"
              data-testid={row.priceKey ? "price-bucket-label" : undefined}
              data-price-key={row.priceKey}
            >
              <PriceBucketLabel row={row} />
            </span>
            <div className={cn(
              "min-w-0 whitespace-normal tabular-nums",
              row.muted && "text-muted-foreground",
            )}>
              {row.render(offer)}
            </div>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function MobileComparison({
  offers,
  activeOfferRef,
  onActiveOfferChange,
}: {
  offers: MarketplaceModelOfferDetail[];
  activeOfferRef: string;
  onActiveOfferChange: (offerRef: string) => void;
}) {
  const t = useTranslations("modelMarketplace.detail");
  const rows = useComparisonRows(offers);
  const activeOffer = offers.find((offer) => offer.offer_ref === activeOfferRef);
  if (!activeOffer) return null;

  return (
    <div className="flex flex-col gap-3 md:hidden">
      <div className="max-w-full overflow-x-auto pb-1">
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          spacing={1}
          value={activeOfferRef}
          onValueChange={(next) => {
            if (next) onActiveOfferChange(next);
          }}
          aria-label={t("mobileOfferSelectionLabel")}
          className="w-max"
        >
          {offers.map((offer) => (
            <ToggleGroupItem
              key={offer.offer_ref}
              value={offer.offer_ref}
              aria-label={offer.display_name}
            >
              {offer.display_name}
              {!offer.available ? (
                <Badge variant="destructive">{t("offerUnavailable")}</Badge>
              ) : null}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      </div>
      <MobileOfferFactSheet
        offer={activeOffer}
        position={publicOfferPosition(offers, activeOffer.offer_ref)}
        rows={rows}
      />
    </div>
  );
}

interface OfferComparisonProps {
  offers: MarketplaceModelOfferDetail[];
  window: MarketplaceUsageWindow;
  usageStatus: MarketplaceUsageAvailability;
  adminDiagnostics?: Record<string, AdminOfferDiagnosticsView | undefined>;
}

export function OfferComparison({
  offers,
  window,
  usageStatus,
  adminDiagnostics,
}: OfferComparisonProps) {
  const t = useTranslations("modelMarketplace.detail");
  const offerKey = offers.map((offer) => offer.offer_ref).join("\u0000");
  const defaults = useMemo(() => defaultMarketplaceOfferRefs(offers), [offers]);
  const [selectionState, setSelectionState] = useState(() => ({
    offerKey,
    refs: defaults,
    limitReached: false,
  }));
  const selectedRefs = selectionState.offerKey === offerKey
    ? selectionState.refs
    : defaults;
  const limitReached = selectionState.offerKey === offerKey && selectionState.limitReached;
  const defaultMobileOfferRef = defaults[0] ?? offers[0]?.offer_ref ?? "";
  const [requestedMobileOfferRef, setRequestedMobileOfferRef] = useState(defaultMobileOfferRef);
  const activeMobileOfferRef = offers.some((offer) => offer.offer_ref === requestedMobileOfferRef)
    ? requestedMobileOfferRef
    : defaultMobileOfferRef;
  const selectedSet = new Set(selectedRefs);
  const selectedOffers = offers.filter((offer) => selectedSet.has(offer.offer_ref));
  const activeMobileOffer = offers.find((offer) => offer.offer_ref === activeMobileOfferRef);
  const hasSelectedByok = selectedOffers.some((offer) => offer.kind === "private");
  const showByokBillingNotice = hasSelectedByok || activeMobileOffer?.kind === "private";

  const changeSelection = (refs: string[]) => {
    if (refs.length > MAX_SELECTED_OFFERS) {
      setSelectionState({ offerKey, refs: selectedRefs, limitReached: true });
      return;
    }
    setSelectionState({ offerKey, refs, limitReached: false });
  };
  const changeActiveMobileOffer = (ref: string) => {
    if (!offers.some((offer) => offer.offer_ref === ref)) return;
    setRequestedMobileOfferRef(ref);
  };

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-col gap-2">
        <div className="flex items-baseline justify-between gap-3">
          <h2 className="text-lg font-semibold tracking-tight">{t("comparisonTitle")}</h2>
          <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
            {t("selectedCount", { count: selectedOffers.length, max: MAX_SELECTED_OFFERS })}
          </span>
        </div>
        <div className="max-w-full overflow-x-auto pb-1">
          <ToggleGroup
            type="multiple"
            variant="outline"
            size="sm"
            spacing={1}
            value={selectedRefs}
            onValueChange={changeSelection}
            aria-label={t("offerSelectionLabel")}
            className="w-max"
          >
            {offers.map((offer) => (
              <ToggleGroupItem key={offer.offer_ref} value={offer.offer_ref} aria-label={offer.display_name}>
                <span
                  aria-hidden="true"
                  className="size-2 rounded-[2px]"
                  style={{ backgroundColor: chartColorForSeries(offer.offer_ref) }}
                />
                {offer.display_name}
                {!offer.available ? (
                  <Badge variant="destructive">{t("offerUnavailable")}</Badge>
                ) : null}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </div>
        {limitReached ? (
          <p className="text-xs text-destructive" role="status">
            {t("selectionLimit", { max: MAX_SELECTED_OFFERS })}
          </p>
        ) : null}
      </div>

      {selectedOffers.length > 0 ? (
        <DesktopComparison offers={selectedOffers} allOffers={offers} />
      ) : (
        <Alert>
          <AlertTitle>{t("noOfferSelectedTitle")}</AlertTitle>
          <AlertDescription>{t("noOfferSelectedDescription")}</AlertDescription>
        </Alert>
      )}

      <MobileComparison
        offers={offers}
        activeOfferRef={activeMobileOfferRef}
        onActiveOfferChange={changeActiveMobileOffer}
      />

      {showByokBillingNotice ? (
        <p className={cn(
          "text-xs text-muted-foreground",
          !hasSelectedByok && "md:hidden",
        )}>
          {t("byokBillingNotice")}
        </p>
      ) : null}

      {selectedOffers.length > 0 ? (
        <OfferStatusMatrix offers={selectedOffers} window={window} />
      ) : null}

      <OfferTrendWorkspace offers={selectedOffers} />
      <UsageReference offers={selectedOffers} usageStatus={usageStatus} />

      {adminDiagnostics && selectedOffers.some((offer) => adminDiagnostics[offer.offer_ref]) ? (
        <Card className="gap-4 py-4">
          <CardHeader className="px-4">
            <CardTitle className="text-sm">{t("adminDiagnosticsTitle")}</CardTitle>
            <CardAction>
              <Badge variant="outline">{t("adminDiagnosticsAdminOnly")}</Badge>
            </CardAction>
          </CardHeader>
          <CardContent className="grid grid-cols-1 gap-3 px-4 lg:grid-cols-2">
            {selectedOffers.map((offer) => {
              const diagnostics = adminDiagnostics[offer.offer_ref];
              return diagnostics ? (
                <div key={offer.offer_ref} className="flex flex-col gap-2 rounded-lg border p-3">
                  <OfferIdentity offer={offer} />
                  <AdminDiagnostics diagnostics={diagnostics} />
                </div>
              ) : null;
            })}
          </CardContent>
        </Card>
      ) : null}
    </div>
  );
}
