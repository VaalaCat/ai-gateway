export interface User {
  id: number;
  username: string;
  email?: string;
  display_name?: string;
  avatar_url?: string;
  role: number;
  status: number;
  quota: number;
  used_quota: number;
  created_at: number;
  updated_at: number;
  group_id?: number;
  group_name?: string;
}

export interface Token {
  id: number;
  user_id: number;
  key: string;
  name: string;
  status: number;
  expired_at: number;
  models: string;
  template_id?: number;
  trace_enabled: boolean;
  created_at: number;
  updated_at: number;
  allowed_channel_ids?: number[];
}

export interface Channel {
  id: number;
  name: string;
  type: number;
  key: string;
  base_url: string;
  models: string;
  model_mapping: string;
  weight: number;
  priority: number;
  status: number;
  setting: string;
  organization: string;
  api_version: string;
  tag: string;
  remark: string;
  test_model: string;
  auto_ban: number;
  status_code_mapping: string;
  param_override: string;
  header_override: string;
  other_settings: string;
  supported_api_types: string;
  endpoints: string;
  passthrough_enabled: boolean;
  use_legacy_adaptor: boolean;
  system_prompt: string;
  proxy_url: string;
  role_mapping: string;
  system_prompt_in_input?: boolean;
  created_at: number;
  updated_at: number;
}

export interface ChannelTypeMeta {
  id: number;
  name: string;
  i18n_key: string;
}

export interface ChannelSettings {
  force_format?: boolean;
  thinking_to_content?: boolean;
  proxy?: string;
  pass_through_body_enabled?: boolean;
  system_prompt?: string;
  system_prompt_override?: boolean;
}

export type BuiltinToolFallbackPolicy = "drop" | "error" | "passthrough";

export interface ChannelOtherSettings {
  azure_responses_version?: string;
  vertex_key_type?: string;
  openrouter_enterprise?: boolean | null;
  claude_beta_query?: boolean;
  allow_service_tier?: boolean;
  allow_inference_geo?: boolean;
  allow_safety_identifier?: boolean;
  disable_store?: boolean;
  allow_include_obfuscation?: boolean;
  aws_key_type?: string;
  builtin_tool_fallback?: BuiltinToolFallbackPolicy;
  protocol_override?: {
    openai_chat?: 'openai_chat' | 'openai_responses' | 'claude' | 'auto';
    openai_responses?: 'openai_chat' | 'openai_responses' | 'claude' | 'auto';
    claude?: 'openai_chat' | 'openai_responses' | 'claude' | 'auto';
  };
  model_protocol_override?: Array<{
    model: string;
    overrides: Partial<Record<
      'openai_chat' | 'openai_responses' | 'claude' | '*',
      'openai_chat' | 'openai_responses' | 'claude' | 'auto'
    >>;
  }>;
  model_thinking_passthrough?: Array<{
    model_pattern: string;
    send_back_thinking: boolean;
  }>;
}

export interface ModelConfig {
  id: number;
  model_name: string;
  input_price: number;
  output_price: number;
  cache_read_price: number;
  cache_write_price: number;
  status: number;
  created_at: number;
  updated_at: number;
}

export interface AgentAddress {
  url: string;
  tag: string;
}

export interface Agent {
  id: number;
  agent_id: string;
  secret?: string;
  name: string;
  status: number;
  // Legacy field from backend, currently represents effective addresses.
  http_addresses: string;
  configured_http_addresses?: string;
  effective_http_addresses?: string;
  tags: string;
  proxy_url: string;
  last_seen: number;
  created_at: number;
}

export interface OnlineAgentInfo {
  agent_id: string;
  name: string;
  tags: string;
  http_addresses: string;
  configured_http_addresses?: string;
  effective_http_addresses?: string;
  last_seen: number;
}

export interface CacheEntityStats {
  hits: number;
  misses: number;
  evictions: number;
  negative_hits: number;
  size: number;
  capacity: number;
}

export interface AgentRuntime {
  uptime: number;
  cached_tokens: number;
  cached_channels: number;
  cached_models: number;
  active_connections: number;
  version: number;
  master_version: number;
  cache_stats?: Record<string, CacheEntityStats>;
}

export interface AgentDetail extends Agent {
  runtime?: AgentRuntime;
}

export interface AddressProbeResult {
  url: string;
  tag: string;
  reachable: boolean;
  latency_ms: number;
  error: string;
}

