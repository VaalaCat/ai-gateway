import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import type { UseQueryOptions } from "@tanstack/react-query";
import { api, buildQuery } from "./client";
import type { ChannelTestResponse, PaginatedResponse, PaginatedParams } from "@/lib/types";

// === Types ===

export interface BYOKChannelDetail {
  id: number;
  owner_id: number;
  name: string;
  type: number;
  key_last4: string;
  base_url: string;
  models: string[];
  model_mapping: Record<string, string>;
  weight: number;
  priority: number;
  status: number;
  supported_api_types: string;
  endpoints: string;
  passthrough_enabled: boolean;
  use_legacy_adaptor: boolean;
  organization: string;
  api_version: string;
  system_prompt: string;
  system_prompt_in_input: boolean;
  role_mapping: string;
  param_override: string;
  setting: string;
  tag: string;
  remark: string;
  test_model: string;
  auto_ban: number;
  status_code_mapping: string;
  other_settings: string;
  created_at: number;
  updated_at: number;
}

export interface BYOKCreateRequest {
  name: string;
  type: number;
  key: string;
  base_url?: string;
  models: string[];
  model_mapping?: Record<string, string>;
  weight?: number;
  priority?: number;
  supported_api_types?: string;
  endpoints?: string;
  organization?: string;
  api_version?: string;
  system_prompt?: string;
  system_prompt_in_input?: boolean;
  role_mapping?: string;
  param_override?: string;
  setting?: string;
  tag?: string;
  remark?: string;
  test_model?: string;
  auto_ban?: number;
  status_code_mapping?: string;
  other_settings?: string;
  passthrough_enabled?: boolean;
  use_legacy_adaptor?: boolean;
}

export interface ProviderType {
  id: number;
  name: string;
  default_url: string;
}

export interface BYOKListParams extends PaginatedParams {
  type?: string;
  status?: string;
  search?: string;
}

export interface BYOKTestResult {
  ok: boolean;
  status_code: number;
  latency_ms: number;
  detail?: string;
}

// === Portal hooks (user-facing) ===

export function useBYOKChannels(
  params: BYOKListParams = {},
  options?: Omit<UseQueryOptions<PaginatedResponse<BYOKChannelDetail>>, "queryKey" | "queryFn">,
) {
  return useQuery({
    queryKey: ["byok-channels", params],
    queryFn: () =>
      api.get<PaginatedResponse<BYOKChannelDetail>>(
        `/private-channels${buildQuery(params)}`
      ),
    ...options,
  });
}

export function useBYOKChannel(id: number) {
  return useQuery({
    queryKey: ["byok-channels", id],
    queryFn: () =>
      api.get<BYOKChannelDetail>(`/private-channels/${id}`),
    enabled: !!id,
  });
}

export function useBYOKAvailableModels() {
  return useQuery({
    queryKey: ["byok-available-models"],
    queryFn: () =>
      api.get<{ models: string[] }>("/private-channels/available-models"),
  });
}

export function useBYOKSupportedTypes() {
  return useQuery({
    queryKey: ["byok-supported-types"],
    queryFn: () =>
      api.get<{ types: ProviderType[] }>("/private-channels/types"),
  });
}

export function useCreateBYOKChannel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: BYOKCreateRequest) =>
      api.post<BYOKChannelDetail>("/private-channels", body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["byok-channels"] });
    },
  });
}

export function useUpdateBYOKChannel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...patch }: { id: number } & Partial<BYOKChannelDetail>) =>
      api.put<BYOKChannelDetail>(`/private-channels/${id}`, patch),
    onSuccess: (_, variables) => {
      queryClient.invalidateQueries({ queryKey: ["byok-channels"] });
      queryClient.invalidateQueries({ queryKey: ["byok-channels", variables.id] });
    },
  });
}

export function useUpdateBYOKChannelKey() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, key }: { id: number; key: string }) =>
      api.put<BYOKChannelDetail>(`/private-channels/${id}/key`, { key }),
    onSuccess: (_, variables) => {
      queryClient.invalidateQueries({ queryKey: ["byok-channels"] });
      queryClient.invalidateQueries({ queryKey: ["byok-channels", variables.id] });
    },
  });
}

export function useDeleteBYOKChannel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) =>
      api.delete<{ status: string }>(`/private-channels/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["byok-channels"] });
    },
  });
}

/**
 * 把 BYOK portal-test 的原始响应形态适配成
 * admin shared channel 的 ChannelTestResponse 形态。
 * 便于 ChannelTestDialog 通过 testFn prop 复用同一渲染契约。
 */
export function toChannelTestResponse(raw: BYOKTestResult): ChannelTestResponse {
  return {
    success: raw.ok,
    time_cost: raw.latency_ms / 1000,
    error: raw.ok ? "" : raw.detail || `status ${raw.status_code}`,
  };
}

/**
 * 测试 BYOK 渠道连通性，返回 backend portal-test 的原始响应。
 * 调用方按需用 toChannelTestResponse 适配为 admin shared 契约。
 */
export function useTestBYOKChannel() {
  return useMutation({
    mutationFn: ({
      id,
      model,
      endpoint_type,
    }: {
      id: number;
      model?: string;
      endpoint_type?: string;
    }): Promise<BYOKTestResult> =>
      api.post<BYOKTestResult>(`/private-channels/${id}/test`, {
        ...(model ? { model } : {}),
        ...(endpoint_type ? { endpoint_type } : {}),
      }),
  });
}

// === Admin hooks (cross-user view + kill switch) ===

export interface AdminBYOKListParams extends BYOKListParams {
  owner_id?: string;
}

export function useAdminBYOKChannels(
  params: AdminBYOKListParams = {},
  options?: Omit<UseQueryOptions<PaginatedResponse<BYOKChannelDetail>>, "queryKey" | "queryFn">,
) {
  return useQuery({
    queryKey: ["admin-byok-channels", params],
    queryFn: () =>
      api.get<PaginatedResponse<BYOKChannelDetail>>(
        `/admin/private-channels${buildQuery(params)}`
      ),
    ...options,
  });
}

export function useAdminBYOKChannel(id: number) {
  return useQuery({
    queryKey: ["admin-byok-channels", id],
    queryFn: () =>
      api.get<BYOKChannelDetail>(`/admin/private-channels/${id}`),
    enabled: !!id,
  });
}

export function useDisableBYOKChannel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) =>
      api.post<{ status: string }>(`/admin/private-channels/${id}/disable`, {}),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin-byok-channels"] });
    },
  });
}
