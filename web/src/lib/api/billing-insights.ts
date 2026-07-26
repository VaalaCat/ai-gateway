import { useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type { ChartTopN } from "@/lib/types";
import type { ObsGranularity } from "@/lib/types/observability";
import type { StackedBucket, TimeBucket } from "@/lib/types/observability";

export type { StackedBucket };

export interface CacheSaving {
  hit_ratio: number;
  saved_tokens: number;
  saved_cost: number;
  vs_label: string;
  read_tokens?: number;
  write_tokens?: number;
}

export interface BillingInsightsResponse {
  trend: TimeBucket[];
  cost_trend_stacked: {
    buckets: StackedBucket[];
    series_order: string[];
  };
  cache_saving: CacheSaving;
}

export type BillingStackBy = "model" | "user" | "channel";

export interface BillingInsightsParams {
  from: number;
  to: number;
  gran: ObsGranularity;
  token_id?: number;
  top_n?: ChartTopN;
  stack?: BillingStackBy;
  model?: string;
  user_id?: number;
}

export function useBillingInsights(
  params: BillingInsightsParams,
  options?: { enabled?: boolean; refetchKey?: number },
) {
  return useQuery({
    queryKey: ["billing-insights", params, options?.refetchKey ?? 0],
    queryFn: () => {
      const qs = new URLSearchParams({
        start: String(params.from),
        end: String(params.to),
        gran: params.gran,
      });
      if (params.stack) qs.set("stack", params.stack);
      if (params.model) qs.set("model", params.model);
      if (params.user_id) qs.set("user_id", String(params.user_id));
      if (params.token_id) qs.set("token_id", String(params.token_id));
      if (params.top_n) qs.set("top_n", String(params.top_n));
      return api.get<BillingInsightsResponse>(
        `/billing/insights?${qs.toString()}`,
      );
    },
    staleTime: 5 * 60 * 1000,
    enabled: options?.enabled ?? true,
  });
}
