import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, buildQuery } from "./client";
import type { ModelConfig, PaginatedResponse, PaginatedParams } from "@/lib/types";

export interface PricingValues {
  input_price: number;
  output_price: number;
  cache_read_price: number;
  cache_write_price: number;
}

export interface PriceCandidate {
  source: string;
  provider: string;
  match_type: string;
  matched_name: string;
  price: PricingValues;
}

export interface PricingRecommendation {
  model_id: number;
  model_name: string;
  current: PricingValues;
  has_price: boolean;
  recommended: PricingValues;
  provenance: string;
  confidence: "high" | "needs_review";
  review_reasons?: string[];
  has_change: boolean;
  candidates: PriceCandidate[];
}

export interface FetchPricingResponse {
  matches: PricingRecommendation[];
  unmatched_models: string[];
  source_errors?: Record<string, string>;
}

interface PricingUpdate {
  model_id: number;
  input_price: number;
  output_price: number;
  cache_read_price: number;
  cache_write_price: number;
}

export function useModels(
  params: PaginatedParams & { search?: string; price_filter?: string } = {},
) {
  return useQuery({
    queryKey: ["models", params],
    queryFn: () => api.get<PaginatedResponse<ModelConfig>>(`/admin/models${buildQuery(params)}`),
  });
}

export function useModel(id: number) {
  return useQuery({
    queryKey: ["models", id],
    queryFn: () => api.get<ModelConfig>(`/admin/models/${id}`),
    enabled: !!id,
  });
}

export function useCreateModel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<ModelConfig>) =>
      api.post<ModelConfig>("/admin/models", body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["models"] });
    },
  });
}

export function useUpdateModel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: number } & Partial<ModelConfig>) =>
      api.put<ModelConfig>(`/admin/models/${id}`, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["models"] });
    },
  });
}

export function useDeleteModel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete<void>(`/admin/models/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["models"] });
    },
  });
}

export function useSyncModels() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<{ created: number }>("/admin/models/sync", {}),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["models"] });
    },
  });
}

export function useFetchPricing() {
  return useMutation({
    mutationFn: (params?: { source?: string }) => {
      const query = new URLSearchParams();
      if (params?.source) query.set("source", params.source);
      const qs = query.toString();
      return api.post<FetchPricingResponse>(
        `/admin/models/fetch-pricing${qs ? `?${qs}` : ""}`,
        {}
      );
    },
  });
}

export function useApplyPricing() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { updates: PricingUpdate[] }) =>
      api.post<{ updated: number }>("/admin/models/apply-pricing", body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["models"] });
    },
  });
}
