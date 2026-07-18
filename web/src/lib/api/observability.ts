import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./client";
import type {
  AllInflightResponse,
  BreakerBoardResponse,
  DeliveryBoardResponse,
  DeliveryOpRequest,
  DeliveryOpResponse,
  LimiterUsageResponse,
  RecentHealthResponse,
} from "@/lib/types";
import { useSettings } from "./system";

export function useAllInflight() {
  return useQuery({
    queryKey: ["observability", "inflight-all"],
    queryFn: () => api.get<AllInflightResponse>("/admin/agents/inflight/all"),
    refetchInterval: 5000,
  });
}

export function useLimiterUsage() {
  return useQuery({
    queryKey: ["observability", "limiter-usage"],
    queryFn: () =>
      api.get<LimiterUsageResponse>("/admin/observability/limiter-usage"),
    refetchInterval: 5000,
  });
}

export function useBreakerBoard() {
  return useQuery({
    queryKey: ["observability", "breaker-board"],
    queryFn: () =>
      api.get<BreakerBoardResponse>("/admin/observability/breaker-board"),
    refetchInterval: 5000,
  });
}

export function useDeliveryBoard() {
  return useQuery({
    queryKey: ["observability", "delivery-board"],
    queryFn: () =>
      api.get<DeliveryBoardResponse>("/admin/observability/delivery-board"),
    refetchInterval: 5000,
  });
}

export function useDeliveryOp() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: DeliveryOpRequest) =>
      api.post<DeliveryOpResponse>("/admin/observability/delivery-op", req),
    onSettled: () => qc.invalidateQueries({ queryKey: ["observability", "delivery-board"] }),
  });
}

export function useRecentHealth() {
  return useQuery({
    queryKey: ["observability", "recent-health"],
    queryFn: () =>
      api.get<RecentHealthResponse>("/admin/observability/recent-health"),
    refetchInterval: 30000,
  });
}

/** 集群健康阈值，全部来自后台 Settings（百分比整数 / 秒），缺省回退到默认值。 */
export interface HealthThresholds {
  errYellowPct: number;
  errRedPct: number;
  satYellowPct: number;
  satRedPct: number;
  offlineSeconds: number;
}

const HEALTH_THRESHOLD_DEFAULTS: HealthThresholds = {
  errYellowPct: 2,
  errRedPct: 10,
  satYellowPct: 80,
  satRedPct: 95,
  offlineSeconds: 90,
};

/** 从 Settings 读取健康阈值，逐项 Number 解析并回退默认。 */
export function useHealthThresholds(): HealthThresholds {
  const { data } = useSettings();
  const s = data?.settings ?? {};
  const num = (key: string, fallback: number): number => {
    const v = Number(s[key]);
    return Number.isFinite(v) && s[key] !== undefined && s[key] !== ""
      ? v
      : fallback;
  };
  return {
    errYellowPct: num("health_error_rate_yellow_pct", HEALTH_THRESHOLD_DEFAULTS.errYellowPct),
    errRedPct: num("health_error_rate_red_pct", HEALTH_THRESHOLD_DEFAULTS.errRedPct),
    satYellowPct: num("health_saturation_yellow_pct", HEALTH_THRESHOLD_DEFAULTS.satYellowPct),
    satRedPct: num("health_saturation_red_pct", HEALTH_THRESHOLD_DEFAULTS.satRedPct),
    offlineSeconds: num("health_offline_seconds", HEALTH_THRESHOLD_DEFAULTS.offlineSeconds),
  };
}
