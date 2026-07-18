import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, buildQuery } from "./client";
import type {
  ModelRouting,
  RoutingMember,
  RoutingCandidates,
  RoutingNamesResp,
  RoutingPreview,
  PaginatedResponse,
  PaginatedParams,
} from "@/lib/types";

export const ROUTING_ERROR_KEYS: Record<string, string> = {
  duplicate_name: "errors.duplicateName",
  invalid_ref: "errors.invalidRef",
  cycle_detected: "errors.cycleDetected",
  max_depth: "errors.maxDepth",
  cannot_nest_user: "errors.cannotNestUser",
  name_contains_comma: "errors.nameContainsComma",
  referenced_by: "errors.referencedBy",
};

export type ModelRoutingApiMode = "admin" | "user";

export type ModelRoutingOwner =
  | { kind: "scope" }
  | { kind: "token"; tokenId: number };

const scopeOwner: ModelRoutingOwner = { kind: "scope" };

function routingBase(apiMode: ModelRoutingApiMode, owner: ModelRoutingOwner): string {
  if (owner.kind === "token") {
    const prefix = apiMode === "admin" ? "/admin" : "";
    return `${prefix}/tokens/${owner.tokenId}/model-routings`;
  }
  return apiMode === "admin" ? "/admin/model-routings" : "/model-routings";
}

function ownerQueryKey(owner: ModelRoutingOwner): readonly [string, number] {
  return [owner.kind, owner.kind === "token" ? owner.tokenId : 0];
}

function routingBody<T extends Record<string, unknown>>(
  body: T,
  owner: ModelRoutingOwner,
): T | Omit<T, "scope" | "user_id" | "token_id"> {
  if (owner.kind !== "token") return body;
  const { scope: _scope, user_id: _userID, token_id: _tokenID, ...tokenBody } = body;
  return tokenBody;
}

export function useModelRoutings(
  params: PaginatedParams & { scope?: 'global' | 'user'; user_id?: number } = {},
  apiMode: ModelRoutingApiMode = "admin",
  owner: ModelRoutingOwner = scopeOwner,
) {
  return useQuery({
    queryKey: ["model-routings", apiMode, ...ownerQueryKey(owner), params],
    queryFn: () =>
      api.get<PaginatedResponse<ModelRouting>>(
        `${routingBase(apiMode, owner)}${buildQuery(params)}`
      ),
  });
}

export function useModelRouting(
  id: number | null,
  apiMode: ModelRoutingApiMode = "admin",
  owner: ModelRoutingOwner = scopeOwner,
) {
  return useQuery({
    queryKey: ["model-routing", apiMode, ...ownerQueryKey(owner), id],
    queryFn: () => api.get<ModelRouting>(`${routingBase(apiMode, owner)}/${id}`),
    enabled: id !== null && id > 0,
  });
}

export function useCreateModelRouting(
  apiMode: ModelRoutingApiMode = "admin",
  owner: ModelRoutingOwner = scopeOwner,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: {
      name: string;
      scope: 'global' | 'user' | 'token';
      user_id?: number;
      token_id?: number;
      members: RoutingMember[];
      enabled?: boolean;
      remark?: string;
    }) => api.post<ModelRouting>(routingBase(apiMode, owner), routingBody(body, owner)),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["model-routings"] });
    },
  });
}

export function useUpdateModelRouting(
  apiMode: ModelRoutingApiMode = "admin",
  owner: ModelRoutingOwner = scopeOwner,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: number } & Partial<ModelRouting>) =>
      api.put<ModelRouting>(
        `${routingBase(apiMode, owner)}/${id}`,
        routingBody(body, owner),
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["model-routings"] });
    },
  });
}

export function useDeleteModelRouting(
  apiMode: ModelRoutingApiMode = "admin",
  owner: ModelRoutingOwner = scopeOwner,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete<void>(`${routingBase(apiMode, owner)}/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["model-routings"] });
    },
  });
}

/**
 * admin 视图候选集。user portal 改用 useRoutingCandidatesByToken。
 */
export function useRoutingCandidates(opts?: { enabled?: boolean }) {
  return useQuery({
    queryKey: ["routing-candidates", "admin"],
    enabled: opts?.enabled ?? true,
    queryFn: () =>
      api.get<RoutingCandidates>("/admin/model-routings/candidates"),
  });
}

export function usePreviewModelRouting(
  apiMode: ModelRoutingApiMode = "admin",
  owner: ModelRoutingOwner = scopeOwner,
) {
  return useMutation({
    mutationFn: (body: {
      members: RoutingMember[];
      self_name?: string;
      self_scope?: 'global' | 'user' | 'token';
      self_user_id?: number;
    }) => api.post<RoutingPreview>(`${routingBase(apiMode, owner)}/preview`, body),
  });
}

/**
 * 获取所有 enabled global routing 名（user portal 视图，admin 应继续走 useRoutingCandidates("admin")）。
 */
export function useGlobalRoutingNames(enabled = true) {
  return useQuery({
    queryKey: ["global-routing-names"],
    staleTime: 30_000,
    enabled,
    queryFn: () =>
      api.get<RoutingNamesResp>("/model-routings/global-routing-names"),
  });
}

/**
 * user portal 候选集：
 * - models：用所选 token 的 sk-key 调 /v1/models（共享 4 层 AND 过滤），
 *   返回的真实模型 ID。
 * - global_routings：直接拿 /global-routing-names 的返回值（所有 enabled
 *   global routing 名），不与 models 取交集。
 *
 * 历史背景：之前实现把两者取交集放进 global_routings，由于 /v1/models 不返回
 * routing 名，交集恒为空，导致 user 模式 ref combobox 看不到任何 routing。
 *
 * 安全性：用户引用 global routing 不会越权——routing 解析到具体 model 后
 * 仍走 token 4 层过滤；引用本身只是表单层的关系。/global-routing-names
 * 后端只返回 enabled=true 的 routing。
 *
 * 不走默认 ApiClient（ApiClient 强制覆盖 Authorization 为 session JWT）。
 * 使用裸 fetch；401/5xx 不外抛全局登出，仅作为 query error 上报。
 */
export function useRoutingCandidatesByToken(
  tokenKey: string | null | undefined,
) {
  const routingNamesQuery = useGlobalRoutingNames(!!tokenKey);

  return useQuery({
    queryKey: ["routing-candidates-by-token", tokenKey],
    enabled: !!tokenKey && routingNamesQuery.isSuccess,
    staleTime: 30_000,
    queryFn: async () => {
      if (!tokenKey) {
        return { models: [], global_routings: [] };
      }
      const res = await fetch("/v1/models", {
        headers: {
          Authorization: `Bearer ${tokenKey}`,
        },
      });
      if (!res.ok) {
        throw new Error(`/v1/models ${res.status}`);
      }
      const body = (await res.json()) as {
        data?: { id: string; owned_by?: string }[];
      };
      const visible = body.data ?? [];
      return {
        models: visible
          .filter((model) => model.owned_by !== "ai-gateway-routing")
          .map((model) => model.id),
        global_routings: routingNamesQuery.data?.names ?? [],
        visible_refs: visible.map((model) => model.id),
      };
    },
  });
}
