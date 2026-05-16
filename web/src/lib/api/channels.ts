import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, buildQuery } from "./client";
import type { Channel, ChannelTypeMeta, ChannelTestResponse, ChannelTestParams, PaginatedResponse, PaginatedParams } from "@/lib/types";

interface QueryOptions {
  enabled?: boolean;
}

export function useChannels(params: PaginatedParams = {}, options: QueryOptions = {}) {
  return useQuery({
    queryKey: ["channels", params],
    queryFn: () => api.get<PaginatedResponse<Channel>>(`/admin/channels${buildQuery(params)}`),
    enabled: options.enabled ?? true,
  });
}

export function useChannelTypes(options: QueryOptions = {}) {
  return useQuery({
    queryKey: ["channel-types"],
    queryFn: () => api.get<ChannelTypeMeta[]>("/admin/channels/types"),
    enabled: options.enabled ?? true,
  });
}

export function useChannel(id: number) {
  return useQuery({
    queryKey: ["channels", id],
    queryFn: () => api.get<Channel>(`/admin/channels/${id}`),
    enabled: !!id,
  });
}

export function useCreateChannel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<Channel>) =>
      api.post<Channel>("/admin/channels", body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["channels"] });
    },
  });
}

export function useUpdateChannel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: number } & Partial<Channel>) =>
      api.put<Channel>(`/admin/channels/${id}`, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["channels"] });
    },
  });
}

export function useFetchUpstreamModels() {
  return useMutation({
    mutationFn: (body: { base_url: string; key: string; type: number; endpoints?: string; proxy_url?: string; agent_id?: string }) =>
      api.post<{ models: string[]; error?: string }>("/admin/channels/fetch-models", body),
  });
}

export function useTestChannel() {
  return useMutation({
    mutationFn: ({ id, ...body }: ChannelTestParams) =>
      api.post<ChannelTestResponse>(`/admin/channels/${id}/test`, body),
  });
}

export function useDeleteChannel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete<void>(`/admin/channels/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["channels"] });
    },
  });
}
