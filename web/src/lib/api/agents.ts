import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, buildQuery } from "./client";
import type { Agent, AgentDetail, OnlineAgentInfo, ConnectivityReport, PaginatedResponse, PaginatedParams } from "@/lib/types";

export function useAgents(params: PaginatedParams = {}) {
  return useQuery({
    queryKey: ["agents", params],
    queryFn: () => api.get<PaginatedResponse<Agent>>(`/admin/agents${buildQuery(params)}`),
  });
}

export function useAgent(id: number) {
  return useQuery({
    queryKey: ["agents", id],
    queryFn: () => api.get<Agent>(`/admin/agents/${id}`),
    enabled: !!id,
  });
}

export function useAgentDetail(id: number) {
  return useQuery({
    queryKey: ["agents", id, "detail"],
    queryFn: () => api.get<AgentDetail>(`/admin/agents/${id}/detail`),
    enabled: !!id,
  });
}

export function useOnlineAgents(options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: ["agents", "online"],
    queryFn: () => api.get<OnlineAgentInfo[]>("/admin/agents/online"),
    refetchInterval: 30000,
    enabled: options.enabled ?? true,
  });
}

export function useConnectivityReport(id: number) {
  return useQuery({
    queryKey: ["agents", id, "connectivity"],
    queryFn: () => api.get<ConnectivityReport>(`/admin/agents/${id}/connectivity`),
    enabled: !!id,
  });
}

export function useCheckConnectivity() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) =>
      api.post<ConnectivityReport>(`/admin/agents/${id}/connectivity`, {}),
    onSuccess: (_, id) => {
      queryClient.invalidateQueries({ queryKey: ["agents", id, "connectivity"] });
    },
  });
}

export function useCreateAgent() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<Agent>) =>
      api.post<Agent>("/admin/agents", body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["agents"] });
    },
  });
}

export function useUpdateAgent() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: number } & Partial<Agent>) =>
      api.put<Agent>(`/admin/agents/${id}`, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["agents"] });
    },
  });
}

export function useDeleteAgent() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete<void>(`/admin/agents/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["agents"] });
    },
  });
}

export function useGenerateEnrollmentToken() {
  return useMutation({
    mutationFn: (body: { ttl: number }) =>
      api.post<{ enrollment_token: string; expires_at: number }>("/admin/agents/enrollment-token", body),
  });
}

export function useFullSyncAgents() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { agent_ids?: string[]; all?: boolean }) =>
      api.post<{
        results: Array<{
          agent_id: string;
          success: boolean;
          version?: number;
          duration_ms?: number;
          error?: string;
        }>;
      }>("/admin/agents/full-sync", body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["agents"] });
    },
  });
}
