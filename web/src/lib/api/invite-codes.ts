import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, buildQuery } from "./client";
import type { PaginatedResponse, InviteCodeRow } from "../types";

export function useInviteCodes(
  params: { page?: number; pageSize?: number; search?: string; enabled?: boolean } = {},
) {
  const { enabled = true, ...rest } = params;
  const query = buildQuery({ page: rest.page, page_size: rest.pageSize, search: rest.search });
  return useQuery({
    queryKey: ["invite-codes", rest],
    queryFn: () => api.get<PaginatedResponse<InviteCodeRow>>(`/invite-codes${query}`),
    enabled,
  });
}

export function useCreateInviteCode() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { note?: string; max_uses?: number; expires_at?: number }) =>
      api.post<InviteCodeRow>("/invite-codes", data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["invite-codes"] });
      qc.invalidateQueries({ queryKey: ["admin-invite-codes"] });
    },
  });
}

export function useDeleteInviteCode() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete(`/invite-codes/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["invite-codes"] });
      qc.invalidateQueries({ queryKey: ["admin-invite-codes"] });
    },
  });
}

export function useAdminInviteCodes(
  params: {
    page?: number;
    pageSize?: number;
    search?: string;
    creatorId?: number;
    enabled?: boolean;
  } = {},
) {
  const { enabled = true, ...rest } = params;
  const query = buildQuery({
    page: rest.page,
    page_size: rest.pageSize,
    search: rest.search,
    creator_id: rest.creatorId,
  });
  return useQuery({
    queryKey: ["admin-invite-codes", rest],
    queryFn: () => api.get<PaginatedResponse<InviteCodeRow>>(`/admin/invite-codes${query}`),
    enabled,
  });
}

export function useAdminDeleteInviteCode() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete(`/admin/invite-codes/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["admin-invite-codes"] });
      qc.invalidateQueries({ queryKey: ["invite-codes"] });
    },
  });
}
