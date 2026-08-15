import { keepPreviousData, useQuery, type UseQueryOptions } from "@tanstack/react-query";

import { api, buildQuery } from "./client";

export type ModelMarketplaceRole = "user" | "admin";
export type ModelMarketplaceKind = "" | "real" | "routing";
export type MarketplaceHealthStatus = "operational" | "degraded" | "outage" | "unknown";
export type MarketplaceEndpoint = "chat_completions" | "responses" | "messages" | "models";
export type MarketplaceOfferKind = "platform" | "private";
export type MarketplaceOfferOwnership = "platform" | "owned" | "shared";
export type MarketplacePriceAccuracy = "exact" | "reference";
export type MarketplacePerformanceStatus = "available" | "stale" | "unavailable";
export type MarketplaceUsageAvailability = "available" | "unavailable" | "not_applicable";
export type MarketplaceUsageScope = "selected_token" | "owner_channel_total" | "offer_total";
export type MarketplaceUsageWindow = "24h" | "7d" | "30d";

export interface MarketplacePrices {
  input: number;
  output: number;
  cache_read: number;
  cache_write: number;
}

export interface MarketplaceOfferPricing {
  reference_price: MarketplacePrices;
  gateway_charge: MarketplacePrices;
  estimated_total: MarketplacePrices;
  accuracy: MarketplacePriceAccuracy;
}

export interface MarketplaceModelOffer {
  offer_ref: string;
  kind: MarketplaceOfferKind;
  display_name: string;
  ownership: MarketplaceOfferOwnership;
  available: boolean;
  supported_endpoints: MarketplaceEndpoint[];
  pricing: MarketplaceOfferPricing;
  performance_status: MarketplacePerformanceStatus;
  performance: MarketplacePerformanceSummary;
  status_history: MarketplacePerformanceStatusBucket[];
  trend_series: MarketplacePerformanceTrendPoint[];
  usage_references: MarketplaceUsageReference[];
}

export interface MarketplaceTokenUnits {
  input: number;
  output: number;
  cache_read: number;
  cache_write: number;
  total: number;
}

export interface MarketplacePerformanceSummary {
  status: MarketplaceHealthStatus;
  success_rate: number | null;
  ttft_avg_ms: number | null;
  ttft_p95_ms: number | null;
  tps_avg: number | null;
  tps_p5: number | null;
  duration_p95_ms: number | null;
  token_units: MarketplaceTokenUnits;
}

export interface MarketplacePerformanceStatusBucket {
  started_at: number;
  ended_at: number;
  status: MarketplaceHealthStatus;
  in_progress: boolean;
}

export interface MarketplaceModelPerformanceStatusBucket
  extends MarketplacePerformanceStatusBucket {
  success_rate: number | null;
}

export interface MarketplaceModelPerformance {
  performance_status: MarketplacePerformanceStatus;
  window: MarketplaceUsageWindow;
  success_rate: number | null;
  cache_hit_rate: number | null;
  status_history: MarketplaceModelPerformanceStatusBucket[];
}

export interface MarketplacePerformanceTrendPoint extends MarketplacePerformanceStatusBucket {
  success_rate: number | null;
  ttft_avg_ms: number | null;
  tps_avg: number | null;
  token_units: MarketplaceTokenUnits;
}

export interface MarketplaceUsageReference {
  scope: MarketplaceUsageScope;
  window: MarketplaceUsageWindow;
  token_units: MarketplaceTokenUnits;
  reference_cost: number | null;
  gateway_charge_cost: number;
  estimated_total_cost: number | null;
  accuracy: MarketplacePriceAccuracy;
  includes_shared_usage: boolean;
}

export type MarketplaceModelOfferDetail = MarketplaceModelOffer;

export interface MarketplaceRealModelDetail extends Omit<MarketplaceRealModel, "offers"> {
  offers: MarketplaceModelOfferDetail[];
}

export type MarketplaceModelDetail =
  | { kind: "real"; real: MarketplaceRealModelDetail; routing?: never }
  | { kind: "routing"; routing: MarketplaceRoutingModel; real?: never };

export interface AdminMarketplaceOfferDiagnostics {
  channel_id: number;
  private_channel_id: number;
  internal_name: string;
  public_display_name: string;
  owner_id: number;
  base_url: string;
  endpoint_paths: Array<{ endpoint: MarketplaceEndpoint; path: string }>;
  disabled_reasons: string[];
}

export interface AdminMarketplacePerformanceSummary extends MarketplacePerformanceSummary {
  request_count: number;
  success_count: number;
  failure_count: number;
  stream_request_count: number;
  ttft_sample_count: number;
  tps_sample_count: number;
  duration_sample_count: number;
}

export interface AdminMarketplaceModelOfferDetail extends Omit<MarketplaceModelOfferDetail, "performance"> {
  performance: AdminMarketplacePerformanceSummary;
  diagnostics: AdminMarketplaceOfferDiagnostics;
}

export interface AdminMarketplaceRealModelDetail extends Omit<MarketplaceRealModel, "offers"> {
  offers: AdminMarketplaceModelOfferDetail[];
}

