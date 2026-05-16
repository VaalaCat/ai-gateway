import { useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type { CacheStatsResponse } from "@/lib/types";

export function useCacheStats() {
  return useQuery({
    queryKey: ["cache-stats"],
    queryFn: () => api.get<CacheStatsResponse>("/admin/cache/stats"),
    refetchInterval: 15000,
    staleTime: 5000,
  });
}
