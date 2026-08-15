import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, buildQuery, type StatusResponse } from "./client";

export type APIProtocol = "http" | "websocket";
export type APIUpstreamAuthType = "none" | "bearer" | "header" | "query" | "basic";

export interface APIUpstreamCredential {
  bearer_token?: string;
  header_name?: string;
  header_value?: string;
  query_name?: string;
  query_value?: string;
  basic_username?: string;
  basic_password?: string;
}

export interface APIService {
  id: number;
  slug: string;
  name: string;
  description: string;
  price_per_call: number;
  status: number;
  created_at?: number;
  updated_at?: number;
}

export interface APIBackend {
	id: number;
	api_service_id: number;
	name: string;
	route_count: number;
	upstream_count: number;
	enabled_upstream_count: number;
	endpoint_hosts: string[];
	updated_at?: number;
}

export interface APIRequestExample {
	method: string;
	subpath: string;
	query: string;
	headers: Record<string, string>;
	body: string;
}

export interface APIRouteFields {
  slug: string;
  protocols: APIProtocol[];
  allowed_methods: string[];
  upstream_path: string;
  forward_subpath: boolean;
  example_request: APIRequestExample;
  websocket_subprotocols?: string[];
  status?: number;
}

export interface APIRouteCreateInput extends APIRouteFields {
	api_service_id: number;
	target: APIRouteTargetCommand;
}

export type APIRouteUpdateInput = APIRouteFields & { target?: APIRouteTargetCommand };
export type APIRouteInput = APIRouteCreateInput;

export interface APIRoute extends APIRouteFields {
	id: number;
	api_service_id: number;
	backend_id: number;
	status: number;
	updated_at?: number;
}

export interface APIUpstream {
	id: number;
	backend_id: number;
  name: string;
  base_url: string;
  weight: number;
  priority: number;
  auth_type: APIUpstreamAuthType;
  status: number;
  credential_configured: boolean;
  proxy_url_configured: boolean;
	header_override?: Record<string, string>;
}

export interface APIUpstreamInput {
	name: string;
  base_url: string;
  weight: number;
  priority: number;
  auth_type: APIUpstreamAuthType;
  status?: number;
	credential?: APIUpstreamCredential;
	proxy_url?: string;
	header_override?: Record<string, string>;
}

export interface APIUpstreamCreateInput extends APIUpstreamInput {
	backend_id: number;
}

export type APIUpstreamUpdateInput = APIUpstreamInput;

export type APIRouteTargetCommand =
	| { mode: "existing"; backend_id: number }
	| { mode: "create"; backend: { name: string }; first_upstream: APIUpstreamInput };

export interface APIRoutePreviewInput {
	api_service_id: number;
	slug: string;
	upstream_path: string;
	forward_subpath: boolean;
	sample: APIRequestExample;
	target: APIRouteTargetCommand;
}

export interface APIRoutePreviewEndpoint {
	upstream_id: number;
	upstream_name: string;
	status: number;
	priority: number;
	weight: number;
	final_url: string;
}

export interface APIRoutePreview {
	endpoints: APIRoutePreviewEndpoint[];
	diagnostics: string[];
}

const previewRevisions = new WeakMap<object, number>();
let nextPreviewRevision = 0;

function previewRevision(draft: APIRoutePreviewInput | undefined) {
	if (!draft) return 0;
	const current = previewRevisions.get(draft);
	if (current !== undefined) return current;
	const created = ++nextPreviewRevision;
	previewRevisions.set(draft, created);
	return created;
}

interface Page<T> { data: T[]; total: number; page: number; page_size: number }
interface APIScopedPage<T> extends Page<T> { api_service_id?: number; backend_id?: number }
const validID = (id: number) => Number.isSafeInteger(id) && id > 0;

export interface APIServiceListParams {
  page?: number;
  page_size?: number;
  search?: string;
  status?: number;
}

export interface APIRouteListParams {
  api_service_id: number;
  page?: number;
  page_size?: number;
  search?: string;
  status?: number;
}

export interface APIBackendListParams {
	api_service_id: number;
	page?: number;
	page_size?: number;
	search?: string;
}

interface APIUpstreamListFilters {
	page?: number;
	page_size?: number;
	search?: string;
	status?: number;
}

export type APIUpstreamListParams = APIUpstreamListFilters & (
	| { api_service_id: number; backend_id?: number }
	| { api_service_id?: number; backend_id: number }
);

type QueryOptions = { enabled?: boolean; retainPreviousData?: boolean };
type PreviewQueryOptions = QueryOptions & { cacheKey?: string | number };

async function fetchAllPages<T>(path: string, scope: Record<string, number>) {
	const rows: T[] = [];
	for (let page = 1; ; page += 1) {
		const response = await api.get<Page<T>>(`${path}${buildQuery({ ...scope, page, page_size: 100 })}`);
		rows.push(...response.data);
		if (response.data.length === 0 || page * response.page_size >= response.total) return rows;
	}
}