export interface AdminMarketplaceRoutingPath {
  ref: string;
  routing_id: number;
}

export interface AdminMarketplaceRoutingModel extends MarketplaceRoutingModel {
  diagnostics: {
    definitions: Array<{
      occurrence_id: string;
      path: AdminMarketplaceRoutingPath[];
      routing_id: number;
      name: string;
      scope: string;
      user_id: number;
      token_id: number;
      enabled: boolean;
      members: Array<{
        ref: string;
        priority: number;
        weight: number;
        kind: "model" | "routing" | "invalid";
        model_name?: string;
        routing_id?: number;
      }>;
    }>;
  };
}

export type AdminMarketplaceModelDetail =
  | { kind: "real"; real: AdminMarketplaceRealModelDetail; routing?: never }
  | { kind: "routing"; routing: AdminMarketplaceRoutingModel; real?: never };

export interface ModelMarketplaceDetailResponse {
  selected_token: { id: number; name: string };
  window: MarketplaceUsageWindow;
  usage_status: MarketplaceUsageAvailability;
  model: MarketplaceModelDetail;
}

export interface AdminModelMarketplaceDetailResponse {
  view: {
    mode: "global" | "token_preview";
    selected_token: { id: number; name: string } | null;
  };
  window: MarketplaceUsageWindow;
  usage_status: MarketplaceUsageAvailability;
  model: AdminMarketplaceModelDetail;
}

export interface MarketplaceModelMetadata {
  display_name: string;
  description: string;
  provider: string;
  input_modalities: string[];
  output_modalities: string[];
  context_length: number;
  max_output_tokens: number;
  supported_parameters: string[];
  tool_calling: boolean;
  structured_output: boolean;
  reasoning: boolean;
  prompt_cache: boolean;
}

export interface MarketplaceRealModel {
  model_name: string;
  metadata: MarketplaceModelMetadata;
  aggregate_status: MarketplaceHealthStatus;
  available_offer_count: number;
  platform_offer_count: number;
  private_offer_count: number;
  offers: MarketplaceModelOffer[];
  performance: MarketplaceModelPerformance;
}

export interface MarketplaceRoutingDestination {
  model_name: string;
  offers: Array<{
    offer_ref: string;
    kind: MarketplaceOfferKind;
    display_name: string;
    ownership: MarketplaceOfferOwnership;
    available: boolean;
    supported_endpoints: MarketplaceEndpoint[];
  }>;
}

export interface MarketplaceRoutingModel {
  model_name: string;
  display_name: string;
  reachable_real_models: string[];
  flattened_destinations: MarketplaceRoutingDestination[];
  routing_warnings: MarketplaceRoutingWarning[];
  guidance: "view_reachable_real_models";
}

export type MarketplaceRoutingWarning =
  | "cycle"
  | "max_depth"
  | "disabled"
  | "model_not_found"
  | "no_visible_offer"
  | (string & {});

export type MarketplaceModel =
  | { kind: "real"; real: MarketplaceRealModel; routing?: never }
  | { kind: "routing"; routing: MarketplaceRoutingModel; real?: never };

export interface MarketplaceFilters {
  providers: string[];
  input_modalities: string[];
  output_modalities: string[];
}

export interface ModelMarketplaceListResponse {
  selected_token: { id: number; name: string };
  models: MarketplaceModel[];
  filters: MarketplaceFilters;
  total: number;
  page: number;
  page_size: number;
}

export interface AdminModelMarketplaceListResponse {
  view: {
    mode: "global" | "token_preview";
    selected_token: { id: number; name: string } | null;
  };
  models: MarketplaceModel[];
  filters: MarketplaceFilters;
  total: number;
  page: number;
  page_size: number;
}

export interface ModelMarketplaceListParams {
  tokenId?: number;
  search?: string;
  provider?: string;
  kind?: ModelMarketplaceKind;
  page: number;
  pageSize: number;
}

function normalizedListParams(params: ModelMarketplaceListParams) {
  return {
    tokenId: params.tokenId ?? null,
    search: params.search ?? "",
    provider: params.provider ?? "",
    kind: params.kind ?? "",
    page: params.page,
    pageSize: params.pageSize,
  };
}

export function modelMarketplaceListQueryKey(
  role: ModelMarketplaceRole,
  viewerId: number | undefined,
  params: ModelMarketplaceListParams,
) {
  return [
    "model-marketplace",
    "list",
    { role, viewerId: viewerId ?? null, ...normalizedListParams(params) },
  ] as const;
}

function listQuery(params: ModelMarketplaceListParams) {
  return buildQuery({
    token_id: params.tokenId,
    search: params.search,
    provider: params.provider,
    kind: params.kind,
    page: params.page,
    page_size: params.pageSize,
  });
}

