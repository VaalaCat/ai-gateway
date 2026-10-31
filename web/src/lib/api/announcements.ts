import { useCallback, useState } from "react";
import { useMutation, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api, ApiError } from "./client";

export interface AnnouncementSummary {
  id: number;
  title: string;
  group_ids: number[];
  show_for_guests: boolean;
  pinned: boolean;
  revision: number;
  created_at: number;
  updated_at: number;
}

export interface Announcement extends AnnouncementSummary {
  content: string;
}

export interface ReceivedAnnouncementSummary extends AnnouncementSummary {
  seen: boolean;
}

export interface AnnouncementPage<T = AnnouncementSummary> {
  items: T[];
  total: number;
  page: number;
  page_size: number;
}

export interface AnnouncementInput {
  title: string;
  content: string;
  group_ids: number[];
  show_for_guests?: boolean;
  pinned?: boolean;
}

// Each identity loads once per page load; navigation and background events reuse its cache.
const sessionReadOptions = {
  staleTime: Infinity,
  gcTime: Infinity,
  refetchOnMount: false,
  refetchOnWindowFocus: false,
  refetchOnReconnect: false,
  refetchInterval: false,
  retry: false,
  retryOnMount: false,
} as const;

const receivedKey = (userId: number | undefined) => ["announcements", "received", userId] as const;
const detailPrefetchLifetime = 5_000;
type AnnouncementDetail = Announcement & { seen?: boolean };
type AnnouncementFeed = AnnouncementPage<AnnouncementSummary & { seen?: boolean }>;
const pagePrefetchLifetime = 5_000;

// Unlike query placeholders, this survives a failed page request. Scope changes
// discard the fallback before it can be exposed to another identity or page size.
function useRetainedAnnouncementPage(data: AnnouncementFeed | undefined, scope: string) {
  const [last, setLast] = useState<{ scope: string; data: AnnouncementFeed | undefined }>({ scope, data });
  if (last.scope !== scope || (data && last.data !== data)) {
    setLast({ scope, data });
  }
  return data ?? (last.scope === scope ? last.data : undefined);
}

export function usePrefetchAnnouncementPage(userId?: number): (page: number, pageSize?: number) => Promise<void> {
  const qc = useQueryClient();
  return useCallback((page: number, pageSize = 10) => {
    const prefix = userId === undefined ? ["announcements", "public"] : receivedKey(userId);
    const committed = qc.getQueryState([...prefix, "feed", page, pageSize]);
    if (committed?.data !== undefined || committed?.fetchStatus === "fetching") return Promise.resolve();
    const queryKey = [...prefix, "page-prefetch", page, pageSize];
    const pending = qc.getQueryCache().find({ queryKey, exact: true });
    if (pending?.state.fetchStatus === "fetching" && pending.promise) {
      return pending.promise.then(() => undefined, () => undefined);
    }
    return qc.prefetchQuery({
      queryKey,
      queryFn: () => api.get<AnnouncementFeed>(`/announcements?page=${page}&page_size=${pageSize}`),
      staleTime: pagePrefetchLifetime,
      gcTime: pagePrefetchLifetime,
      retry: false,
      meta: { startedAt: Date.now(), consumed: false },
    });
  }, [qc, userId]);
}

export function usePrefetchAnnouncementDetail(userId?: number): (id: number) => Promise<void> {
  const qc = useQueryClient();
  return useCallback((id: number) => {
    const prefix = userId === undefined ? ["announcements", "public"] : receivedKey(userId);
    const queryKey = [...prefix, "prefetch", id];
    const pending = qc.getQueryCache().find({ queryKey, exact: true });
    // Preserve an in-flight request's claim and original expiration on repeated hover.
    if (pending?.state.fetchStatus === "fetching" && pending.promise) {
      return pending.promise.then(() => undefined, () => undefined);
    }
    return qc.prefetchQuery({
      queryKey,
      queryFn: () => api.get<AnnouncementDetail>(`/announcements/${id}`),
      staleTime: detailPrefetchLifetime,
      gcTime: detailPrefetchLifetime,
      retry: false,
      meta: { startedAt: Date.now(), consumed: false },
    });
  }, [qc, userId]);
}

export function useAdminAnnouncements(page = 1, pageSize = 10, userId?: number) {
  const query = useQuery({
    ...sessionReadOptions,
    queryKey: ["announcements", "admin", userId, "feed", page, pageSize],
    queryFn: () => api.get<AnnouncementPage>(`/admin/announcements?page=${page}&page_size=${pageSize}`),
  });
  const data = useRetainedAnnouncementPage(query.data, `admin:${userId}:${pageSize}`);
  return { ...query, data, total: data?.total ?? 0, isPlaceholderData: !query.data && !!data };
}

export function useCreateAnnouncement() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: AnnouncementInput) => api.post<Announcement>("/admin/announcements", data),
    onSuccess: () => qc.resetQueries({ queryKey: ["announcements", "admin"], predicate: ({ queryKey }) => queryKey[3] === "feed" }),
  });
}

export function useUpdateAnnouncement() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...data }: AnnouncementInput & { id: number; show_for_guests: boolean; pinned: boolean; reannounce?: boolean }) =>
      api.put<Announcement>(`/admin/announcements/${id}`, data),
    onSuccess: () => qc.resetQueries({ queryKey: ["announcements", "admin"], predicate: ({ queryKey }) => queryKey[3] === "feed" }),
  });
}

export function useDeleteAnnouncement() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete<{ status: "ok" }>(`/admin/announcements/${id}`),
    onSuccess: () => qc.resetQueries({ queryKey: ["announcements", "admin"], predicate: ({ queryKey }) => queryKey[3] === "feed" }),
  });
}

