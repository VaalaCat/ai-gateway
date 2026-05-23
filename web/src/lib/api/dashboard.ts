import { useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type { ObsRangeParams, TimeBucket } from "@/lib/types/observability";

export type { ObsRangeParams, TimeBucket };

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
}

export interface DashboardResponse {
  kpis: KpiBundle;
  trend: { buckets: TimeBucket[]; metrics: string[] };
  model_distribution?: Bucket[];
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

// ----- Hook -----

export function useDashboard(
  params: ObsRangeParams,
  options?: { enabled?: boolean; refetchKey?: number },
) {
  return useQuery({
    queryKey: ["dashboard", params, options?.refetchKey ?? 0],
    queryFn: () =>
      api.get<DashboardResponse>(
        `/stats/dashboard?start=${params.start}&end=${params.end}&gran=${params.gran}`,
      ),
    staleTime: 5 * 60 * 1000,
    enabled: options?.enabled ?? true,
  });
}
