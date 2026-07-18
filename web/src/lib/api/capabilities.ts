import { useQuery } from "@tanstack/react-query";

import { api } from "./client";

export interface CapabilitiesResponse {
  token: {
    can_edit_model_whitelist: boolean;
  };
}

export function useCapabilities() {
  return useQuery({
    queryKey: ["capabilities"],
    queryFn: () => api.get<CapabilitiesResponse>("/capabilities"),
    staleTime: 30_000,
  });
}
