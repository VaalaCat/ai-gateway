import { useQuery, type UseQueryOptions } from "@tanstack/react-query";
import { api, ApiError, buildQuery } from "./client";
import type { RateLimitHit } from "@/lib/types";

export function isLogDatabaseUnavailable(error: unknown) {
  return error instanceof ApiError && error.body?.code === "LogDatabaseUnavailable";
}

export interface APIBodyCapture {
  captured?: boolean;
  status?: string;
  skip_reason?: string;
  data?: string;
  captured_bytes?: number;
  total_bytes?: number;
  truncated?: boolean;
}

export interface APIRequestLog {
  id: number;
  request_id: string;
  user_id?: number;
  client_ip?: string;
  api_service_id: number;
  api_service_name: string;
  api_route_id: number;
  api_route_name: string;
  api_upstream_id?: number;
  api_upstream_name?: string;
  token_id: number;
  token_name: string;
  protocol: string;
  method: string;
  subpath: string;
  source_agent_id?: string;
  execution_agent_id?: string;
  agent_route_id?: number;
  agent_route_path?: string;
  status_code: number;
  duration_ms: number;
  first_byte_ms: number;
  request_bytes: number;
  response_bytes: number;
  websocket_close_code: number | null;
  provider_dispatch_known?: boolean;
  provider_dispatched?: boolean;
  quota_gate_decision: string;
  error_stage: string;
  error_code: string;
  service_missing_at_settlement?: boolean;
  rate_limit_decision: string;
  rate_limit_wait_ms: number;
  rate_limit_reason?: string;
  rate_limit_hits?: RateLimitHit[];
  unit_price: number;
  total_cost: number;
  created_at: number;
}

export interface APIRequestTrace {
  id: number;
  request_id: string;
  source_request_headers?: Record<string, string[]>;
  source_request_trailers?: Record<string, string[]>;
  source_request_headers_truncated?: boolean;
  source_request_trailers_truncated?: boolean;
  source_request_body?: APIBodyCapture;
  request_headers?: Record<string, string[]>;
  request_trailers?: Record<string, string[]>;
  request_headers_truncated?: boolean;
  request_trailers_truncated?: boolean;
  request_body?: APIBodyCapture;
  response_headers?: Record<string, string[]>;
  response_trailers?: Record<string, string[]>;
  response_headers_truncated?: boolean;
  response_trailers_truncated?: boolean;
  response_body?: APIBodyCapture;
  created_at: number;
}

export interface APIRequestLogParams {
  page?: number;
  page_size?: number;
  api_service_id?: number;
  api_route_id?: number;
  api_upstream_id?: number;
  token_id?: number;
  status_code?: number;
  request_id?: string;
  start?: number;
  end?: number;
}

interface Page<T> {
  data: T[];
  total: number;
  page: number;
  page_size: number;
}

export type APIRequestLogScope = "admin" | "portal";

function logEndpoint(scope: APIRequestLogScope) {
  return scope === "admin" ? "/admin/api-request-logs" : "/api-request-logs";
}

function traceEndpoint(scope: APIRequestLogScope) {
  return scope === "admin" ? "/admin/api-request-traces" : "/api-request-traces";
}

export function useAPIRequestLogs(
  params: APIRequestLogParams = {},
  scope: APIRequestLogScope = "admin",
  options?: Omit<UseQueryOptions<Page<APIRequestLog>>, "queryKey" | "queryFn">,
) {
  return useQuery({
    queryKey: ["api-request-logs", scope, params],
    queryFn: () => api.get<Page<APIRequestLog>>(`${logEndpoint(scope)}${buildQuery(params)}`),
    ...options,
  });
}

export function useAPIRequestTrace(requestID: string | null, scope: APIRequestLogScope = "admin") {
  return useQuery({
    queryKey: ["api-request-trace", scope, requestID],
    queryFn: () => api.get<APIRequestTrace>(`${traceEndpoint(scope)}${buildQuery({ request_id: requestID })}`),
    enabled: Boolean(requestID),
  });
}
