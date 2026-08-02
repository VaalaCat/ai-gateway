"use client";

import Link from "next/link";
import { AlertTriangle, Route } from "lucide-react";
import { useTranslations } from "next-intl";

import { ModelProviderLogo } from "@/components/business/model-provider-logo";
import { ChannelAvatarGroup } from "@/components/model-marketplace/channel-avatar-group";
import { MARKETPLACE_STATUS_PRESENTATION } from "@/components/model-marketplace/marketplace-status";
import { MarketplacePriceGrid } from "@/components/model-marketplace/marketplace-price-grid";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Tracker } from "@/components/ui/tracker";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import type {
  MarketplaceEndpoint,
  MarketplaceModel,
  MarketplaceModelOffer,
  MarketplaceModelPerformance,
} from "@/lib/api/model-marketplace";
import { marketplaceDetailHref } from "@/lib/model-marketplace-navigation";
import { percentValueOrNull } from "@/lib/model-marketplace-values";
import { formatPercentValue } from "@/lib/utils/format";

const invocationEndpointOrder: MarketplaceEndpoint[] = [
  "chat_completions",
  "responses",
  "messages",
];

interface ModelCardRendererProps<T extends MarketplaceModel> {
  model: T;
  detailTokenId?: number;
}

interface MarketplaceModelIdentityProps {
  modelName: string;
  displayName: string;
  detailHref: string;
  description?: string;
  provider?: string;
  routing?: boolean;
}

function MarketplaceModelIdentity({
  modelName,
  displayName,
  detailHref,
  description,
  provider,
  routing = false,
}: MarketplaceModelIdentityProps) {
  const t = useTranslations("modelMarketplace");

  return (
    <div data-testid="marketplace-model-identity" className="flex min-w-0 items-start gap-3">
      <div className="flex size-9 shrink-0 items-center justify-center rounded-md bg-muted">
        {routing ? (
          <Route className="size-4 text-muted-foreground" aria-hidden="true" />
        ) : (
          <ModelProviderLogo modelName={modelName} size={22} />
        )}
      </div>
      <div className="min-w-0 space-y-1">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <Link
            data-testid="marketplace-row-link"
            className="truncate rounded-sm text-body font-semibold after:absolute after:inset-0 after:rounded-lg hover:underline focus-visible:outline-none focus-visible:after:ring-2 focus-visible:after:ring-ring focus-visible:after:ring-offset-2"
            href={detailHref}
          >
            {displayName}
          </Link>
          {routing ? (
            <Badge variant="secondary">{t("kind.routing")}</Badge>
          ) : (
            <Badge variant="outline">{provider || t("providerUnknown")}</Badge>
          )}
        </div>
        <p className="truncate text-meta text-muted-foreground">{modelName}</p>
        {description ? (
          <p className="truncate text-meta leading-4 text-muted-foreground">{description}</p>
        ) : null}
      </div>
    </div>
  );
}

function formatPerformanceUtc(timestamp: number): string {
  return `${new Intl.DateTimeFormat("en-GB", {
    timeZone: "UTC",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(new Date(timestamp * 1_000))} UTC`;
}

interface ModelPerformanceMetricProps {
  label: string;
  value: string;
  explanation: string;
}

function ModelPerformanceMetric({ label, value, explanation }: ModelPerformanceMetricProps) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label={explanation}
          className="inline-flex cursor-default items-baseline rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
        >
          <span className="text-muted-foreground">{label} </span>
          <span className="font-medium">{value}</span>
        </button>
      </TooltipTrigger>
      <TooltipContent>{explanation}</TooltipContent>
    </Tooltip>
  );
}

function ModelPerformanceSummary({
  performance,
  windowLabel,
}: {
  performance: MarketplaceModelPerformance;
  windowLabel: string;
}) {
  const t = useTranslations("modelMarketplace");
  const observedSla = percentValueOrNull(performance.success_rate);
  const cacheHitRate = percentValueOrNull(performance.cache_hit_rate);
  const observedSlaText = observedSla === null ? "—" : formatPercentValue(observedSla);
  const cacheHitRateText = cacheHitRate === null ? "—" : formatPercentValue(cacheHitRate);
  const staleLabel = performance.performance_status === "stale"
    ? t("detail.statusState.stale")
    : null;
  const slaExplanation = [
    t("modelSlaSummaryTooltip", { window: windowLabel, value: observedSlaText }),
    staleLabel,
  ].filter(Boolean).join(" · ");
  const cacheExplanation = [
    t("modelCacheHitRateSummaryTooltip", { window: windowLabel, value: cacheHitRateText }),
    staleLabel,
  ].filter(Boolean).join(" · ");

  return (
    <div className="flex min-w-0 items-center justify-between gap-3 tabular-nums">
      <span className="text-label text-muted-foreground">{windowLabel}</span>
      <div className="flex shrink-0 items-center gap-3 text-meta">
        <ModelPerformanceMetric
          label={t("modelSlaLabel")}
          value={observedSlaText}
          explanation={slaExplanation}
        />
        <ModelPerformanceMetric
          label={t("modelCacheHitRateLabel")}
          value={cacheHitRateText}
          explanation={cacheExplanation}
        />
      </div>
    </div>
  );
}

