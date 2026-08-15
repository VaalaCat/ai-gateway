import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import type { UseQueryOptions } from "@tanstack/react-query";
import { api, buildQuery } from "./client";
import type {
  PaginatedResponse,
  TokenTemplate,
  SyncPreviewResponse,
  TokenTemplateSyncResponse,
} from "../types";

export function useTokenTemplates(params: { page?: number; pageSize?: number; search?: string; status?: string } = {}) {
  const query = buildQuery({ page: params.page, page_size: params.pageSize, search: params.search, status: params.status });
  return useQuery({
    queryKey: ["token-templates", params],
    queryFn: () => api.get<PaginatedResponse<TokenTemplate>>(`/admin/token-templates${query}`),
  });
}

export function useEnabledTokenTemplates(
  options?: Omit<UseQueryOptions<PaginatedResponse<TokenTemplate>>, "queryKey" | "queryFn">,
) {
  return useQuery({
    queryKey: ["token-templates-enabled"],
    queryFn: () => api.get<PaginatedResponse<TokenTemplate>>(`/token-templates?page=1&page_size=100`),
    ...options,
  });
}

export function useCreateTokenTemplate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: Partial<TokenTemplate>) => api.post<TokenTemplate>("/admin/token-templates", data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["token-templates"] }),
  });
}

export function useUpdateTokenTemplate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...data }: { id: number } & Record<string, unknown>) =>
      api.put<TokenTemplate>(`/admin/token-templates/${id}`, data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["token-templates"] }),
  });
}

export function useDeleteTokenTemplate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete(`/admin/token-templates/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["token-templates"] }),
  });
}

export function usePreviewSyncTokenTemplate() {
  return useMutation({
    mutationFn: ({ id, fields }: { id: number; fields: string[] }) =>
      api.post<SyncPreviewResponse>(`/admin/token-templates/${id}/sync-preview`, { fields }),
  });
}

export function useSyncTokenTemplate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, fields }: { id: number; fields: string[] }) =>
      api.post<TokenTemplateSyncResponse>(`/admin/token-templates/${id}/sync`, { fields }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["token-templates"] });
      qc.invalidateQueries({ queryKey: ["tokens"] });
    },
  });
}
