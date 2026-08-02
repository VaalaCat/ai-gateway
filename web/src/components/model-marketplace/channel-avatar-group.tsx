"use client";

import { useTranslations } from "next-intl";

import {
  Avatar,
  AvatarBadge,
  AvatarFallback,
  AvatarGroup,
  AvatarGroupCount,
} from "@/components/ui/avatar";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { useIsMobile } from "@/hooks/use-mobile";
import type { MarketplaceModelOffer } from "@/lib/api/model-marketplace";
import { cn } from "@/lib/utils";
import { MARKETPLACE_STATUS_PRESENTATION } from "./marketplace-status";

interface ChannelAvatarGroupProps {
  offers: readonly MarketplaceModelOffer[];
  desktopLimit?: number;
  mobileLimit?: number;
  className?: string;
}

function segmentGraphemes(value: string): string[] {
  if (typeof Intl.Segmenter === "function") {
    return Array.from(
      new Intl.Segmenter(undefined, { granularity: "grapheme" }).segment(value),
      ({ segment }) => segment,
    );
  }
  return Array.from(value);
}

function firstGrapheme(value: string): string {
  return segmentGraphemes(value)[0] ?? "";
}

export function channelInitials(displayName: string): string {
  const words = displayName.trim().split(/[\s\-_./]+/u).filter(Boolean);
  if (words.length >= 2) {
    return `${firstGrapheme(words[0])}${firstGrapheme(words[1])}`.toUpperCase();
  }

  const graphemes = segmentGraphemes(words[0] ?? "");
  if (graphemes.length === 0) return "?";
  return /\p{Script=Han}/u.test(graphemes[0])
    ? graphemes[0]
    : graphemes.slice(0, 2).join("").toUpperCase();
}

function publicEndpoints(offer: MarketplaceModelOffer) {
  return offer.supported_endpoints ?? [];
}

function marketplaceOfferKindLabelKey(kind: MarketplaceModelOffer["kind"]): "kind.platform" | "kind.byok" {
  return kind === "private" ? "kind.byok" : "kind.platform";
}

function ChannelOfferIdentity({ offer }: { offer: MarketplaceModelOffer }) {
  const t = useTranslations("modelMarketplace");
  const endpoints = publicEndpoints(offer);

  return (
    <div className="grid gap-1.5 text-xs">
      <p className="font-medium">{offer.display_name}</p>
      <dl className="grid grid-cols-[auto_1fr] gap-x-2 gap-y-1 text-current opacity-75">
        <dt>{t("channelKindLabel")}</dt>
        <dd>{t(marketplaceOfferKindLabelKey(offer.kind))}</dd>
        <dt>{t("channelAvailabilityLabel")}</dt>
        <dd>{offer.available ? t("channelAvailable") : t("channelUnavailable")}</dd>
        <dt>{t("channelEndpointsLabel")}</dt>
        <dd>
          {endpoints.length > 0
            ? endpoints.map((endpoint) => t(`endpoint.${endpoint}`)).join(", ")
            : t("channelNoEndpoints")}
        </dd>
      </dl>
    </div>
  );
}

export function ChannelAvatar({ offer }: { offer: MarketplaceModelOffer }) {
  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <Avatar data-testid="channel-avatar" tabIndex={0} aria-label={offer.display_name}>
            <AvatarFallback>{channelInitials(offer.display_name)}</AvatarFallback>
            <AvatarBadge className={offer.available
              ? MARKETPLACE_STATUS_PRESENTATION.operational.dot
              : MARKETPLACE_STATUS_PRESENTATION.unavailable.dot}
            />
          </Avatar>
        </TooltipTrigger>
        <TooltipContent sideOffset={6}>
          <ChannelOfferIdentity offer={offer} />
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

export function ChannelAvatarGroup({
  offers,
  desktopLimit = 5,
  mobileLimit = 3,
  className,
}: ChannelAvatarGroupProps) {
  const t = useTranslations("modelMarketplace");
  const isMobile = useIsMobile();
  const orderedOffers = offers
    .map((offer, index) => ({ offer, index }))
    .sort((a, b) => Number(b.offer.available) - Number(a.offer.available) || a.index - b.index)
    .map(({ offer }) => offer);

  if (orderedOffers.length === 0) return null;

  const limit = Math.max(0, isMobile ? mobileLimit : desktopLimit);
  const visibleOffers = orderedOffers.slice(0, limit);
  const overflow = orderedOffers.length - visibleOffers.length;

  const avatars = (
    <TooltipProvider>
      <AvatarGroup className={cn(className)} aria-label={t("channelGroupLabel")}>
        {visibleOffers.map((offer) => (
          <ChannelAvatar key={offer.offer_ref} offer={offer} />
        ))}
        {overflow > 0 ? (
          <AvatarGroupCount asChild>
            <PopoverTrigger asChild>
              <button
                type="button"
                aria-label={t("showAllChannels", { count: orderedOffers.length })}
              >
                +{overflow}
              </button>
            </PopoverTrigger>
          </AvatarGroupCount>
        ) : null}
      </AvatarGroup>
    </TooltipProvider>
  );

  if (overflow === 0) return avatars;

  return (
    <Popover>
      {avatars}
      <PopoverContent
        align="end"
        aria-label={t("allChannelsDialogLabel", { count: orderedOffers.length })}
        className="p-2"
      >
        <ScrollArea className="max-h-72 pr-2">
          <div className="grid gap-2">
            {orderedOffers.map((offer) => (
              <ChannelOfferIdentity key={offer.offer_ref} offer={offer} />
            ))}
          </div>
        </ScrollArea>
      </PopoverContent>
    </Popover>
  );
}
