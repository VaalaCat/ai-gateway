import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "./client";
import type {
  SystemStatsResponse,
  CleanupPreviewResponse,
  CleanupResponse,
  RetryLogQueueResponse,
  ClearLogBacklogResponse,
  RetryHistoryBackfillResponse,
  SkipHistoryBackfillResponse,
  CompleteHistoryBackfillResponse,
  DeleteLegacyFileResponse,
} from "@/lib/types";

export interface SettingsResponse {
  settings: Record<string, string>;
}

export type AgentRelaySettingsPatch = Partial<{
  "agent.relay_default_uri": string;
  "agent.connectivity_probe_success_ttl_seconds": string;
  "agent.connectivity_probe_failure_retry_min_seconds": string;
  "agent.connectivity_probe_failure_retry_max_seconds": string;
}>;

export function useSystemStats() {
  return useQuery({
    queryKey: ["system-stats"],
    queryFn: () => api.get<SystemStatsResponse>("/admin/system/stats"),
  });
}

export function useRetryLogQueue() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<RetryLogQueueResponse>("/admin/system/log-queue/retry", {}),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["system-stats"] }),
  });
}

export function useClearLogBacklog() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ confirm }: { confirm: true }) =>
      api.delete<ClearLogBacklogResponse>(`/admin/system/log-queue/backlog?confirm=${confirm}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["system-stats"] }),
  });
}

export function useRetryHistoryBackfill() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<RetryHistoryBackfillResponse>("/admin/system/history-backfill/retry", {}),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["system-stats"] }),
  });
}

export function useSkipHistoryBackfill() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ confirm }: { confirm: true }) =>
      api.post<SkipHistoryBackfillResponse>(`/admin/system/history-backfill/skip?confirm=${confirm}`, {}),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["system-stats"] }),
  });
}

export function useCompleteHistoryBackfill() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { confirm: true }) =>
      api.post<CompleteHistoryBackfillResponse>("/admin/system/history-backfill/complete", body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["system-stats"] }),
  });
}

export function useDeleteLegacySource() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ confirmation }: { confirmation: "DELETE" }) =>
      api.delete<DeleteLegacyFileResponse>(`/admin/system/history-backfill/source?confirmation=${confirmation}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["system-stats"] }),
  });
}

export function useDeleteLegacyArtifact() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ confirmation }: { confirmation: "DELETE" }) =>
      api.delete<DeleteLegacyFileResponse>(`/admin/system/history-backfill/legacy-artifact?confirmation=${confirmation}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["system-stats"] }),
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
    mutationFn: (body: { target: string; retain_days: number; cutoff_unix: number }) =>
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

export function useUpdateAgentRelaySettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { settings: AgentRelaySettingsPatch }) =>
      api.put<SettingsResponse>("/admin/system/settings", body),
    onSuccess: (data) => {
      qc.setQueryData(["system-settings"], data);
      qc.invalidateQueries({ queryKey: ["system-settings"] });
    },
  });
}

export interface PublicConfig {
  registration_enabled: boolean;
  invite_enabled: boolean;
  invite_user_max_codes: number;
}

export function usePublicConfig() {
  return useQuery({
    queryKey: ["public-config"],
    queryFn: () => api.get<PublicConfig>("/system/public-config"),
  });
}
