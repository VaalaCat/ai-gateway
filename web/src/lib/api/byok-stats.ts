import { useQuery } from "@tanstack/react-query";

import { localDateRangeToUTCRange } from "@/lib/utils/date-range";

import { api, buildQuery } from "./client";

export interface BYOKOverview {
  requests: number;
  total_cost: number;
}

/**
 * useBYOKOverview — legacy aggregate (no time series).
 * Hits /stats/byok-overview, kept for backward compat with callers that only
 * need 2 numbers. New BYOK stats UI uses {@link useBYOKBillingOverview}.
 */
export function useBYOKOverview() {
  return useQuery({
    queryKey: ["byok-overview"],
    queryFn: () => api.get<BYOKOverview>("/stats/byok-overview"),
  });
}

export interface BillingDailySeriesItem {
  date: string;
  request_count: number;
  success_count: number;
  failed_count: number;
  prompt_tokens: number;
  completion_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  input_cost: number;
  output_cost: number;
  total_cost: number;
}

export interface BillingOverviewResponse {
  total_requests: number;
  total_success: number;
  total_failed: number;
  total_cost: number;
  total_tokens: number;
  total_prompt_tokens: number;
  total_completion_tokens: number;
  total_cache_read_tokens: number;
  total_cache_write_tokens: number;
  success_rate: number;
  daily_series: BillingDailySeriesItem[];
}

export interface ByChannelItem {
  private_channel_id: number;
  channel_name: string;
  channel_type: number;
  request_count: number;
  success_count: number;
  failed_count: number;
  success_rate: number;
  total_tokens: number;
  total_cost: number;
  prompt_tokens: number;
  completion_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  input_cost: number;
  output_cost: number;
}

export interface ByChannelResponse {
  items: ByChannelItem[];
}

export interface ByModelItem {
  model_name: string;
  request_count: number;
  success_count: number;
  failed_count: number;
  success_rate: number;
  total_tokens: number;
  total_cost: number;
  prompt_tokens: number;
  completion_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  input_cost: number;
  output_cost: number;
}

export interface ByModelResponse {
  items: ByModelItem[];
}

interface RangeQuery {
  from?: string;
  to?: string;
}

interface BYOKBillingQueryOptions {
  enabled?: boolean;
}

function rangeKey(range: RangeQuery): readonly unknown[] {
  return [range.from ?? "", range.to ?? ""];
}

export function useBYOKBillingOverview(
  range: RangeQuery,
  options: BYOKBillingQueryOptions = {},
) {
  return useQuery({
    queryKey: ["byok-billing-overview", ...rangeKey(range)],
    queryFn: () => {
      const utc = localDateRangeToUTCRange(range.from ?? "", range.to ?? "");
      return api.get<BillingOverviewResponse>(
        `/private-channels/billing/overview${buildQuery(utc)}`,
      );
    },
    enabled: options.enabled ?? true,
  });
}

export function useBYOKBillingByChannel(
  range: RangeQuery,
  options: BYOKBillingQueryOptions = {},
) {
  return useQuery({
    queryKey: ["byok-billing-by-channel", ...rangeKey(range)],
    queryFn: () => {
      const utc = localDateRangeToUTCRange(range.from ?? "", range.to ?? "");
      return api.get<ByChannelResponse>(
        `/private-channels/billing/by-channel${buildQuery(utc)}`,
      );
    },
    enabled: options.enabled ?? true,
  });
}

export function useBYOKBillingByModel(
  range: RangeQuery,
  options: BYOKBillingQueryOptions = {},
) {
  return useQuery({
    queryKey: ["byok-billing-by-model", ...rangeKey(range)],
    queryFn: () => {
      const utc = localDateRangeToUTCRange(range.from ?? "", range.to ?? "");
      return api.get<ByModelResponse>(
        `/private-channels/billing/by-model${buildQuery(utc)}`,
      );
    },
    enabled: options.enabled ?? true,
  });
}
