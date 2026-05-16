import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, buildQuery } from "./client";
import type {
  BillingChannelDailyQueryParams,
  BillingChannelDailyRow,
  BillingChannelQueryParams,
  BillingChannelRow,
  BillingDailyResponse,
  BillingOverviewQueryParams,
  BillingOverviewResponse,
  BillingRebuildRequest,
  BillingRebuildResponse,
  BillingTokenDailyQueryParams,
  BillingTokenDailyRow,
  BillingTokenQueryParams,
  BillingTokenRow,
  PaginatedResponse,
} from "@/lib/types";

interface BillingQueryOptions {
  enabled?: boolean;
}

export function useBillingOverview(
  params: BillingOverviewQueryParams = {},
  options: BillingQueryOptions = {}
) {
  return useQuery({
    queryKey: ["billing-overview", params],
    queryFn: () => api.get<BillingOverviewResponse>(`/billing/overview${buildQuery(params)}`),
    enabled: options.enabled ?? true,
  });
}

export function useTokenBilling(
  params: BillingTokenQueryParams = {},
  options: BillingQueryOptions = {}
) {
  return useQuery({
    queryKey: ["billing-token-list", params],
    queryFn: () => api.get<PaginatedResponse<BillingTokenRow>>(`/billing/tokens${buildQuery(params)}`),
    enabled: options.enabled ?? true,
  });
}

export function useTokenBillingDaily(
  tokenId: number | null,
  params: BillingTokenDailyQueryParams = {},
  options: BillingQueryOptions = {}
) {
  return useQuery({
    queryKey: ["billing-token-daily", tokenId, params],
    queryFn: () =>
      api.get<BillingDailyResponse<BillingTokenDailyRow>>(`/billing/tokens/${tokenId}/daily${buildQuery(params)}`),
    enabled: (options.enabled ?? true) && tokenId != null,
  });
}

export function useChannelBilling(
  params: BillingChannelQueryParams = {},
  options: BillingQueryOptions = {}
) {
  return useQuery({
    queryKey: ["billing-channel-list", params],
    queryFn: () =>
      api.get<PaginatedResponse<BillingChannelRow>>(`/admin/billing/channels${buildQuery(params)}`),
    enabled: options.enabled ?? true,
  });
}

export function useChannelBillingDaily(
  channelId: number | null,
  params: BillingChannelDailyQueryParams = {},
  options: BillingQueryOptions = {}
) {
  return useQuery({
    queryKey: ["billing-channel-daily", channelId, params],
    queryFn: () =>
      api.get<BillingDailyResponse<BillingChannelDailyRow>>(
        `/admin/billing/channels/${channelId}/daily${buildQuery(params)}`
      ),
    enabled: (options.enabled ?? true) && channelId != null,
  });
}

export function useRebuildBilling() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: BillingRebuildRequest) =>
      api.post<BillingRebuildResponse>("/admin/billing/rebuild", body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["billing-overview"] });
      qc.invalidateQueries({ queryKey: ["billing-token-list"] });
      qc.invalidateQueries({ queryKey: ["billing-channel-list"] });
    },
  });
}
