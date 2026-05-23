import { useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type { LeaderRow, ObsRangeParams, TimeBucket } from "./dashboard";

export type EntityType = "agent" | "channel" | "model" | "user" | "token";

export interface EntityMeta {
  id: string;
  name: string;
  online?: boolean;
  last_seen?: number;
  region?: string;
  version?: string;
  joined_at?: number;
}

export interface SummaryKpis {
  requests: number;
  cost: number;
  tokens: number;
  success_rate: number;
  ttft_p95_ms: number;
  tps_avg: number;
}

export interface StageP95 {
  name: string;
  p95_ms: number;
}

export interface StageLatency {
  stages: StageP95[];
}

export interface Breakdown {
  by_model?: LeaderRow[];
  by_channel?: LeaderRow[];
  by_agent?: LeaderRow[];
  by_user?: LeaderRow[];
  by_token?: LeaderRow[];
}

export interface ErrorSample {
  ts: number;
  stage?: string;
  channel?: string;
  model?: string;
  message: string;
}

export interface InsightResponse {
  meta: EntityMeta;
  summary: SummaryKpis;
  trend: { buckets: TimeBucket[]; metrics: string[] };
  stage_latency?: StageLatency;
  breakdown: Breakdown;
  errors: ErrorSample[];
}

export function useInsight(
  params: { type: EntityType; id: string } & ObsRangeParams,
  options?: { enabled?: boolean; refetchKey?: number },
) {
  return useQuery({
    queryKey: ["insight", params, options?.refetchKey ?? 0],
    queryFn: () =>
      api.get<InsightResponse>(
        `/admin/insights?type=${params.type}&id=${encodeURIComponent(params.id)}&start=${params.start}&end=${params.end}&gran=${params.gran}`,
      ),
    staleTime: 5 * 60 * 1000,
    enabled: options?.enabled ?? !!params.id,
  });
}
