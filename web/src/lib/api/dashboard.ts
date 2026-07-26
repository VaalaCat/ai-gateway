import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type {
  ObsGranularity,
  ObsRangeParams,
  TimeBucket,
  StackedBucket,
} from "@/lib/types/observability";
import type { ChartTopN, DataStatus, TrendStat } from "@/lib/types";

export type { ObsRangeParams, TimeBucket };
export type { ChartTopN, DataStatus, TrendStat };

// ----- KPI types -----

export interface KpiMetric {
  value: number;
  spark: number[];
  delta: number;
}

export interface KpiUsers {
  value: number;
  active: number;
  new: number;
}

export interface KpiQuota {
  quota: number;
  used_quota: number;
}

export interface KpiBundle {
  requests: KpiMetric;
  cost: KpiMetric;
  tokens: KpiMetric;
  users?: KpiUsers;
  success_rate?: KpiMetric;
  quota?: KpiQuota;
}

// ----- Trend / breakdown types -----

export interface Bucket {
  name: string;
  value: number;
  ratio: number;
}

export interface LeaderRow {
  id?: number;
  name: string;
  cost: number;
  requests: number;
  tokens: number;
  tps?: number;
  ttft_ms?: number;
}

export interface SpeedRow {
  id?: number;
  name: string;
  ttft_ms: number;
  tps: number;
  ttft_p95_ms?: number;
  tps_p5?: number;
}

export interface BillingTrendBucket {
  ts: number;
  label: string;
  cost: number;
  requests: number;
  tokens: number;
}

export interface PerformanceTrendBucket {
  ts: number;
  label: string;
  ttft_ms: number;
  tps: number;
  cache_hit_rate: number;
}

export interface LogMetrics {
  trend: { buckets: PerformanceTrendBucket[]; metrics: string[] };
  leaderboard?: {
    users: LeaderRow[];
    models: LeaderRow[];
    channels: LeaderRow[];
    available_metrics: string[];
  };
  speed_compare?: {
    by_model: SpeedRow[];
    by_channel: SpeedRow[];
  };
}

export interface DashboardResponse {
  kpis: KpiBundle;
  trend: { buckets: BillingTrendBucket[]; metrics: Array<"cost" | "requests" | "tokens"> };
  log_metrics: LogMetrics | null;
  data_status: DataStatus;
}

// ----- Hook -----

export function useDashboard(
  params: ObsRangeParams & { model?: string; user_id?: number },
  options?: { enabled?: boolean; refetchKey?: number },
) {
  return useQuery({
    queryKey: ["dashboard", params, options?.refetchKey ?? 0],
    queryFn: () => {
      const qs = new URLSearchParams({
        start: String(params.start),
        end: String(params.end),
        gran: params.gran,
      });
      if (params.model) qs.set("model", params.model);
      if (params.user_id) qs.set("user_id", String(params.user_id));
      return api.get<DashboardResponse>(`/stats/dashboard?${qs.toString()}`);
    },
    staleTime: 5 * 60 * 1000,
    enabled: options?.enabled ?? true,
  });
}

// ----- Market share (stacked bar by model/channel) -----

/** market-share 分组维度：author 本期不支持（见 dao.ErrUnsupportedMarketShareDim） */
export type MarketShareDim = "model" | "channel";

/** 单个时间桶按 series 名(model/channel)分组的 token 量，series 值语义与后端 StackedBucket 对齐 */
export type MarketShareBucket = StackedBucket;

export interface MarketShareResponse {
  buckets: MarketShareBucket[];
  series_order: string[];
}

export function useMarketShare(
  dim: MarketShareDim,
  start: number,
  end: number,
  options?: {
    gran?: ObsGranularity;
    model?: string;
    top_n?: ChartTopN;
    enabled?: boolean;
  },
) {
  const gran = options?.gran ?? "day";
  return useQuery({
    queryKey: ["market-share", dim, start, end, gran, options?.model, options?.top_n],
    queryFn: () => {
      const qs = new URLSearchParams({
        dim,
        start: String(start),
        end: String(end),
        gran,
      });
      if (options?.model) qs.set("model", options.model);
      if (options?.top_n) qs.set("top_n", String(options.top_n));
      return api.get<MarketShareResponse>(`/stats/market-share?${qs.toString()}`);
    },
    staleTime: 5 * 60 * 1000,
    // 切换 dim(模型/渠道)时保留上一份数据,避免图表整块闪成骨架屏。
    placeholderData: keepPreviousData,
    enabled: options?.enabled ?? true,
  });
}

// ----- Metric trend breakdown (multi-series by model/channel, admin-only) -----

/** MetricTrendChart 支持的当前指标;单一来源,组件从这里 re-export 复用 */
export type TrendMetric = "cost" | "requests" | "tokens" | "ttft" | "tps" | "cache_hit_rate";

export interface MetricTrendParams {
  metric: TrendMetric;
  stat?: TrendStat;
  top_n?: ChartTopN;
}

export interface MetricTrendBucket {
  ts: number;
  label: string;
  series: Record<string, number>;
}

export interface MetricTrendResponse {
  metric: TrendMetric;
  stat: string;
  unit: string;
  estimated: boolean;
  buckets: MetricTrendBucket[];
  series_order: string[];
}

/** 按 (metric, dim) 拉取 top-N + others 折叠后的多系列时间序列,dim 复用 MarketShareDim(model/channel) */
export function useMetricTrend(
  metric: TrendMetric,
  dim: MarketShareDim,
  start: number,
  end: number,
  options?: {
    gran?: ObsGranularity;
    stat?: TrendStat;
    top_n?: ChartTopN;
    model?: string;
    user_id?: number;
    enabled?: boolean;
  },
) {
  const gran = options?.gran ?? "day";
  return useQuery({
    queryKey: [
      "metric-trend", metric, dim, start, end, gran, options?.stat, options?.top_n,
      options?.model, options?.user_id,
    ],
    queryFn: () => {
      const qs = new URLSearchParams({
        metric,
        dim,
        start: String(start),
        end: String(end),
        gran,
      });
      if (options?.stat) qs.set("stat", options.stat);
      if (options?.top_n) qs.set("top_n", String(options.top_n));
      if (options?.model) qs.set("model", options.model);
      if (options?.user_id) qs.set("user_id", String(options.user_id));
      return api.get<MetricTrendResponse>(`/stats/metric-trend?${qs.toString()}`);
    },
    staleTime: 5 * 60 * 1000,
    enabled: options?.enabled ?? true,
  });
}
