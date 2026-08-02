import type { MarketplaceUsageWindow } from "@/lib/api/model-marketplace";

interface MarketplaceDetailHrefParams {
  model: string;
  tokenId?: number;
  window: MarketplaceUsageWindow;
  offerRef?: string;
}

export function marketplaceDetailHref({
  model,
  tokenId,
  window,
  offerRef,
}: MarketplaceDetailHrefParams) {
  const query = new URLSearchParams();
  query.set("model", model);
  if (Number.isInteger(tokenId) && (tokenId ?? 0) > 0) {
    query.set("token_id", String(tokenId));
  }
  query.set("window", window);
  if (offerRef?.trim()) query.set("offer_ref", offerRef.trim());
  return `/model-marketplace/detail?${query.toString()}`;
}
