import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import type { UseQueryOptions } from "@tanstack/react-query";
import { api, buildQuery } from "./client";
import type { Token, PaginatedResponse, PaginatedParams } from "@/lib/types";

export function useTokens(
  params: PaginatedParams & {
    user_id?: number;
    status?: number;
    search?: string;
  } = {},
  options?: Omit<UseQueryOptions<PaginatedResponse<Token>>, "queryKey" | "queryFn">,
) {
  return useQuery({
    queryKey: ["tokens", params],
    queryFn: () => api.get<PaginatedResponse<Token>>(`/tokens${buildQuery(params)}`),
    ...options,
  });
}

export function useToken(id: number) {
  return useQuery({
    queryKey: ["tokens", id],
    queryFn: () => api.get<Token>(`/tokens/${id}`),
    enabled: !!id,
  });
}

export function useCreateToken() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { user_id?: number; name: string; key?: string; expired_at?: number; models?: string; template_id?: number; trace_enabled?: boolean; allowed_channel_ids?: number[] }) =>
      api.post<Token>("/tokens", body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tokens"] });
    },
  });
}

export function useUpdateToken() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: number } & Partial<Token>) =>
      api.put<Token>(`/tokens/${id}`, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tokens"] });
      queryClient.invalidateQueries({ queryKey: ["available-models"] });
    },
  });
}

export function useDeleteToken() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete<void>(`/tokens/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tokens"] });
    },
  });
}
