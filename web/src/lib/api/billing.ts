import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { localDateRangeToUTCRange } from "@/lib/utils/date-range";
import type {
  BillingChannelDailyQueryParams,
  BillingChannelDailyRow,
  BillingChannelQueryParams,
  BillingChannelRow,
  BillingDailyResponse,
  BillingOverviewQueryParams,
  BillingOverviewResponse,
  BillingRebuildRequest,
  BillingRebuildSubmitResponse,
  ChannelModelBreakdownResponse,
  RebuildJob,
  RebuildJobListResponse,
  BillingTokenDailyQueryParams,
  BillingTokenDailyRow,
  BillingTokenQueryParams,
  BillingTokenRow,
  PaginatedResponse,
} from "@/lib/types";

import { api, buildQuery } from "./client";

interface BillingQueryOptions {
  enabled?: boolean;
}

export function useBillingOverview(
  params: BillingOverviewQueryParams = {},
  options: BillingQueryOptions = {}
) {
  return useQuery({
    queryKey: ["billing-overview", params],
    queryFn: () => {
      const utc = localDateRangeToUTCRange(params.start_date ?? "", params.end_date ?? "");
      return api.get<BillingOverviewResponse>(
        `/billing/overview${buildQuery({
          ...params,
          start_date: utc.from,
          end_date: utc.to,
        })}`,
      );
    },
    enabled: options.enabled ?? true,
  });
}

export function useTokenBilling(
  params: BillingTokenQueryParams = {},
  options: BillingQueryOptions = {}
) {
  return useQuery({
    queryKey: ["billing-token-list", params],
    queryFn: () => {
      const utc = localDateRangeToUTCRange(params.start_date ?? "", params.end_date ?? "");
      return api.get<PaginatedResponse<BillingTokenRow>>(
        `/billing/tokens${buildQuery({
          ...params,
          start_date: utc.from,
          end_date: utc.to,
        })}`,
      );
    },
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
    queryFn: () => {
      const utc = localDateRangeToUTCRange(params.start_date ?? "", params.end_date ?? "");
      return api.get<BillingDailyResponse<BillingTokenDailyRow>>(
        `/billing/tokens/${tokenId}/daily${buildQuery({
          ...params,
          start_date: utc.from,
          end_date: utc.to,
        })}`,
      );
    },
    enabled: (options.enabled ?? true) && tokenId != null,
  });
}

export function useChannelBilling(
  params: BillingChannelQueryParams = {},
  options: BillingQueryOptions = {}
) {
  return useQuery({
    queryKey: ["billing-channel-list", params],
    queryFn: () => {
      const utc = localDateRangeToUTCRange(params.start_date ?? "", params.end_date ?? "");
      return api.get<PaginatedResponse<BillingChannelRow>>(
        `/admin/billing/channels${buildQuery({
          ...params,
          start_date: utc.from,
          end_date: utc.to,
        })}`,
      );
    },
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
    queryFn: () => {
      const utc = localDateRangeToUTCRange(params.start_date ?? "", params.end_date ?? "");
      return api.get<BillingDailyResponse<BillingChannelDailyRow>>(
        `/admin/billing/channels/${channelId}/daily${buildQuery({
          ...params,
          start_date: utc.from,
          end_date: utc.to,
        })}`
      );
    },
    enabled: (options.enabled ?? true) && channelId != null,
  });
}

// useChannelModelBreakdown: 单渠道按 model_name 分组的用量/计费细分(billed+raw),给
// Billing channel tab 行展开用。start/end 为 unix 秒(同 ObsRange 约定,非 date string,
// 端点自己转换,别再套 localDateRangeToUTCRange)。channelId 为 undefined 时不发请求
// (展开态才拉,折叠行不占带宽)。
export function useChannelModelBreakdown(
  channelId: number | undefined,
  start: number,
  end: number,
) {
  return useQuery({
    queryKey: ["billing-channel-model-breakdown", channelId, start, end],
    queryFn: () =>
      api.get<ChannelModelBreakdownResponse>(
        `/stats/channel-model-breakdown${buildQuery({
          channel_id: channelId,
          start,
          end,
        })}`,
      ),
    enabled: channelId !== undefined,
    staleTime: 60_000,
  });
}

// useRebuildBillingSubmit kicks off an async rebuild job. The mutation
// resolves with { job_id, total_slices }; callers should then poll
// useRebuildBillingJob(jobId) and invalidate billing caches on terminal status.
export function useRebuildBillingSubmit() {
  return useMutation({
    mutationFn: (body: BillingRebuildRequest) =>
      api.post<BillingRebuildSubmitResponse>("/admin/billing/rebuild", body),
  });
}

// useRebuildBillingJob polls a single job; auto-stops refetch on terminal status.
// Pass `null` to disable (e.g. before a job has been created).
export function useRebuildBillingJob(jobId: string | null) {
  return useQuery({
    queryKey: ["billing-rebuild-job", jobId],
    enabled: !!jobId,
    refetchInterval: (q) => {
      const data = q.state.data as RebuildJob | undefined;
      if (!data) return 2000;
      return data.status === "running" ? 2000 : false;
    },
    queryFn: () =>
      api.get<RebuildJob>(`/admin/billing/rebuild/jobs/${jobId}`),
  });
}

// useRebuildBillingJobs polls the global jobs list, used by the entry button
// to surface in-progress jobs after the dialog has been closed. Polls every
// 2s while any job is running; backs off to 10s when idle so we don't hammer
// the admin endpoint when nothing's happening.
export function useRebuildBillingJobs(options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: ["billing-rebuild-jobs"],
    enabled: options.enabled ?? true,
    refetchInterval: (q) => {
      const data = q.state.data as RebuildJobListResponse | undefined;
      const anyRunning = data?.jobs?.some((j) => j.status === "running");
      return anyRunning ? 2000 : 10000;
    },
    queryFn: () =>
      api.get<RebuildJobListResponse>("/admin/billing/rebuild/jobs"),
  });
}

// useInvalidateBillingOnRebuildComplete is a helper for callers that want to
// flush billing-related caches after a successful rebuild. Typically called
// once when polling sees status === "succeeded".
export function useInvalidateBillingCaches() {
  const qc = useQueryClient();
  return () => {
    qc.invalidateQueries({ queryKey: ["billing-overview"] });
    qc.invalidateQueries({ queryKey: ["billing-token-list"] });
    qc.invalidateQueries({ queryKey: ["billing-channel-list"] });
  };
}
