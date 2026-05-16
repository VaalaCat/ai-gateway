import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "./client";
import type {
  SystemStatsResponse,
  CleanupPreviewResponse,
  CleanupResponse,
} from "@/lib/types";

export interface SettingsResponse {
  settings: Record<string, string>;
}

export function useSystemStats() {
  return useQuery({
    queryKey: ["system-stats"],
    queryFn: () => api.get<SystemStatsResponse>("/admin/system/stats"),
  });
}

export function useCleanupPreview(
  target: string,
  retainDays: number,
  enabled: boolean
) {
  return useQuery({
    queryKey: ["cleanup-preview", target, retainDays],
    queryFn: () =>
      api.get<CleanupPreviewResponse>(
        `/admin/system/cleanup/preview?target=${target}&retain_days=${retainDays}`
      ),
    enabled,
  });
}

export function useCleanup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { target: string; retain_days: number }) =>
      api.post<CleanupResponse>("/admin/system/cleanup", body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["system-stats"] });
      qc.invalidateQueries({ queryKey: ["cleanup-preview"] });
    },
  });
}

export function useSettings() {
  return useQuery({
    queryKey: ["system-settings"],
    queryFn: () => api.get<SettingsResponse>("/admin/system/settings"),
  });
}

export function useUpdateSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { settings: Record<string, string> }) =>
      api.put<SettingsResponse>("/admin/system/settings", body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["system-settings"] });
    },
  });
}