export interface ConnectivityResult {
  target_agent_id: string;
  target_name: string;
  results: AddressProbeResult[];
}

export interface ConnectivityReport {
  agent_id: string;
  checked_at: number;
  results: ConnectivityResult[];
}

export interface UsageLog {
  id: number;
  user_id: number;
  token_id: number;
  channel_id: number;
  private_channel_id: number;
  owner_type: "admin" | "private";
  channel_name: string;
  agent_id: string;
  model_name: string;
  prompt_tokens: number;
  completion_tokens: number;
  input_cost: number;
  output_cost: number;
  total_cost: number;
  is_stream: boolean;
  duration: number;
  request_id: string;
  client_ip: string;
  token_name: string;
  upstream_model: string;
  first_response_ms: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  inbound_protocol: string;
  outbound_protocol: string;
  use_legacy: boolean;
  status: number;
  error_message: string;
  other: string;
  has_trace: boolean;
  created_at: number;
}

export interface UsageLogTrace {
  id: number;
  request_id: string;
  inbound_path: string;
  outbound_path: string;
  inbound_headers: string;
  outbound_headers: string;
  inbound_body: string;
  outbound_body: string;
  response_headers: string;
  response_body: string;
  client_response_body: string;
  upstream_status: number;
  created_at: number;
}

export interface BillingOverviewResponse {
  total_cost: number;
  request_count: number;
  success_rate: number;
  active_tokens: number;
}

export interface BillingTokenRow {
  user_id: number;
  token_id: number;
  token_name: string;
  request_count: number;
  success_count: number;
  failed_count: number;
  prompt_tokens: number;
  completion_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  input_cost: number;
  output_cost: number;
  total_cost: number;
  last_used_at: number;
  spark_24h?: number[];
}

export interface BillingTokenDailyRow {
  date: string;
  request_count: number;
  success_count: number;
  failed_count: number;
  prompt_tokens: number;
  completion_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  input_cost: number;
  output_cost: number;
  total_cost: number;
  last_used_at: number;
}

export interface BillingChannelRow {
  channel_id: number;
  channel_name: string;
  channel_type: number;
  request_count: number;
  success_count: number;
  failed_count: number;
  prompt_tokens: number;
  completion_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  input_cost: number;
  output_cost: number;
  total_cost: number;
  last_used_at: number;
  spark_24h?: number[];
}

export interface BillingChannelDailyRow {
  date: string;
  request_count: number;
  success_count: number;
  failed_count: number;
  prompt_tokens: number;
  completion_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  input_cost: number;
  output_cost: number;
  total_cost: number;
  last_used_at: number;
}

export interface BillingDailyResponse<T> {
  items: T[];
}

export interface BillingOverviewQueryParams {
  start_date?: string;
  end_date?: string;
  user_id?: number;
}

export interface BillingTokenQueryParams {
  page?: number;
  page_size?: number;
  start_date?: string;
  end_date?: string;
  token_id?: number;
  user_id?: number;
}

export interface BillingTokenDailyQueryParams {
  start_date?: string;
  end_date?: string;
  user_id?: number;
}

export interface BillingChannelQueryParams {
  page?: number;
  page_size?: number;
  start_date?: string;
  end_date?: string;
  channel_id?: number;
}

export interface BillingChannelDailyQueryParams {
  start_date?: string;
  end_date?: string;
}

export interface BillingRebuildRequest {
  start_date?: string;
  end_date?: string;
  targets?: string[];
}

export interface BillingRebuildSubmitResponse {
  job_id: string;
  total_slices: number;
}

export type RebuildJobStatus = "running" | "succeeded" | "failed" | "canceled";

export interface RebuildJob {
  id: string;
  status: RebuildJobStatus;
  done_slices: number;
  total_slices: number;
  replayed_logs: number;
  started_at: number;
  finished_at?: number;
  error?: string;
}

export interface RebuildJobListResponse {
  jobs: RebuildJob[];
}

export interface ChannelTestResponse {
  success: boolean;
  status_code?: number;
  response?: string;
  error?: string;
  time_cost: number;
  model?: string;
}

export interface ChannelTestParams {
  id: number;
  model?: string;
  endpoint_type?: string;
  stream?: boolean;
  agent_id?: string;
}

export interface PaginatedResponse<T> {
  data: T[];
  total: number;
  page: number;
  page_size: number;
}

export interface PaginatedParams {
  page?: number;
  page_size?: number;
  search?: string;
  [key: string]: string | number | undefined;
}