function childListParams(
	params: APIRouteListParams | number,
): APIRouteListParams {
	return typeof params === "number" ? { api_service_id: params } : params;
}

function upstreamListParams(params: APIUpstreamListParams | number): APIUpstreamListParams {
	return typeof params === "number" ? { api_service_id: params } : params;
}

function validUpstreamScope(params: APIUpstreamListParams) {
	const hasService = params.api_service_id !== undefined;
	const hasBackend = params.backend_id !== undefined;
	return (hasService || hasBackend)
		&& (params.api_service_id === undefined || validID(params.api_service_id))
		&& (params.backend_id === undefined || validID(params.backend_id));
}

export function useAPIServices(params: APIServiceListParams = {}, options: QueryOptions = {}) {
  return useQuery({ queryKey: ["api-services", params], queryFn: () => api.get<Page<APIService>>(`/admin/api-services${buildQuery(params)}`), enabled: options.enabled ?? true });
}
export function useAPIService(id: number, options: QueryOptions = {}) {
  return useQuery({ queryKey: ["api-service", id], queryFn: () => api.get<APIService>(`/admin/api-services/${id}`), enabled: validID(id) && (options.enabled ?? true) });
}
export function useAPIBackends(params: APIBackendListParams, options: QueryOptions = {}) {
	return useQuery({ queryKey: ["api-backends", params], queryFn: async () => ({ ...await api.get<Page<APIBackend>>(`/admin/api-backends${buildQuery(params)}`), api_service_id: params.api_service_id }), enabled: validID(params.api_service_id) && (options.enabled ?? true), placeholderData: options.retainPreviousData ? keepPreviousData : undefined });
}
export function useAPIBackendSummaries(apiServiceID: number, backendIDs: number[], options: QueryOptions = {}) {
	const requiredIDs = [...new Set(backendIDs.filter(validID))].sort((a, b) => a - b);
	return useQuery({
		queryKey: ["api-backends", "summaries", apiServiceID, requiredIDs],
		queryFn: async () => {
			const required = new Set(requiredIDs);
			const found = new Map<number, APIBackend>();
			for (let page = 1; found.size < required.size; page += 1) {
				const response = await api.get<Page<APIBackend>>(`/admin/api-backends${buildQuery({ api_service_id: apiServiceID, page, page_size: 100 })}`);
				for (const backend of response.data) if (required.has(backend.id)) found.set(backend.id, backend);
				if (response.data.length === 0 || page * response.page_size >= response.total) break;
			}
			return [...found.values()];
		},
		enabled: validID(apiServiceID) && requiredIDs.length > 0 && (options.enabled ?? true),
	});
}
export function useAPIBackend(id: number, options: QueryOptions = {}) {
	return useQuery({ queryKey: ["api-backend", id], queryFn: () => api.get<APIBackend>(`/admin/api-backends/${id}`), enabled: validID(id) && (options.enabled ?? true) });
}
export function useAllAPIBackends(apiServiceID: number, options: QueryOptions = {}) {
	return useQuery({
		queryKey: ["api-backends", "all", apiServiceID],
		queryFn: () => fetchAllPages<APIBackend>("/admin/api-backends", { api_service_id: apiServiceID }),
		enabled: validID(apiServiceID) && (options.enabled ?? true),
	});
}
export function useAPIRoutes(params: APIRouteListParams | number, options: QueryOptions = {}) {
  const queryParams = childListParams(params);
	return useQuery({ queryKey: ["api-routes", queryParams], queryFn: async () => ({ ...await api.get<Page<APIRoute>>(`/admin/api-routes${buildQuery(queryParams)}`), api_service_id: queryParams.api_service_id }) satisfies APIScopedPage<APIRoute>, enabled: validID(queryParams.api_service_id) && (options.enabled ?? true), placeholderData: options.retainPreviousData ? keepPreviousData : undefined });
}
export function useAPIRoute(id: number, options: QueryOptions = {}) {
  return useQuery({ queryKey: ["api-route", id], queryFn: () => api.get<APIRoute>(`/admin/api-routes/${id}`), enabled: validID(id) && (options.enabled ?? true) });
}
export function useAllAPIRoutes(apiServiceID: number, options: QueryOptions = {}) {
	return useQuery({
		queryKey: ["api-routes", "all", apiServiceID],
		queryFn: () => fetchAllPages<APIRoute>("/admin/api-routes", { api_service_id: apiServiceID }),
		enabled: validID(apiServiceID) && (options.enabled ?? true),
	});
}
export function useAPIUpstreams(params: APIUpstreamListParams | number, options: QueryOptions = {}) {
	const queryParams = upstreamListParams(params);
	return useQuery({ queryKey: ["api-upstreams", queryParams], queryFn: async () => ({ ...await api.get<Page<APIUpstream>>(`/admin/api-upstreams${buildQuery(queryParams)}`), ...(queryParams.api_service_id !== undefined ? { api_service_id: queryParams.api_service_id } : {}), ...(queryParams.backend_id !== undefined ? { backend_id: queryParams.backend_id } : {}) }) satisfies APIScopedPage<APIUpstream>, enabled: validUpstreamScope(queryParams) && (options.enabled ?? true), placeholderData: options.retainPreviousData ? keepPreviousData : undefined });
}
export function useAllAPIUpstreams(backendID: number, options: QueryOptions = {}) {
	return useQuery({
		queryKey: ["api-upstreams", "all", backendID],
		queryFn: () => fetchAllPages<APIUpstream>("/admin/api-upstreams", { backend_id: backendID }),
		enabled: validID(backendID) && (options.enabled ?? true),
	});
}
export function useAllAPIUpstreamsByService(apiServiceID: number, options: QueryOptions = {}) {
	return useQuery({
		queryKey: ["api-upstreams", "all-by-service", apiServiceID],
		queryFn: () => fetchAllPages<APIUpstream>("/admin/api-upstreams", { api_service_id: apiServiceID }),
		enabled: validID(apiServiceID) && (options.enabled ?? true),
	});
}
export function useAPIUpstream(id: number, options: QueryOptions = {}) {
  return useQuery({ queryKey: ["api-upstream", id], queryFn: () => api.get<APIUpstream>(`/admin/api-upstreams/${id}`), enabled: validID(id) && (options.enabled ?? true) });
}

