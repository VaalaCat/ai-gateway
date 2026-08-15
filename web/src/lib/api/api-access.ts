import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, buildQuery } from "./client";
import type { APIRequestExample } from "./api-services";
import { clearAPIAuthorityDerivedCache } from "./clear-api-authority-derived-cache";
import {
  catalogQueriesEnabled,
  catalogScopeKey,
  catalogTokenID,
  type CatalogAccessScope,
} from "@/app/(dashboard)/api-catalog/_components/catalog-token-scope";

export type APIResource = "api_service" | "api_route";
export type APIAction = "invoke";
export type APIPrincipalType = "user" | "user_group" | "token";
export interface APIPermission { id?: number; resource: APIResource; resource_id: number; action: APIAction }
export interface APIRoleMember { principal_type: APIPrincipalType; principal_id: number }
export interface APIRole { id: number; key: string; name: string; description: string; built_in?: boolean; status: number; permissions: APIPermission[]; members: APIRoleMember[]; created_at?: number; updated_at?: number }
export interface APIRoleBinding { id: number; principal_type: APIPrincipalType; principal_id: number; role_id: number; created_at?: number }
interface Page<T> { data: T[]; total: number; page: number; page_size: number }
const validID = (id: number) => Number.isSafeInteger(id) && id > 0;

export type APIAccessScope = "service" | "routes";
export type APIAccessSource = "managed" | "custom_role" | "user_group";
export interface APIAccessGrant { principal_type: APIPrincipalType; principal_id: number; principal_label: string; api_service_id: number; api_service_name: string; configured?: { scope: APIAccessScope; route_ids: number[] }; effective: { scope: APIAccessScope; route_ids: number[] }; sources: APIAccessSource[] }
export interface APIAccessGrantListParams { page?: number; page_size?: number; principal_type?: APIPrincipalType; principal_id?: number; api_service_id?: number; search?: string }
export interface APICatalogService { id: number; slug: string; name: string; description: string }
export interface APICatalogRoute { id: number; api_service_id: number; slug: string; protocols: Array<"http" | "websocket">; allowed_methods: string[]; websocket_subprotocols: string[]; example_request: APIRequestExample }
export interface APICatalogParams { page?: number; page_size?: number; search?: string }

export interface APIRoleListParams {
  page?: number;
  page_size?: number;
  search?: string;
  status?: number;
  assignable?: boolean;
}

export interface APIRoleBindingListParams {
  page?: number;
  page_size?: number;
  principal_type?: APIPrincipalType;
  principal_id?: number;
  role_id?: number;
}

type QueryOptions = { enabled?: boolean };

function catalogQueryParams(scope: CatalogAccessScope, params: object) {
  const tokenID = catalogTokenID(scope);
  return tokenID === undefined ? params : { ...params, token_id: tokenID };
}

function catalogQueryEnabled(scope: CatalogAccessScope, options: QueryOptions, resourceID?: number) {
  return catalogQueriesEnabled(scope) && (resourceID === undefined || validID(resourceID)) && (options.enabled ?? true);
}

