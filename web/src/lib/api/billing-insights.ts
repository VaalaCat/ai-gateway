import { useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type { ObsRangeParams } from "./dashboard";
import type { StackedBucket } from "@/lib/types/observability";

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
  cost_trend_stacked: {
    buckets: StackedBucket[];
    series_order: string[];
  };
  cache_saving: CacheSaving;
}

export type BillingStackBy = "model" | "user" | "channel";

export function useBillingInsights(
  params: ObsRangeParams & { stack?: BillingStackBy },
  options?: { enabled?: boolean; refetchKey?: number },
) {
  return useQuery({
    queryKey: ["billing-insights", params, options?.refetchKey ?? 0],
    queryFn: () => {
      const qs = new URLSearchParams({
        start: String(params.start),
        end: String(params.end),
        gran: params.gran,
      });
      if (params.stack) qs.set("stack", params.stack);
      return api.get<BillingInsightsResponse>(
        `/billing/insights?${qs.toString()}`,
      );
    },
    staleTime: 5 * 60 * 1000,
    enabled: options?.enabled ?? true,
  });
}