async function fetchAnnouncementPage(qc: QueryClient, prefix: readonly unknown[], page: number, pageSize: number) {
  const prefetchKey = [...prefix, "page-prefetch", page, pageSize];
  const prefetched = qc.getQueryCache().find<AnnouncementFeed>({ queryKey: prefetchKey, exact: true });
  let data: AnnouncementFeed | undefined;
  if (prefetched?.meta && !prefetched.meta.consumed) {
    const pending = prefetched.state.fetchStatus === "fetching";
    const timestamp = pending ? Number(prefetched.meta.startedAt) : prefetched.state.dataUpdatedAt;
    if (Date.now() - timestamp < pagePrefetchLifetime && (pending || prefetched.state.status === "success")) {
      prefetched.meta.consumed = true;
      try {
        data = pending ? await prefetched.promise : prefetched.state.data;
      } catch {
        // Navigation, not hover, retries a failed speculative request.
      }
    }
    qc.removeQueries({ queryKey: prefetchKey, exact: true });
  }
  return data ?? api.get<AnnouncementFeed>(`/announcements?page=${page}&page_size=${pageSize}`);
}

export function useAnnouncements(userId?: number, enabled = true, page = 1, pageSize = 10) {
  const prefix = userId === undefined ? ["announcements", "public"] : receivedKey(userId);
  const qc = useQueryClient();
  const query = useQuery({
    ...sessionReadOptions,
    queryKey: [...prefix, "feed", page, pageSize],
    queryFn: () => fetchAnnouncementPage(qc, prefix, page, pageSize),
    enabled,
  });
  const data = useRetainedAnnouncementPage(query.data, `${userId === undefined ? "public" : `received:${userId}`}:${pageSize}`);
  const loaded = new Map<number, AnnouncementSummary & { seen?: boolean }>();
  let total = data?.total ?? 0;
  let latestMetadataAt = 0;
  for (const [key, cached] of qc.getQueriesData<AnnouncementFeed>({ queryKey: [...prefix, "feed"], predicate: ({ queryKey }) => queryKey.at(-1) === pageSize })) {
    if (!data && cached) {
      const updatedAt = qc.getQueryState(key)?.dataUpdatedAt ?? 0;
      if (updatedAt >= latestMetadataAt) {
        latestMetadataAt = updatedAt;
        total = cached.total;
      }
    }
    for (const item of cached?.items ?? []) loaded.set(item.id, item);
  }
  for (const item of data?.items ?? []) loaded.set(item.id, item);
  return { ...query, data, total, isPlaceholderData: !query.data && !!data, loadedItems: [...loaded.values()] };
}

export function useAnnouncementDetail(id?: number, userId?: number, admin = false) {
  const qc = useQueryClient();
  const prefix = admin ? ["announcements", "admin", userId]
    : userId === undefined ? ["announcements", "public"] : receivedKey(userId);
  return useQuery({
    queryKey: [...prefix, "detail", id],
    queryFn: async ({ signal }) => {
      const prefetchKey = [...prefix, "prefetch", id];
      const prefetched = admin ? undefined : qc.getQueryCache().find<AnnouncementDetail>({ queryKey: prefetchKey, exact: true });
      let detail: AnnouncementDetail | undefined;
      if (prefetched?.meta && !prefetched.meta.consumed) {
        const pending = prefetched.state.fetchStatus === "fetching";
        const timestamp = pending ? Number(prefetched.meta?.startedAt) : prefetched.state.dataUpdatedAt;
        if (Date.now() - timestamp < detailPrefetchLifetime && (pending || prefetched.state.status === "success")) {
          // Claim before awaiting so only this open may reuse the authorization.
          prefetched.meta.consumed = true;
          try {
            detail = pending ? await prefetched.promise : prefetched.state.data;
          } catch {
            // Speculative failures are retried only by this explicit detail open.
          }
        }
        qc.removeQueries({ queryKey: prefetchKey, exact: true });
      }
      signal.throwIfAborted();
      detail ??= await api.get<AnnouncementDetail>(`${admin ? "/admin" : ""}/announcements/${id}`);
      signal.throwIfAborted();
      const summary = {
        id: detail.id, title: detail.title, group_ids: detail.group_ids,
        show_for_guests: detail.show_for_guests, pinned: detail.pinned,
        revision: detail.revision, created_at: detail.created_at, updated_at: detail.updated_at,
        ...(detail.seen === undefined ? {} : { seen: detail.seen }),
      };
      qc.setQueriesData<AnnouncementPage>({ queryKey: [...prefix, "feed"] }, (data) =>
        data && { ...data, items: data.items.map((item) => item.id === id ? { ...item, ...summary } : item) },
      );
      return detail;
    },
    enabled: id !== undefined,
    gcTime: 0,
    staleTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    retry: false,
  });
}

export function useMarkAnnouncementSeen(userId?: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, revision }: { id: number; revision: number }) =>
      api.post<{ status: "ok" }>(`/announcements/${id}/seen`, { revision }),
    onSuccess: (_, { id, revision }) => {
      qc.setQueriesData<AnnouncementPage<ReceivedAnnouncementSummary>>({ queryKey: [...receivedKey(userId), "feed"] }, (data) =>
        data && { ...data, items: data.items.map((item) => item.id === id && item.revision === revision ? { ...item, seen: true } : item) },
      );
    },
    onError: (error, { id }) => {
      if (error instanceof ApiError && error.status === 409) {
        // Resolve only the open detail; never rescan historical pages after a receipt.
        return qc.invalidateQueries({ queryKey: [...receivedKey(userId), "detail", id] });
      }
    },
  });
}
