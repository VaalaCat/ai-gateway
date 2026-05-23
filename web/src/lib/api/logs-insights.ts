import { useQuery } from "@tanstack/react-query";
import { api } from "./client";

export interface LogsTotals {
  total: number;
  failed: number;
  p95_ms: number;
  slowest_ms: number;
  spark_total: number[];
  spark_failed: number[];
  spark_p95: number[];
}

export interface ErrBucket {
  id?: number;
  stage?: string;
  name?: string;
  count: number;
  ratio: number;
}

export interface LogsInsightsResponse {
  totals: LogsTotals;
  error_by_stage: ErrBucket[];
}

export function useLogsInsights(
  params: { start: number; end: number },
  options?: { enabled?: boolean; refetchKey?: number },
) {
  return useQuery({
    queryKey: ["logs-insights", params, options?.refetchKey ?? 0],
    queryFn: () =>
      api.get<LogsInsightsResponse>(
        `/logs/insights?start=${params.start}&end=${params.end}`,
      ),
    staleTime: 5 * 60 * 1000,
    enabled: options?.enabled ?? true,
  });
}
