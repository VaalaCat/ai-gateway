import type {
  MarketplaceHealthStatus,
  MarketplaceModelOfferDetail,
} from "@/lib/api/model-marketplace";

export type MarketplacePresentationStatus =
  | MarketplaceHealthStatus
  | "stale"
  | "unavailable"
  | "in_progress";

export const MARKETPLACE_STATUS_PRESENTATION: Record<
  MarketplacePresentationStatus,
  {
    tracker: string;
    badge: string;
    dot: string;
  }
> = {
  operational: {
    tracker: "bg-emerald-600 dark:bg-emerald-500",
    badge: "border-emerald-600/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
    dot: "bg-emerald-500 dark:bg-emerald-400",
  },
  degraded: {
    tracker: "bg-amber-500 dark:bg-amber-400",
    badge: "border-amber-600/30 bg-amber-500/10 text-amber-700 dark:text-amber-400",
    dot: "bg-amber-500 dark:bg-amber-400",
  },
  outage: {
    tracker: "bg-red-600 dark:bg-red-500",
    badge: "border-red-600/30 bg-red-500/10 text-red-700 dark:text-red-400",
    dot: "bg-red-500 dark:bg-red-400",
  },
  unknown: {
    tracker: "bg-gray-400 dark:bg-gray-500",
    badge: "border-gray-500/30 bg-gray-500/10 text-gray-700 dark:text-gray-400",
    dot: "bg-gray-400 dark:bg-gray-500",
  },
  stale: {
    tracker: "bg-gray-400 dark:bg-gray-500",
    badge: "border-gray-500/30 bg-gray-500/10 text-gray-700 dark:text-gray-400",
    dot: "bg-gray-400 dark:bg-gray-500",
  },
  unavailable: {
    tracker: "bg-red-600 dark:bg-red-500",
    badge: "border-red-600/30 bg-red-500/10 text-red-700 dark:text-red-400",
    dot: "bg-red-500 dark:bg-red-400",
  },
  in_progress: {
    tracker: "ring-2 ring-amber-500/80 ring-inset",
    badge: "border-amber-600/30 bg-amber-500/10 text-amber-700 dark:text-amber-400",
    dot: "bg-amber-500 dark:bg-amber-400",
  },
};

export function marketplaceOfferStatus(
  offer: Pick<MarketplaceModelOfferDetail, "performance_status" | "performance">,
): MarketplacePresentationStatus {
  if (offer.performance_status === "unavailable" || offer.performance_status === "stale") {
    return offer.performance_status;
  }
  return offer.performance.status;
}
