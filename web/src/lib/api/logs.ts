import { useQuery } from "@tanstack/react-query";
import { api, buildQuery } from "./client";
import type { PaginatedResponse, PaginatedParams, UsageLog, UsageLogTrace } from "@/lib/types";

export function useLogs(params: PaginatedParams = {}) {
  return useQuery({
    queryKey: ["logs", params],
    queryFn: () => api.get<PaginatedResponse<UsageLog>>(`/logs${buildQuery(params)}`),
  });
}

export function useLogTrace(requestId: string | null) {
  return useQuery({
    queryKey: ["log-trace", requestId],
    queryFn: () => api.get<UsageLogTrace>(`/logs/${requestId}/trace`),
    enabled: !!requestId,
  });
}
