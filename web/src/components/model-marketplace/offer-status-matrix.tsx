"use client";

import { useTranslations } from "next-intl";

import { channelInitials } from "@/components/model-marketplace/channel-avatar-group";
import {
  MARKETPLACE_STATUS_PRESENTATION,
  marketplaceOfferStatus,
  type MarketplacePresentationStatus,
} from "@/components/model-marketplace/marketplace-status";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Tracker, type TrackerBlock } from "@/components/ui/tracker";
import type {
  MarketplaceHealthStatus,
  MarketplaceModelOfferDetail,
  MarketplaceUsageWindow,
} from "@/lib/api/model-marketplace";
import {
  percentValueOrNull,
  unixSecondsOrNull,
} from "@/lib/model-marketplace-values";
import { formatPercentValue } from "@/lib/utils/format";

interface WindowShape {
  points: number;
  seconds: number;
}

const WINDOW_SHAPE = {
  "24h": { points: 24, seconds: 3_600 },
  "7d": { points: 28, seconds: 21_600 },
  "30d": { points: 30, seconds: 86_400 },
} satisfies Record<MarketplaceUsageWindow, WindowShape>;

type BaseStatus = Exclude<MarketplacePresentationStatus, "in_progress">;

export interface NormalizedStatusBucket {
  started_at: number | null;
  ended_at: number | null;
  status: MarketplaceHealthStatus;
  baseState: BaseStatus;
  in_progress: boolean;
  hasObservation: boolean;
  successRate: number | null | undefined;
}

function offerBaseState(
  offer: MarketplaceModelOfferDetail,
  status: MarketplaceHealthStatus,
): BaseStatus {
  return marketplaceOfferStatus({
    performance_status: offer.performance_status,
    performance: { ...offer.performance, status },
  }) as BaseStatus;
}

export function normalizeStatusBuckets(
  offer: MarketplaceModelOfferDetail,
  window: MarketplaceUsageWindow,
): NormalizedStatusBucket[] {
  const shape = WINDOW_SHAPE[window];
  const successRateByStart = new Map(
    offer.trend_series.map((point) => [point.started_at, point.success_rate]),
  );
  // The server owns chronological ordering; preserve it and keep the final window.
  const observations = offer.status_history.slice(-shape.points).map((bucket) => ({
    started_at: unixSecondsOrNull(bucket.started_at),
    ended_at: unixSecondsOrNull(bucket.ended_at),
    status: bucket.status,
    baseState: offerBaseState(offer, bucket.status),
    in_progress: bucket.in_progress,
    hasObservation: true,
    successRate: successRateByStart.get(bucket.started_at),
  }));
  const missing = shape.points - observations.length;
  const missingBaseState = offerBaseState(offer, "unknown");
  const placeholders = Array.from({ length: missing }, (): NormalizedStatusBucket => ({
    started_at: null,
    ended_at: null,
    status: "unknown",
    baseState: missingBaseState,
    in_progress: false,
    hasObservation: false,
    successRate: undefined,
  }));
  return [...placeholders, ...observations];
}

