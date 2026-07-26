import { useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type { Stats, TrendItem } from "@/lib/types";
import type { ChartTopN } from "@/lib/types";
import type { Bucket } from "./dashboard";
import type { ObsRangeParams } from "@/lib/types/observability";

export interface ModelDistributionResponse {
  buckets: Bucket[];
  series_order: string[];
}

export interface ModelDistributionParams extends ObsRangeParams {
  model?: string;
  user_id?: number;
  top_n?: ChartTopN;
}

export function useStats() {
  return useQuery({
    queryKey: ["stats"],
    queryFn: () => api.get<Stats>("/stats/overview"),
  });
}

export function useStatsTrend(days: number = 30) {
  return useQuery({
    queryKey: ["stats-trend", days],
    queryFn: () => api.get<{ items: TrendItem[] }>(`/stats/trend?days=${days}`),
  });
}

export function useModelDistribution(
  params: ModelDistributionParams,
  options?: { enabled?: boolean },
) {
  return useQuery({
    queryKey: ["model-distribution", params],
    queryFn: () => {
      const qs = new URLSearchParams({
        start: String(params.start),
        end: String(params.end),
        gran: params.gran,
      });
      if (params.model) qs.set("model", params.model);
      if (params.user_id) qs.set("user_id", String(params.user_id));
      if (params.top_n) qs.set("top_n", String(params.top_n));
      return api.get<ModelDistributionResponse>(`/stats/model-distribution?${qs.toString()}`);
    },
    staleTime: 5 * 60 * 1000,
    enabled: options?.enabled ?? true,
  });
}
