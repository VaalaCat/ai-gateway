import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, buildQuery } from "./client";
import type { AdminScript, PaginatedResponse, PaginatedParams } from "@/lib/types";

export function useScripts(params: PaginatedParams = {}) {
  return useQuery({
    queryKey: ["scripts", params],
    queryFn: () => api.get<PaginatedResponse<AdminScript>>(`/admin/scripts${buildQuery(params)}`),
  });
}

export function useScript(id: number) {
  return useQuery({
    queryKey: ["scripts", id],
    queryFn: () => api.get<AdminScript>(`/admin/scripts/${id}`),
    enabled: !!id,
  });
}

export function useCreateScript() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<AdminScript>) => api.post<AdminScript>("/admin/scripts", body),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["scripts"] }),
  });
}

export function useUpdateScript() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: number } & Partial<AdminScript>) =>
      api.put<AdminScript>(`/admin/scripts/${id}`, body),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["scripts"] }),
  });
}

export function useDeleteScript() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete<{ status: string }>(`/admin/scripts/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["scripts"] }),
  });
}
