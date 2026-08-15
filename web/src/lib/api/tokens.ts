import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import type { UseQueryOptions } from "@tanstack/react-query";
import { api, buildQuery } from "./client";
import { clearAPIAuthorityDerivedCache } from "./clear-api-authority-derived-cache";
import type { APIRoleMode, Token, TokenTraceMode, PaginatedResponse, PaginatedParams } from "@/lib/types";

type TokenListParams = Pick<PaginatedParams, "page" | "page_size"> & {
  user_id?: number;
  status?: number;
  search?: string;
	token_id?: number;
	usable_only?: boolean;
	api_service_id?: number;
	api_route_id?: number;
	api_role_mode?: APIRoleMode;
};

type TokenListOptions = Omit<UseQueryOptions<PaginatedResponse<Token>>, "queryKey" | "queryFn"> & {
  cacheScope?: readonly unknown[];
};

export function useTokens(
  params: TokenListParams = {},
  options: TokenListOptions = {},
) {
  const { cacheScope = [], ...queryOptions } = options;
  return useQuery({
    queryKey: ["tokens", "list", ...cacheScope, params],
    queryFn: () => api.get<PaginatedResponse<Token>>(`/tokens${buildQuery(params)}`),
    ...queryOptions,
  });
}

export function useToken(id: number, options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: ["tokens", id],
    queryFn: () => api.get<Token>(`/tokens/${id}`),
    enabled: !!id && (options.enabled ?? true),
  });
}

interface UsableTokenForAPIRouteScope {
  viewerUserID: number;
  ownerUserID?: number;
  apiServiceID: number;
  apiRouteID: number;
  tokenID: number;
}

const validID = (id: number) => Number.isSafeInteger(id) && id > 0;

async function findUsableTokenForAPIRoute(scope: UsableTokenForAPIRouteScope) {
  const params = {
    usable_only: true,
    ...(scope.ownerUserID !== undefined ? { user_id: scope.ownerUserID } : {}),
    api_service_id: scope.apiServiceID,
    api_route_id: scope.apiRouteID,
    token_id: scope.tokenID,
    page: 1,
    page_size: 1,
  };
  const response = await api.get<PaginatedResponse<Token>>(`/tokens${buildQuery(params)}`);
  return response.data.find((item) => (
    item.id === scope.tokenID
    && (scope.ownerUserID === undefined || item.user_id === scope.ownerUserID)
  )) ?? null;
}

export function useUsableTokenForAPIRoute(scope: UsableTokenForAPIRouteScope) {
  const enabled = validID(scope.viewerUserID)
    && validID(scope.apiServiceID)
    && validID(scope.apiRouteID)
    && validID(scope.tokenID);
  return useQuery({
    queryKey: [
      "tokens", "usable-for-api-route",
      scope.viewerUserID, scope.apiServiceID, scope.apiRouteID, scope.tokenID,
      "owner", scope.ownerUserID ?? 0,
    ],
    queryFn: () => findUsableTokenForAPIRoute(scope),
    enabled,
  });
}

export function useCreateToken() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { user_id?: number; name: string; key?: string; expired_at?: number; models?: string; template_id?: number; trace_enabled?: boolean; trace_mode?: TokenTraceMode; byok_only?: boolean; allowed_channel_ids?: number[] }) =>
      api.post<Token>("/tokens", body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tokens"] });
      clearAPIAuthorityDerivedCache(queryClient);
    },
  });
}

export function useUpdateToken() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: number; api_role_mode?: APIRoleMode; api_role_ids?: number[] } & Partial<Token>) =>
      api.put<Token>(`/tokens/${id}`, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tokens"] });
      queryClient.invalidateQueries({ queryKey: ["available-models"] });
      clearAPIAuthorityDerivedCache(queryClient);
    },
  });
}

export function useDeleteToken() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete<void>(`/tokens/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tokens"] });
      clearAPIAuthorityDerivedCache(queryClient);
    },
  });
}