function ModelPerformanceHistory({ performance }: { performance: MarketplaceModelPerformance }) {
  const t = useTranslations("modelMarketplace");
  const staleLabel = performance.performance_status === "stale"
    ? t("detail.statusState.stale")
    : null;
  const data = performance.status_history.map((bucket, index) => {
    const interval = `${formatPerformanceUtc(bucket.started_at)} – ${formatPerformanceUtc(bucket.ended_at)}`;
    const successRate = percentValueOrNull(bucket.success_rate);
    const observedRate = successRate === null ? "—" : formatPercentValue(successRate);
    const statusLabel = t(`status.${bucket.status}`);
    const inProgress = bucket.in_progress ? t("detail.statusState.in_progress") : null;
    const bucketSla = t("modelSlaBucketTooltip", { value: observedRate });
    const presentationStatus = performance.performance_status === "stale" ? "stale" : bucket.status;
    return {
      key: `${bucket.started_at}:${index}`,
      color: MARKETPLACE_STATUS_PRESENTATION[presentationStatus].tracker,
      ariaLabel: [interval, statusLabel, bucketSla, staleLabel, inProgress].filter(Boolean).join(" · "),
      state: presentationStatus,
      inProgress: bucket.in_progress,
      indicatorClassName: bucket.in_progress
        ? MARKETPLACE_STATUS_PRESENTATION.in_progress.tracker
        : undefined,
      tooltip: (
        <div className="flex max-w-64 flex-col gap-1">
          <span className="font-medium">{statusLabel}</span>
          <span>{interval}</span>
          <span>{bucketSla}</span>
          {staleLabel ? <span>{staleLabel}</span> : null}
          {inProgress ? <span>{inProgress}</span> : null}
        </div>
      ),
    };
  });

  return <Tracker layout="compact" data={data} />;
}

function ModelPerformanceStrip({ performance }: { performance: MarketplaceModelPerformance }) {
  const t = useTranslations("modelMarketplace");
  const windowLabel = performance.window === "24h"
    ? t("performanceWindow24h")
    : t(`detail.window.${performance.window}`);

  return (
    <TooltipProvider>
      <div data-testid="model-performance-strip" className="relative z-10 flex min-w-0 flex-col gap-2 tabular-nums">
        <ModelPerformanceSummary performance={performance} windowLabel={windowLabel} />
        <ModelPerformanceHistory performance={performance} />
      </div>
    </TooltipProvider>
  );
}

function EndpointSummary({ offers }: { offers: readonly MarketplaceModelOffer[] }) {
  const t = useTranslations("modelMarketplace");
  const invocationEndpoints = invocationEndpointOrder.filter((endpoint) =>
    offers.some((offer) => offer.supported_endpoints.includes(endpoint)),
  );
  const supportsModelDiscovery = offers.some((offer) =>
    offer.supported_endpoints.includes("models"),
  );

  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <span className="text-label text-muted-foreground">{t("invocationEndpointsLabel")}</span>
      <div className="flex flex-wrap gap-1">
        {invocationEndpoints.length > 0 ? invocationEndpoints.map((endpoint) => (
          <Badge key={endpoint} variant="outline">{t(`endpoint.${endpoint}`)}</Badge>
        )) : <span className="text-body text-muted-foreground">—</span>}
      </div>
      {supportsModelDiscovery ? (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-label text-muted-foreground">{t("modelDiscoveryLabel")}</span>
          <Badge variant="outline">{t("endpoint.models")}</Badge>
        </div>
      ) : null}
    </div>
  );
}

