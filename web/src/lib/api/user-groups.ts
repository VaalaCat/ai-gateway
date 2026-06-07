import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, buildQuery } from "./client";
import type { PaginatedResponse, UserGroup } from "../types";

export const DEFAULT_GROUP_ID = 1;

interface ListParams {
  page?: number;
  pageSize?: number;
  search?: string;
  status?: string;
}

export function useUserGroups(params: ListParams = {}) {
  const query = buildQuery({
    page: params.page,
    page_size: params.pageSize,
    search: params.search,
    status: params.status,
  });
  return useQuery({
    queryKey: ["user-groups", params],
    queryFn: () => api.get<PaginatedResponse<UserGroup>>(`/admin/user-groups${query}`),
  });
}

export function useUserGroup(id: number) {
  return useQuery({
    queryKey: ["user-group", id],
    queryFn: () => api.get<UserGroup>(`/admin/user-groups/${id}`),
    enabled: !!id,
  });
}

export function useCreateUserGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<UserGroup>) =>
      api.post<UserGroup>("/admin/user-groups", body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["user-groups"] }),
  });
}

export function useUpdateUserGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...data }: { id: number } & Record<string, unknown>) =>
      api.put<UserGroup>(`/admin/user-groups/${id}`, data),
    onSuccess: (_d, vars) => {
      qc.invalidateQueries({ queryKey: ["user-groups"] });
      qc.invalidateQueries({ queryKey: ["user-group", vars.id] });
    },
  });
}

export function useDeleteUserGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete<void>(`/admin/user-groups/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["user-groups"] }),
  });
}