export interface Stats {
  // Admin fields (optional for normal users)
  users?: number;
  channels?: number;
  models?: number;
  agents?: number;
  connected_agents?: number;
  // Common fields
  tokens: number;
  usage_logs: number;
  total_cost: number;
  // User fields (optional for admin)
  quota?: number;
  used_quota?: number;
}

export interface TrendItem {
  date: string;
  requests: number;
  prompt_tokens: number;
  completion_tokens: number;
  cost: number;
}

export interface TokenTemplate {
  id: number;
  name: string;
  models: string;
  expiry_days: number;
  status: number;
  created_at: number;
  updated_at: number;
  allowed_channel_ids?: number[];
}

export interface UserGroup {
  id: number;
  name: string;
  description: string;
  status: number;
  allowed_channel_ids?: number[];
  models: string;
  created_at: number;
  updated_at: number;
  user_count?: number;
  byok_enabled?: boolean | null;
  byok_max_channels?: number | null;
}

export interface AuthPayload {
  user_id: number;
  username: string;
  display_name?: string;
  avatar_url?: string;
  role: number;
  exp: number;
}

export interface TableStats {
  name: string;
  count: number;
}

export interface SystemInfo {
  version: string;
  go_version: string;
  start_time: number;
  uptime_sec: number;
  memory_alloc: number;
  memory_sys: number;
  num_gc: number;
  num_goroutine: number;
  online_agents: number;
}

export interface SystemStatsResponse {
  tables: TableStats[];
  system: SystemInfo;
}

export interface CleanupPreviewResponse {
  target: string;
  retain_days: number;
  total: number;
  to_delete: number;
}

export interface CleanupResponse {
  deleted: number;
}

export interface AgentRoute {
  id: number;
  source_type: string;
  source_id: number;
  model: string;
  agent_id: string;
  agent_tag: string;
  priority: number;
  created_at: number;
  updated_at: number;
}

export interface AgentRouteOverviewItem extends AgentRoute {
  source_name: string;
  agent_name: string;
}

export interface ClusterEntityStats {
  hits: number;
  misses: number;
  evictions: number;
  negative_hits: number;
  size: number;
  capacity: number;
  hit_rate: number | null;
  util: number | null;
}

export interface AgentCacheSnapshot {
  agent_id: string;
  name: string;
  online: boolean;
  last_seen: number;
  cache_stats?: Record<string, CacheEntityStats>;
}

export interface CacheStatsResponse {
  agents: AgentCacheSnapshot[];
  cluster: Record<string, ClusterEntityStats>;
}

/** cache entity 固定顺序，前后端共享；最后两项 routing 见 spec §3.4。 */
export const CACHE_ENTITY_NAMES = [
  "token",
  "user",
  "channel",
  "model_config",
  "agent",
  "user_group",
  "model_routing",
  "user_routings",
] as const;
export type CacheEntityName = typeof CACHE_ENTITY_NAMES[number];

export interface RoutingMember {
  ref: string;
  priority: number;
  weight: number;
}

export interface ModelRouting {
  id: number;
  name: string;
  scope: 'global' | 'user';
  user_id: number;
  members: RoutingMember[];
  enabled: boolean;
  remark: string;
  created_at: number;
  updated_at: number;
}

export interface RoutingCandidates {
  models: string[];
  global_routings: string[];
}

export interface RoutingNamesResp {
  names: string[];
}

export interface RoutingPreviewNode {
  ref: string;
  kind: 'model' | 'routing' | 'invalid';
  scope?: 'global' | 'user';
  priority: number;
  weight: number;
  effective_pct: number;
  children?: RoutingPreviewNode[];
  error?: 'not_found' | 'disabled' | 'cycle' | 'max_depth';
}

export interface RoutingPreview {
  root: RoutingPreviewNode;
  effective_weights: Array<{ ref: string; percent: number }>;
  warnings: string[];
}

export interface SyncPreviewItem {
  token_id: number;
  token_name: string;
  models_before: string;
  models_after: string;
  channels_before: number[];
  channels_after: number[];
}

export interface SyncPreviewResponse {
  template_id: number;
  template_name: string;
  total: number;
  changed: number;
  unchanged: number;
  items: SyncPreviewItem[];
}

export interface TokenTemplateSyncResponse {
  template_id: number;
  synced: number;
  skipped_unchanged: number;
}