function RealModelCard({
  model,
  detailTokenId,
}: ModelCardRendererProps<Extract<MarketplaceModel, { kind: "real" }>>) {
  const detailHref = marketplaceDetailHref({
    model: model.real.model_name,
    tokenId: detailTokenId,
    window: "24h",
  });
  const availableOffers = model.real.offers.filter((offer) => offer.available);
  const availableOfferPrices = {
    input: availableOffers.map((offer) => offer.pricing.gateway_charge.input),
    cache_read: availableOffers.map((offer) => offer.pricing.gateway_charge.cache_read),
    output: availableOffers.map((offer) => offer.pricing.gateway_charge.output),
    cache_write: availableOffers.map((offer) => offer.pricing.gateway_charge.cache_write),
  };

  return (
    <article
      className="group relative isolate grid min-w-0 gap-3 px-4 py-4 md:grid-cols-[minmax(0,1fr)_minmax(22rem,1.2fr)]"
      role="article"
      aria-label={model.real.metadata.display_name || model.real.model_name}
    >
      <MarketplaceModelIdentity
        modelName={model.real.model_name}
        displayName={model.real.metadata.display_name || model.real.model_name}
        description={model.real.metadata.description}
        provider={model.real.metadata.provider}
        detailHref={detailHref}
      />
      <ModelPerformanceStrip performance={model.real.performance} />
      <MarketplacePriceGrid prices={availableOfferPrices} mode="range" />
      <div className="flex min-w-0 items-end justify-between gap-3">
        <EndpointSummary offers={availableOffers} />
        <ChannelAvatarGroup offers={model.real.offers} className="relative z-10" />
      </div>
    </article>
  );
}

function RoutingModelCard({
  model,
  detailTokenId,
}: ModelCardRendererProps<Extract<MarketplaceModel, { kind: "routing" }>>) {
  const t = useTranslations("modelMarketplace");
  const detailHrefFor = (modelName: string) => marketplaceDetailHref({
    model: modelName,
    tokenId: detailTokenId,
    window: "24h",
  });
  const knownWarnings = new Set([
    "cycle",
    "max_depth",
    "disabled",
    "model_not_found",
    "no_visible_offer",
  ]);

  return (
    <article
      className="group relative isolate grid min-w-0 gap-3 px-4 py-4 md:grid-cols-[minmax(0,1fr)_minmax(22rem,1.2fr)]"
      role="article"
      aria-label={model.routing.display_name || model.routing.model_name}
    >
      <MarketplaceModelIdentity
        modelName={model.routing.model_name}
        displayName={model.routing.display_name || model.routing.model_name}
        detailHref={detailHrefFor(model.routing.model_name)}
        routing
      />
      <div className="flex min-w-0 flex-col gap-2">
        <div className="flex items-baseline justify-between gap-3">
          <span className="text-label text-muted-foreground">{t("reachableModelsLabel")}</span>
          <span className="text-body font-semibold tabular-nums">
            {model.routing.reachable_real_models.length}
          </span>
        </div>
        <div className="flex flex-wrap gap-1.5">
          {model.routing.reachable_real_models.map((name) => (
            <Badge key={name} variant="outline" asChild>
              <Link className="relative z-10" href={detailHrefFor(name)}>{name}</Link>
            </Badge>
          ))}
          {model.routing.reachable_real_models.length === 0 &&
          model.routing.routing_warnings.length === 0 ? (
            <span className="text-body text-muted-foreground">{t("routingNoReachableModels")}</span>
          ) : null}
        </div>
        <p className="text-meta text-muted-foreground">{t("viewReachableRealModels")}</p>
      </div>
      {model.routing.routing_warnings.length > 0 ? (
        <Alert variant="destructive" className="md:col-span-2">
          <AlertTriangle aria-hidden="true" />
          <AlertTitle>{t("routingWarning.title")}</AlertTitle>
          <AlertDescription className="flex flex-wrap gap-1.5">
            {model.routing.routing_warnings.map((warning, index) => {
              const safeWarning = knownWarnings.has(warning) ? warning : "unknown";
              return (
                <Badge key={`${safeWarning}:${index}`} variant="outline">
                  {t(`routingWarning.code.${safeWarning}`)}
                </Badge>
              );
            })}
          </AlertDescription>
        </Alert>
      ) : null}
    </article>
  );
}

const modelCardRenderers = {
  real: RealModelCard,
  routing: RoutingModelCard,
} satisfies {
  real: React.ComponentType<ModelCardRendererProps<Extract<MarketplaceModel, { kind: "real" }>>>;
  routing: React.ComponentType<ModelCardRendererProps<Extract<MarketplaceModel, { kind: "routing" }>>>;
};

export function ModelCard({ model, detailTokenId }: ModelCardRendererProps<MarketplaceModel>) {
  if (model.kind === "real") {
    const Renderer = modelCardRenderers.real;
    return <Renderer model={model} detailTokenId={detailTokenId} />;
  }
  const Renderer = modelCardRenderers.routing;
  return <Renderer model={model} detailTokenId={detailTokenId} />;
}
