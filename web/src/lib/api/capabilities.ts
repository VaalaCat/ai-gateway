import { useQuery } from "@tanstack/react-query";

import { api } from "./client";

export interface CapabilitiesResponse {
  token: {
    can_edit_model_whitelist: boolean;
  };
  model_marketplace?: boolean;
}

export function isModelMarketplaceVisible(
  capabilities: CapabilitiesResponse | undefined,
  isAdmin: boolean,
) {
  return isAdmin || capabilities?.model_marketplace === true;
}

export function useCapabilities(viewerId: number | undefined) {
  return useQuery({
    queryKey: ["capabilities", { viewerId: viewerId ?? null }],
    queryFn: () => api.get<CapabilitiesResponse>("/capabilities"),
    staleTime: 30_000,
    enabled: Number.isInteger(viewerId) && (viewerId ?? 0) > 0,
  });
}