export function useAPIRoutePreview(draft: APIRoutePreviewInput | undefined, options: PreviewQueryOptions = {}) {
	return useQuery({
		queryKey: ["api-route-preview", draft?.api_service_id ?? 0, options.cacheKey === undefined ? "draft" : "route", options.cacheKey ?? previewRevision(draft)],
		staleTime: options.cacheKey === undefined ? 0 : 30_000,
		gcTime: options.cacheKey === undefined ? 0 : 5 * 60_000,
		queryFn: ({ signal }) => {
			if (draft === undefined) throw new Error("API route preview draft is required");
			return api.post<APIRoutePreview>("/admin/api-routes/preview", draft, { signal });
		},
		enabled: draft !== undefined && validID(draft.api_service_id) && (options.enabled ?? true),
	});
}

const invalidationKeys = ["api-services", "api-service", "api-backends", "api-backend", "api-routes", "api-route", "api-upstreams", "api-upstream", "api-route-preview"] as const;

function useInvalidatingMutation<T, V>(mutationFn: (variables: V) => Promise<T>) {
	const queryClient = useQueryClient();
	return useMutation({ mutationFn, onSuccess: () => Promise.all(invalidationKeys.map((key) => queryClient.invalidateQueries({ queryKey: [key] }))) });
}
export function useCreateAPIService() { return useInvalidatingMutation((body: Partial<APIService>) => api.post<APIService>("/admin/api-services", body)); }
export function useUpdateAPIService() { return useInvalidatingMutation(({ id, ...body }: { id: number } & Partial<APIService>) => api.put<APIService>(`/admin/api-services/${id}`, body)); }
export function useDeleteAPIService() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete<void>(`/admin/api-services/${id}`),
    onSuccess: () => {
		void Promise.all(invalidationKeys.map((key) => queryClient.invalidateQueries({ queryKey: [key] }))).catch(() => undefined);
    },
  });
}
export function useCreateAPIBackend() { return useInvalidatingMutation((body: { api_service_id: number; name: string }) => api.post<APIBackend>("/admin/api-backends", body)); }
export function useUpdateAPIBackend() { return useInvalidatingMutation(({ id, ...body }: { id: number; name: string }) => api.put<StatusResponse>(`/admin/api-backends/${id}`, body)); }
export function useDeleteAPIBackend() { return useInvalidatingMutation((id: number) => api.delete<void>(`/admin/api-backends/${id}`)); }
export function useCreateAPIRoute() { return useInvalidatingMutation((body: APIRouteCreateInput) => api.post<APIRoute>("/admin/api-routes", body)); }
export function useUpdateAPIRoute() { return useInvalidatingMutation(({ id, ...body }: { id: number } & Partial<APIRouteUpdateInput>) => api.put<StatusResponse>(`/admin/api-routes/${id}`, body)); }
export function useDeleteAPIRoute() { return useInvalidatingMutation((id: number) => api.delete<void>(`/admin/api-routes/${id}`)); }
export function useCreateAPIUpstream() { return useInvalidatingMutation((body: APIUpstreamCreateInput) => api.post<APIUpstream>("/admin/api-upstreams", body)); }
export function useUpdateAPIUpstream() { return useInvalidatingMutation(({ id, ...body }: { id: number } & Partial<APIUpstreamUpdateInput>) => api.put<APIUpstream>(`/admin/api-upstreams/${id}`, body)); }
export function useDeleteAPIUpstream() { return useInvalidatingMutation((id: number) => api.delete<void>(`/admin/api-upstreams/${id}`)); }
