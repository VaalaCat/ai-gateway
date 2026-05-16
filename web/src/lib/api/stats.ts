import { useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type { Stats, TrendItem } from "@/lib/types";

export function useStats() {
  return useQuery({
    queryKey: ["stats"],
    queryFn: () => api.get<Stats>("/stats/overview"),
  });
}

export function useStatsTrend(days: number = 30) {
  return useQuery({
    queryKey: ["stats-trend", days],
    queryFn: () => api.get<{ items: TrendItem[] }>(`/stats/trend?days=${days}`),
  });
}