function formatUtc(timestamp: number | null): string | null {
  if (timestamp === null) return null;
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

function trackerBlocks(
  buckets: readonly NormalizedStatusBucket[],
  offer: MarketplaceModelOfferDetail,
  t: ReturnType<typeof useTranslations>,
): TrackerBlock[] {
  return buckets.map((bucket, index) => {
    const startedAt = formatUtc(bucket.started_at);
    const endedAt = formatUtc(bucket.ended_at);
    const observedInterval = startedAt && endedAt ? `${startedAt} – ${endedAt}` : null;
    const statusLabel = t(`statusState.${bucket.baseState}`);
    const successRate = percentValueOrNull(bucket.successRate);
    const observedSla = t("observedSla", {
      value: successRate === null ? "—" : formatPercentValue(successRate),
    });
    const ariaParts = [offer.display_name, statusLabel];
    if (bucket.hasObservation) {
      if (observedInterval) ariaParts.push(observedInterval);
      ariaParts.push(observedSla);
    }
    if (bucket.in_progress) ariaParts.push(t("statusState.in_progress"));

    return {
      key: `${offer.offer_ref}:${index}`,
      color: MARKETPLACE_STATUS_PRESENTATION[bucket.baseState].tracker,
      state: bucket.baseState,
      inProgress: bucket.in_progress,
      indicatorClassName: bucket.in_progress
        ? MARKETPLACE_STATUS_PRESENTATION.in_progress.tracker
        : undefined,
      ariaLabel: ariaParts.join(" · "),
      tooltip: (
        <div className="flex max-w-64 flex-col gap-1">
          <span className="font-medium">{statusLabel}</span>
          {bucket.hasObservation ? (
            <>
              {observedInterval ? <span>{observedInterval}</span> : null}
              <span>{observedSla}</span>
            </>
          ) : null}
          {bucket.in_progress ? <span>{t("statusState.in_progress")}</span> : null}
        </div>
      ),
    };
  });
}

function OfferStatusRow({
  offer,
  window,
}: {
  offer: MarketplaceModelOfferDetail;
  window: MarketplaceUsageWindow;
}) {
  const t = useTranslations("modelMarketplace.detail");
  const buckets = normalizeStatusBuckets(offer, window);

  return (
    <div
      data-testid="offer-status-row"
      className="grid grid-cols-[9rem_max-content] items-center gap-3"
    >
      <div className="flex min-w-0 items-center gap-2">
        <Avatar size="sm" aria-label={offer.display_name}>
          <AvatarFallback>{channelInitials(offer.display_name)}</AvatarFallback>
        </Avatar>
        <span className="truncate text-label" title={offer.display_name}>
          {offer.display_name}
        </span>
      </div>
      <Tracker layout="compact" data={trackerBlocks(buckets, offer, t)} />
    </div>
  );
}

function SharedTimeline({ window }: { window: MarketplaceUsageWindow }) {
  const t = useTranslations("modelMarketplace.detail");
  const shape = WINDOW_SHAPE[window];
  return (
    <div className="grid grid-cols-[9rem_max-content] items-end gap-3 text-meta text-muted-foreground">
      <span>{t("windowLabel")}</span>
      <div
        data-testid="offer-status-timeline"
        data-points={shape.points}
        role="group"
        aria-label={t("statusTimelineLabel")}
        className="grid gap-0.5 tabular-nums"
        style={{ gridTemplateColumns: `repeat(${shape.points}, 5px)` }}
      >
        <span className="whitespace-nowrap" style={{ gridColumn: "1 / span 4" }}>
          {t("statusTimelineStart", { window: t(`window.${window}`) })}
        </span>
        <span
          className="text-right whitespace-nowrap"
          style={{ gridColumn: `${shape.points - 3} / span 4` }}
        >
          {t("statusTimelineEnd")}
        </span>
      </div>
    </div>
  );
}

export function OfferStatusMatrix({
  offers,
  window,
}: {
  offers: MarketplaceModelOfferDetail[];
  window: MarketplaceUsageWindow;
}) {
  const t = useTranslations("modelMarketplace.detail");
  return (
    <section aria-labelledby="marketplace-status-title" data-testid="offer-status-matrix">
      <Card className="gap-4 py-4">
        <CardHeader className="gap-1 px-4">
          <CardTitle id="marketplace-status-title" className="text-lg tracking-tight">
            {t("statusHistoryTitle")}
          </CardTitle>
        </CardHeader>
        <CardContent className="px-4">
          <div
            data-testid="offer-status-scroll"
            className="max-w-full overflow-x-auto pb-1"
          >
            <div className="flex min-w-max flex-col gap-2">
              <SharedTimeline window={window} />
              {offers.map((offer) => (
                <OfferStatusRow key={offer.offer_ref} offer={offer} window={window} />
              ))}
            </div>
          </div>
        </CardContent>
      </Card>
    </section>
  );
}
