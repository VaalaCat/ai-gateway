import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, buildQuery } from "./client";
import type {
  RequestLimiter,
  LimiterBinding,
  LimiterTargetType,
  PaginatedResponse,
  PaginatedParams,
} from "@/lib/types";

interface QueryOptions {
  enabled?: boolean;
}

export function useRateLimiters(params: PaginatedParams = {}, options: QueryOptions = {}) {
  return useQuery({
    queryKey: ["rate-limiters", params],
    queryFn: () =>
      api.get<PaginatedResponse<RequestLimiter>>(`/admin/rate-limiters${buildQuery(params)}`),
    enabled: options.enabled ?? true,
  });
}

export function useCreateRateLimiter() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<RequestLimiter>) =>
      api.post<RequestLimiter>("/admin/rate-limiters", body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["rate-limiters"] });
    },
  });
}

export function useUpdateRateLimiter() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: number } & Partial<RequestLimiter>) =>
      api.put<RequestLimiter>(`/admin/rate-limiters/${id}`, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["rate-limiters"] });
    },
  });
}

export function useDeleteRateLimiter() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete<void>(`/admin/rate-limiters/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["rate-limiters"] });
    },
  });
}

export function useLimiterBindings(limiterId: number, options: QueryOptions = {}) {
  return useQuery({
    queryKey: ["limiter-bindings", limiterId],
    queryFn: () =>
      api.get<LimiterBinding[]>(
        `/admin/limiter-bindings${buildQuery({ limiter_id: limiterId })}`
      ),
    enabled: (options.enabled ?? true) && Number.isFinite(limiterId) && limiterId > 0,
  });
}

export function useCreateLimiterBinding() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: {
      limiter_id: number;
      target_type: LimiterTargetType;
      target_id?: number;
      enabled?: boolean;
    }) => api.post<LimiterBinding>("/admin/limiter-bindings", body),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: ["limiter-bindings", variables.limiter_id] });
    },
  });
}

export function useDeleteLimiterBinding() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id }: { id: number; limiterId: number }) =>
      api.delete<void>(`/admin/limiter-bindings/${id}`),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: ["limiter-bindings", variables.limiterId] });
    },
  });
}
