import { useQuery } from "@tanstack/react-query";
import { api, buildQuery } from "./client";
import type { PaginatedResponse, PaginatedParams, UsageLog, UsageLogTrace } from "@/lib/types";

interface UseLogsParams extends PaginatedParams {
  start?: number;
  end?: number;
  user_id?: number;
  token_id?: number;
  channel_id?: number;
  model_name?: string;
  status?: string;
  private_channel_id?: number;
}

export function useLogs(params: UseLogsParams = {}) {
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
