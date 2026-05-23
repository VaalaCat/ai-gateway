import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import type { UseQueryOptions } from "@tanstack/react-query";
import { api, buildQuery } from "./client";
import type { User, PaginatedResponse, PaginatedParams } from "@/lib/types";

export function useUsers(
  params: PaginatedParams & { search?: string; role?: string; group_id?: number } = {},
  options?: Omit<UseQueryOptions<PaginatedResponse<User>>, "queryKey" | "queryFn">,
) {
  return useQuery({
    queryKey: ["users", params],
    queryFn: () => api.get<PaginatedResponse<User>>(`/admin/users${buildQuery(params)}`),
    ...options,
  });
}

export function useUser(id: number) {
  return useQuery({
    queryKey: ["users", id],
    queryFn: () => api.get<User>(`/admin/users/${id}`),
    enabled: !!id,
  });
}

export function useCreateUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { username: string; password: string; role?: number; group_id?: number }) =>
      api.post<User>("/admin/users", body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
    },
  });
}

export function useUpdateUser() {
  const queryClient = useQueryClient();
  return useMutation({
    // body may include `group_id` (forwarded to user.update via Partial<User>)
    mutationFn: ({ id, ...body }: { id: number } & Partial<User>) =>
      api.put<User>(`/admin/users/${id}`, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      queryClient.invalidateQueries({ queryKey: ["profile"] });
    },
  });
}

export function useDeleteUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete<void>(`/admin/users/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
    },
  });
}

export function useUpdateQuota() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, delta }: { id: number; delta: number }) =>
      api.put<User>(`/admin/users/${id}/quota`, { delta }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
    },
  });
}

export function useProfile() {
  return useQuery({
    queryKey: ["profile"],
    queryFn: () => api.get<User>("/profile"),
  });
}

export function useChangePassword() {
  return useMutation({
    mutationFn: (body: { old_password: string; new_password: string }) =>
      api.put<{ status: string }>("/profile/password", body),
  });
}

export function useUpdateProfile() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { email?: string; display_name?: string; avatar_url?: string }) =>
      api.put<User>("/profile", body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["profile"] });
      queryClient.invalidateQueries({ queryKey: ["users"] });
    },
  });
}