function listQueryScopeMatches(
  queryKey: readonly unknown[] | undefined,
  role: ModelMarketplaceRole,
  viewerId: number | undefined,
  tokenId: number | undefined,
) {
  const scope = queryKey?.[2];
  if (typeof scope !== "object" || scope === null) return false;
  const fields = scope as Record<string, unknown>;
  return fields.role === role &&
    fields.viewerId === (viewerId ?? null) &&
    fields.tokenId === (tokenId ?? null);
}

export function useModelMarketplaceList(
  params: ModelMarketplaceListParams,
  viewerId: number | undefined,
  options?: Omit<
    UseQueryOptions<ModelMarketplaceListResponse>,
    "queryKey" | "queryFn"
  >,
) {
  const tokenIsValid = Number.isInteger(params.tokenId) && (params.tokenId ?? 0) > 0;
  const { enabled: callerEnabled, ...queryOptions } = options ?? {};
  const scopeIsValid = tokenIsValid && Number.isInteger(viewerId) && (viewerId ?? 0) > 0;
  return useQuery({
    placeholderData: (previousData, previousQuery) =>
      listQueryScopeMatches(previousQuery?.queryKey, "user", viewerId, params.tokenId)
        ? keepPreviousData(previousData)
        : undefined,
    ...queryOptions,
    queryKey: modelMarketplaceListQueryKey("user", viewerId, params),
    queryFn: () => api.get<ModelMarketplaceListResponse>(`/model-marketplace${listQuery(params)}`),
    enabled: scopeIsValid ? (callerEnabled ?? true) : false,
  });
}

export function useAdminModelMarketplaceList(
  params: ModelMarketplaceListParams,
  viewerId: number | undefined,
  options?: Omit<
    UseQueryOptions<AdminModelMarketplaceListResponse>,
    "queryKey" | "queryFn"
  >,
) {
  const { enabled: callerEnabled, ...queryOptions } = options ?? {};
  const scopeIsValid = Number.isInteger(viewerId) && (viewerId ?? 0) > 0;
  return useQuery({
    placeholderData: (previousData, previousQuery) =>
      listQueryScopeMatches(previousQuery?.queryKey, "admin", viewerId, params.tokenId)
        ? keepPreviousData(previousData)
        : undefined,
    ...queryOptions,
    queryKey: modelMarketplaceListQueryKey("admin", viewerId, params),
    queryFn: () => api.get<AdminModelMarketplaceListResponse>(`/admin/model-marketplace${listQuery(params)}`),
    enabled: scopeIsValid ? (callerEnabled ?? true) : false,
  });
}

export interface ModelMarketplaceDetailParams {
  tokenId?: number;
  model: string;
  window: MarketplaceUsageWindow;
  offerRef?: string;
}

function normalizedDetailParams(params: ModelMarketplaceDetailParams) {
  return {
    tokenId: params.tokenId ?? null,
    model: params.model.trim(),
    window: params.window,
    offerRef: params.offerRef?.trim() || null,
  };
}

export function modelMarketplaceDetailQueryKey(
  role: ModelMarketplaceRole,
  viewerId: number | undefined,
  params: ModelMarketplaceDetailParams,
) {
  return [
    "model-marketplace",
    "detail",
    { role, viewerId: viewerId ?? null, ...normalizedDetailParams(params) },
  ] as const;
}

function detailQuery(params: ModelMarketplaceDetailParams) {
  return buildQuery({
    token_id: params.tokenId,
    model: params.model.trim(),
    window: params.window,
    offer_ref: params.offerRef?.trim() || undefined,
  });
}

export function useModelMarketplaceDetail(
  params: ModelMarketplaceDetailParams,
  viewerId: number | undefined,
  options?: Omit<UseQueryOptions<ModelMarketplaceDetailResponse>, "queryKey" | "queryFn">,
) {
  const tokenIsValid = Number.isInteger(params.tokenId) && (params.tokenId ?? 0) > 0;
  const modelIsValid = params.model.trim().length > 0;
  return useQuery({
    ...options,
    queryKey: modelMarketplaceDetailQueryKey("user", viewerId, params),
    queryFn: () => api.get<ModelMarketplaceDetailResponse>(
      `/model-marketplace/detail${detailQuery(params)}`,
    ),
    enabled:
      tokenIsValid &&
      modelIsValid &&
      Number.isInteger(viewerId) &&
      (viewerId ?? 0) > 0 &&
      (options?.enabled ?? true),
  });
}

export function useAdminModelMarketplaceDetail(
  params: ModelMarketplaceDetailParams,
  viewerId: number | undefined,
  options?: Omit<UseQueryOptions<AdminModelMarketplaceDetailResponse>, "queryKey" | "queryFn">,
) {
  const modelIsValid = params.model.trim().length > 0;
  return useQuery({
    ...options,
    queryKey: modelMarketplaceDetailQueryKey("admin", viewerId, params),
    queryFn: () => api.get<AdminModelMarketplaceDetailResponse>(
      `/admin/model-marketplace/detail${detailQuery(params)}`,
    ),
    enabled:
      modelIsValid &&
      Number.isInteger(viewerId) &&
      (viewerId ?? 0) > 0 &&
      (options?.enabled ?? true),
  });
}
