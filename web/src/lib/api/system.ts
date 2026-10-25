import { useQuery, useMutation, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useSyncExternalStore } from "react";
import { api } from "./client";
import { isSafeSiteIconURL, SITE_BRANDING_DEFAULTS, type SiteBranding } from "@/lib/site-branding";
import type {
  SystemStatsResponse,
  CleanupBatchRequest,
  CleanupBatchResponse,
  CleanupTablePreview,
  CleanupTableRef,
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

export function previewCleanupTable(
  input: CleanupTableRef & { cutoff_date: string },
) {
  const query = new URLSearchParams({
    database: input.database,
    table: input.table,
    cutoff_date: input.cutoff_date,
  });
  return api.get<CleanupTablePreview>(`/admin/system/cleanup/preview?${query}`);
}

export function deleteCleanupTableBatch(input: CleanupBatchRequest) {
  return api.post<CleanupBatchResponse>("/admin/system/cleanup/batch", input);
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
    onSuccess: (data) => {
      qc.setQueryData(["system-settings"], data);
      qc.invalidateQueries({ queryKey: ["system-settings"] });
      qc.invalidateQueries({ queryKey: ["public-config"] });
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

export interface PublicConfig extends SiteBranding {
  registration_enabled: boolean;
  invite_enabled: boolean;
  invite_user_max_codes: number;
}

const PUBLIC_CONFIG_STORAGE_KEY = "ai-gateway:public-config:v1";

export function restorePublicConfig(queryClient: QueryClient) {
  if (queryClient.getQueryData(["public-config"])) return;
  try {
    const cached: unknown = JSON.parse(localStorage.getItem(PUBLIC_CONFIG_STORAGE_KEY) || "null");
    if (!cached || typeof cached !== "object") return;
    const config = cached as PublicConfig;
    if (!Object.keys(SITE_BRANDING_DEFAULTS).every((key) => typeof config[key as keyof SiteBranding] === "string")
      || !isSafeSiteIconURL(config.site_icon_url)
      || typeof config.registration_enabled !== "boolean"
      || typeof config.invite_enabled !== "boolean"
      || !Number.isFinite(config.invite_user_max_codes)) return;
    // A persisted snapshot is only a visual bootstrap, never a fresh response.
    queryClient.setQueryData(["public-config"], config, { updatedAt: 0 });
  } catch {
    // Storage can be disabled or contain an obsolete/corrupt snapshot.
  }
}

const subscribeHydration = () => () => {};

export function usePublicConfig() {
  const hydrated = useSyncExternalStore(subscribeHydration, () => true, () => false);
  const query = useQuery({
    queryKey: ["public-config"],
    queryFn: async () => {
      const config = await api.get<PublicConfig>("/system/public-config");
      try {
        localStorage.setItem(PUBLIC_CONFIG_STORAGE_KEY, JSON.stringify(config));
      } catch {
        // The in-memory query remains usable when browser storage is unavailable.
      }
      return config;
    },
    refetchOnMount: "always",
    refetchInterval: 30_000,
    refetchOnWindowFocus: "always",
  });
  // A parent effect may restore the cache before a nested Suspense boundary hydrates.
  // That boundary must still see the same placeholder as its server snapshot.
  return hydrated ? query : { ...query, data: undefined };
}
