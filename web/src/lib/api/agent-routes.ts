import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, buildQuery } from "./client";
import type { AgentRoute, AgentRouteOverviewItem, PaginatedResponse, PaginatedParams } from "@/lib/types";

export function useAgentRoutes(
  params: PaginatedParams & { source_type?: string; source_id?: number } = {},
  options: { enabled?: boolean } = {},
) {
  return useQuery({
    queryKey: ["agent-routes", params],
    queryFn: () =>
      api.get<PaginatedResponse<AgentRoute>>(
        `/admin/agent-routes${buildQuery(params)}`
      ),
    enabled: options.enabled ?? true,
  });
}

export function useAgentRoutesOverview(params: PaginatedParams & { source_type?: string } = {}) {
  return useQuery({
    queryKey: ["agent-routes-overview", params],
    queryFn: () =>
      api.get<PaginatedResponse<AgentRouteOverviewItem>>(
        `/admin/agent-routes/overview${buildQuery(params)}`
      ),
  });
}

export function useCreateAgentRoute() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: {
      source_type: string;
      source_id: number;
      model?: string;
      agent_id?: string;
      agent_tag?: string;
    }) => api.post<AgentRoute>("/admin/agent-routes", body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["agent-routes"] });
      queryClient.invalidateQueries({ queryKey: ["agent-routes-overview"] });
    },
  });
}

export function useUpdateAgentRoute() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: number } & Partial<AgentRoute>) =>
      api.put<AgentRoute>(`/admin/agent-routes/${id}`, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["agent-routes"] });
      queryClient.invalidateQueries({ queryKey: ["agent-routes-overview"] });
    },
  });
}

export function useDeleteAgentRoute() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete<void>(`/admin/agent-routes/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["agent-routes"] });
      queryClient.invalidateQueries({ queryKey: ["agent-routes-overview"] });
    },
  });
}