export function useAPIRoles(params: APIRoleListParams = {}, options: QueryOptions = {}) { return useQuery({ queryKey: ["api-roles", params], queryFn: () => api.get<Page<APIRole>>(`/admin/api-roles${buildQuery(params)}`), enabled: options.enabled ?? true }); }
export function useAPIRole(id: number, options: QueryOptions = {}) { return useQuery({ queryKey: ["api-role", id], queryFn: () => api.get<APIRole>(`/admin/api-roles/${id}`), enabled: validID(id) && (options.enabled ?? true) }); }
export function useAPIRoleBindings(params: APIRoleBindingListParams = {}, options: QueryOptions = {}) { return useQuery({ queryKey: ["api-role-bindings", params], queryFn: () => api.get<Page<APIRoleBinding>>(`/admin/api-role-bindings${buildQuery(params)}`), enabled: options.enabled ?? true }); }
export function useAPIAccessGrants(params: APIAccessGrantListParams = {}, options: QueryOptions = {}) { return useQuery({ queryKey: ["api-access-grants", params], queryFn: () => api.get<Page<APIAccessGrant>>(`/admin/api-access-grants${buildQuery(params)}`), enabled: options.enabled ?? true }); }
export function useAPICatalogServices(viewerUserID: number, scope: CatalogAccessScope, params: APICatalogParams = {}, options: QueryOptions = {}) {
  return useQuery({
    queryKey: ["api-catalog", "services", viewerUserID, catalogScopeKey(scope), params],
    queryFn: () => api.get<Page<APICatalogService>>(`/api-catalog/services${buildQuery(catalogQueryParams(scope, params))}`),
    enabled: catalogQueryEnabled(scope, options),
    // behavior change: scope changes must not render the previous catalog result.
    placeholderData: undefined,
  });
}
export function useAPICatalogService(viewerUserID: number, scope: CatalogAccessScope, id: number, options: QueryOptions = {}) {
  return useQuery({
    queryKey: ["api-catalog", "service", viewerUserID, catalogScopeKey(scope), id],
    queryFn: () => api.get<APICatalogService>(`/api-catalog/services/detail${buildQuery(catalogQueryParams(scope, { id }))}`),
    enabled: catalogQueryEnabled(scope, options, id),
    // behavior change: scope changes must not render the previous catalog result.
    placeholderData: undefined,
  });
}
export function useAPICatalogRoutes(viewerUserID: number, scope: CatalogAccessScope, serviceID: number, params: APICatalogParams = {}, options: QueryOptions = {}) {
  return useQuery({
    queryKey: ["api-catalog", "routes", viewerUserID, catalogScopeKey(scope), serviceID, params],
    queryFn: () => api.get<Page<APICatalogRoute>>(`/api-catalog/routes${buildQuery(catalogQueryParams(scope, { service_id: serviceID, ...params }))}`),
    enabled: catalogQueryEnabled(scope, options, serviceID),
    // behavior change: scope changes must not render the previous catalog result.
    placeholderData: undefined,
  });
}
export function useAPICatalogEffective(viewerUserID: number, scope: CatalogAccessScope, serviceID: number, options: QueryOptions = {}) {
  return useQuery({
    queryKey: ["api-catalog", "effective", viewerUserID, catalogScopeKey(scope), serviceID],
    queryFn: () => api.get<{ scope: APIAccessScope; route_ids: number[] }>(`/api-catalog/effective${buildQuery(catalogQueryParams(scope, { service_id: serviceID }))}`),
    enabled: catalogQueryEnabled(scope, options, serviceID),
    // behavior change: scope changes must not render the previous catalog result.
    placeholderData: undefined,
  });
}
function useAccessMutation<T, V>(mutationFn: (variables: V) => Promise<T>) { const qc = useQueryClient(); return useMutation({ mutationFn, onSuccess: () => { qc.invalidateQueries({ queryKey: ["api-roles"] }); qc.invalidateQueries({ queryKey: ["api-role"] }); qc.invalidateQueries({ queryKey: ["api-role-bindings"] }); qc.invalidateQueries({ queryKey: ["api-access-grants"] }); clearAPIAuthorityDerivedCache(qc); } }); }
export function useCreateAPIRole() { return useAccessMutation((body: Omit<APIRole, "id" | "built_in">) => api.post<APIRole>("/admin/api-roles", body)); }
export function useUpdateAPIRole() { return useAccessMutation(({ id, ...body }: { id: number } & Omit<APIRole, "id" | "built_in">) => api.put<APIRole>(`/admin/api-roles/${id}`, body)); }
export function useDeleteAPIRole() { return useAccessMutation((id: number) => api.delete<void>(`/admin/api-roles/${id}`)); }
export function useCreateAPIRoleBinding() { return useAccessMutation((body: Omit<APIRoleBinding, "id">) => api.post<APIRoleBinding>("/admin/api-role-bindings", body)); }
export function useUpdateAPIRoleBinding() { return useAccessMutation(({ id, ...body }: APIRoleBinding) => api.put<APIRoleBinding>(`/admin/api-role-bindings/${id}`, body)); }
export function useDeleteAPIRoleBinding() { return useAccessMutation((id: number) => api.delete<void>(`/admin/api-role-bindings/${id}`)); }
export function useReplaceAPIAccessGrant() { return useAccessMutation(({ principal_type, principal_id, api_service_id, ...body }: { principal_type: APIPrincipalType; principal_id: number; api_service_id: number; scope: APIAccessScope; route_ids: number[] }) => api.put<APIAccessGrant["configured"]>(`/admin/api-access-grants/${principal_type}/${principal_id}/services/${api_service_id}`, body)); }
export function useDeleteAPIAccessGrant() { return useAccessMutation(({ principal_type, principal_id, api_service_id }: Pick<APIAccessGrant, "principal_type" | "principal_id" | "api_service_id">) => api.delete<void>(`/admin/api-access-grants/${principal_type}/${principal_id}/services/${api_service_id}`)); }
