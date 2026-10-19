import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "./client";

export interface Announcement {
  id: number;
  title: string;
  content: string;
  group_ids: number[];
  show_for_guests: boolean;
  pinned: boolean;
  revision: number;
  created_at: number;
  updated_at: number;
}

export interface ReceivedAnnouncement extends Announcement {
  seen: boolean;
}

export interface AnnouncementInput {
  title: string;
  content: string;
  group_ids: number[];
  show_for_guests?: boolean;
  pinned?: boolean;
}

export function useAdminAnnouncements() {
  return useQuery({
    queryKey: ["announcements", "admin"],
    queryFn: () => api.get<Announcement[]>("/admin/announcements"),
  });
}

export function useCreateAnnouncement() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: AnnouncementInput) => api.post<Announcement>("/admin/announcements", data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["announcements"] }),
  });
}

export function useUpdateAnnouncement() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...data }: AnnouncementInput & { id: number; show_for_guests: boolean; pinned: boolean; reannounce?: boolean }) =>
      api.put<Announcement>(`/admin/announcements/${id}`, data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["announcements"] }),
  });
}

export function useDeleteAnnouncement() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete<{ status: "ok" }>(`/admin/announcements/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["announcements"] }),
  });
}

export function useAnnouncements(userId?: number) {
  return useQuery({
    queryKey: ["announcements", "received", userId],
    queryFn: () => api.get<ReceivedAnnouncement[]>("/announcements"),
    enabled: Boolean(userId),
    refetchInterval: 30_000,
    refetchOnWindowFocus: true,
  });
}

export function usePublicAnnouncements(enabled: boolean) {
  return useQuery({
    queryKey: ["announcements", "public"],
    queryFn: () => api.get<Announcement[]>("/public/announcements"),
    enabled,
    refetchInterval: 30_000,
    refetchOnWindowFocus: true,
  });
}

export function useMarkAnnouncementSeen(userId?: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, revision }: { id: number; revision: number }) =>
      api.post<{ status: "ok" }>(`/announcements/${id}/seen`, { revision }),
    onSuccess: (_, { id, revision }) => {
      qc.setQueryData<ReceivedAnnouncement[]>(["announcements", "received", userId], (items) =>
        items?.map((item) => item.id === id && item.revision === revision ? { ...item, seen: true } : item),
      );
      return qc.invalidateQueries({ queryKey: ["announcements", "received", userId] });
    },
    onError: (error) => {
      if (error instanceof ApiError && error.status === 409) {
        return qc.invalidateQueries({ queryKey: ["announcements", "received", userId] });
      }
    },
  });
}
