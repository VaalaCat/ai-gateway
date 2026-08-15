import { useQuery } from "@tanstack/react-query";

import { api } from "./client";

export interface CapabilitiesResponse {
  token: {
    can_edit_model_whitelist: boolean;
  };
  model_marketplace?: boolean;
  generic_api?: {
    services: boolean;
    access: boolean;
    logs: boolean;
    websocket: boolean;
    service_actions?: {
      create: boolean;
      manage_all: boolean;
      manage_ids: number[];
    };
  };
}

export function isGenericAPIVisible(
  capabilities: CapabilitiesResponse | undefined,
  feature: "services" | "access" | "logs",
) {
  return capabilities?.generic_api?.[feature] === true;
}

export function isModelMarketplaceVisible(
  capabilities: CapabilitiesResponse | undefined,
  isAdmin: boolean,
) {
  return isAdmin || capabilities?.model_marketplace === true;
}

export function canCreateAPIService(capabilities: CapabilitiesResponse | undefined) {
  return capabilities?.generic_api?.service_actions?.create === true;
}

export function canManageAPIService(capabilities: CapabilitiesResponse | undefined, serviceId: number) {
  const actions = capabilities?.generic_api?.service_actions;
  if (!actions || !Number.isInteger(serviceId) || serviceId <= 0) return false;
  return actions.manage_all === true || actions.manage_ids?.includes(serviceId) === true;
}

export function useCapabilities(viewerId: number | undefined) {
  return useQuery({
    queryKey: ["capabilities", { viewerId: viewerId ?? null }],
    queryFn: () => api.get<CapabilitiesResponse>("/capabilities"),
    staleTime: 30_000,
    enabled: Number.isInteger(viewerId) && (viewerId ?? 0) > 0,
  });
}
