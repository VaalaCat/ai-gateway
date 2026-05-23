import { useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type { ObsRangeParams } from "./dashboard";
import type { ErrBucket } from "./logs-insights";

export interface KpiRing {
  ratio: number;
  value: number | string;
  sub: string;
  warn_above?: number;
}

export interface KpiRings {
  success: KpiRing;
  cache: KpiRing;
  agents: KpiRing;
  tps: KpiRing;
  error: KpiRing;
}

export interface ChannelMetric {
  id: number;
  name: string;
  requests: number;
  error_ratio: number;
  ttft_p95_ms: number;
  tps_avg: number;
  latency_p95_ms: number;
  spark_24h: number[];
}

export interface AgentMetric {
  id: string;
  name: string;
  online: boolean;
  last_seen: number;
  requests: number;
  ttft_p95_ms: number;
  tps_avg: number;
  latency_p95_ms: number;
  spark_24h: number[];
}

export interface MonitoringInsightsResponse {
  kpi_rings: KpiRings;
  channels: ChannelMetric[];
  agents: AgentMetric[];
  errors: {
    by_stage: ErrBucket[];
    by_channel: ErrBucket[];
  };
}

export function useMonitoringInsights(
  params: ObsRangeParams,
  options?: { enabled?: boolean; refetchKey?: number },
) {
  return useQuery({
    queryKey: ["monitoring-insights", params, options?.refetchKey ?? 0],
    queryFn: () =>
      api.get<MonitoringInsightsResponse>(
        `/admin/monitoring/insights?start=${params.start}&end=${params.end}&gran=${params.gran}`,
      ),
    staleTime: 5 * 60 * 1000,
    enabled: options?.enabled ?? true,
  });
}
