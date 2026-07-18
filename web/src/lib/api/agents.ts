import { useEffect } from "react";
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
  type QueryKey,
} from "@tanstack/react-query";
import { api, ApiError, buildQuery } from "./client";
import type {
  AgentListItem,
  AgentOperation,
  AgentOperationRequest,
  AgentPatch,
  AgentRecord,
  AgentDetail,
  ConnectionDiagnostics,
  RouteTargetsPage,
  ManualProbeProgress,
  OperationAck,
  ProbeAck,
  ProbeScope,
  OnlineAgentInfo,
  PaginatedResponse,
  PaginatedParams,
  InflightSnapshot,
  AllInflightResponse,
} from "@/lib/types";

export type { InflightSnapshot, GlobalInflightRow, AllInflightResponse } from "@/lib/types";

export const agentQueryKeys = {
  all: ["agents"] as const,
  lists: () => ["agents", "list"] as const,
  list: (params: PaginatedParams = {}) => ["agents", "list", params] as const,
  item: (id: number) => ["agents", id] as const,
  detail: (id: number) => ["agents", id, "detail"] as const,
  connections: (id: number) => ["agents", id, "connections"] as const,
  targets: (id: number) => ["agents", id, "connections", "targets"] as const,
  targetsPage: (
    id: number,
    limit: number,
    snapshot?: Pick<RouteTargetsPage, "snapshot_epoch" | "snapshot_seq">,
  ) => ["agents", id, "connections", "targets", {
    limit,
    ...(snapshot ? {
      snapshot_epoch: snapshot.snapshot_epoch,
      snapshot_seq: snapshot.snapshot_seq,
    } : {}),
  }] as const,
  diagnostics: (id: number) =>
    ["agents", id, "connections", "diagnostics"] as const,
  progressRoot: (id: number) => ["agents", id, "connectivity", "progress"] as const,
  progress: (id: number, probeID: string) =>
    ["agents", id, "connectivity", "progress", probeID] as const,
};

export function fetchAgents(params: PaginatedParams = {}, signal?: AbortSignal) {
  return api.request<PaginatedResponse<AgentListItem>>(`/admin/agents${buildQuery(params)}`, {
    signal,
  });
}

export function agentListPollInterval(
  rows?: readonly Pick<AgentListItem, "connection">[],
) {
  return rows?.some((row) => row.connection.relay.convergence === "applying")
    ? 3_000
    : 15_000;
}

export function fetchAgentDetail(id: number, signal?: AbortSignal) {
  return api.request<AgentDetail>(`/admin/agents/${id}/detail`, { signal });
}

export function fetchAgentRouteTargets(
  id: number,
  params: {
    cursor?: string;
    limit?: number;
    expected_snapshot_epoch?: string;
    expected_snapshot_seq?: number;
  } = {},
  signal?: AbortSignal,
) {
  return api.request<RouteTargetsPage>(
    `/admin/agents/${id}/connections/targets${buildQuery(params)}`,
    { signal },
  );
}

export function fetchAgentConnectionDiagnostics(id: number, signal?: AbortSignal) {
  return api.request<ConnectionDiagnostics>(
    `/admin/agents/${id}/connections/diagnostics`,
    { signal },
  );
}

export function enqueueConnectivityProbe(
  id: number,
  request: {
    scope?: ProbeScope;
    expected_epoch: string;
    expected_control_generation?: number;
    expected_relay_generation?: number;
  },
  signal?: AbortSignal,
) {
  return api.request<ProbeAck>(`/admin/agents/${id}/connectivity`, {
    method: "POST",
    body: JSON.stringify(request),
    signal,
  });
}

export function fetchProbeProgress(id: number, probeID: string, signal?: AbortSignal) {
  return api.request<ManualProbeProgress>(
    `/admin/agents/${id}/connectivity${buildQuery({ probe_id: probeID })}`,
    { signal },
  );
}

export function runAgentOperation(
  id: number,
  operation: AgentOperation,
  request: AgentOperationRequest,
  signal?: AbortSignal,
) {
  return api.request<OperationAck>(`/admin/agents/${id}/operations/${operation}`, {
    method: "POST",
    body: JSON.stringify(request),
    signal,
  });
}

export function patchAgent(id: number, patch: AgentPatch, signal?: AbortSignal) {
  return api.request<AgentRecord>(`/admin/agents/${id}`, {
    method: "PUT",
    body: JSON.stringify(patch),
    signal,
  });
}

export function useAllAgentsInflight() {
  return useQuery({
    queryKey: ["agents", "inflight-all"],
    queryFn: () => api.get<AllInflightResponse>(`/admin/agents/inflight/all`),
    refetchInterval: 5000,
  });
}

export function useInterruptInflight() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { agent_id: number; id: number }) =>
      api.post<{ interrupted: boolean }>(`/admin/agents/inflight/interrupt`, body),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: ["agents", "inflight-all"] });
      queryClient.invalidateQueries({ queryKey: ["agents", variables.agent_id, "inflight"] });
    },
  });
}

export interface GoroutineDump {
  count: number;
  dump: string;
}

export function useAgents(params: PaginatedParams = {}) {
  return useQuery({
    queryKey: agentQueryKeys.list(params),
    queryFn: ({ signal }) => fetchAgents(params, signal),
    refetchInterval: (query) => agentListPollInterval(query.state.data?.data),
  });
}

export function useAgent(id: number) {
  return useQuery({
    queryKey: agentQueryKeys.item(id),
    queryFn: ({ signal }) => api.request<AgentRecord>(`/admin/agents/${id}`, { signal }),
    enabled: !!id,
  });
}

export function useAgentDetail(id: number, options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: agentQueryKeys.detail(id),
    queryFn: ({ signal }) => fetchAgentDetail(id, signal),
    enabled: !!id && (options.enabled ?? true),
  });
}

const MAX_ROUTE_TARGET_QUERY_KEYS = 20;

function pruneAgentRouteTargetQueries(
  queryClient: QueryClient,
  id: number,
  currentQueryKey: QueryKey,
) {
  const queries = queryClient.getQueryCache().findAll({
    queryKey: agentQueryKeys.targets(id),
  });
  const excess = queries.length - MAX_ROUTE_TARGET_QUERY_KEYS;
  if (excess <= 0) return;
  const current = queryClient.getQueryCache().find({
    queryKey: currentQueryKey,
    exact: true,
  });
  const inactive = queries.filter(
    (candidate) => candidate !== current && candidate.getObserversCount() === 0,
  );
  for (const candidate of inactive.slice(0, excess)) {
    queryClient.getQueryCache().remove(candidate);
  }
}

export function useAgentRouteTargets(
  id: number,
  limit = 20,
  options: {
    enabled?: boolean;
    initialPage?: RouteTargetsPage;
    snapshot?: Pick<RouteTargetsPage, "snapshot_epoch" | "snapshot_seq">;
  } = {},
) {
  const initialPage = options.initialPage && (
    !options.snapshot ||
    options.initialPage.snapshot_epoch === options.snapshot.snapshot_epoch &&
    options.initialPage.snapshot_seq === options.snapshot.snapshot_seq
  ) ? options.initialPage : undefined;
  const queryClient = useQueryClient();
  const queryKey = agentQueryKeys.targetsPage(id, limit, options.snapshot ?? initialPage);
  const query = useInfiniteQuery({
    queryKey,
    queryFn: ({ pageParam, signal }) =>
      fetchAgentRouteTargets(id, {
        cursor: pageParam,
        limit,
        expected_snapshot_epoch: options.snapshot?.snapshot_epoch,
        expected_snapshot_seq: options.snapshot?.snapshot_seq,
      }, signal),
    initialPageParam: "",
    initialData: initialPage
      ? { pages: [initialPage], pageParams: [""] }
      : undefined,
    staleTime: initialPage ? 30_000 : 0,
    gcTime: options.snapshot ? 60_000 : undefined,
    refetchOnMount: initialPage ? false : true,
    maxPages: 5,
    getNextPageParam: (lastPage) => lastPage.next_cursor,
    enabled: !!id && (options.enabled ?? true),
  });
  useEffect(() => {
    pruneAgentRouteTargetQueries(queryClient, id, queryKey);
  }, [id, queryClient, queryKey]);
  const snapshotConflict = query.error instanceof ApiError &&
    query.error.status === 409 &&
    ["route_targets_cursor_epoch_changed", "route_targets_cursor_snapshot_changed"].includes(
      String(query.error.body?.code ?? ""),
    );
  useEffect(() => {
    if (!snapshotConflict) return;
    void Promise.all([
      queryClient.invalidateQueries({ queryKey: agentQueryKeys.detail(id), exact: true }),
      queryClient.invalidateQueries({ queryKey: agentQueryKeys.connections(id), exact: true }),
    ]);
  }, [id, query.errorUpdatedAt, queryClient, snapshotConflict]);
  return { ...query, snapshotConflict };
}

export function useAgentConnectionDiagnostics(id: number, options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: agentQueryKeys.diagnostics(id),
    queryFn: ({ signal }) => fetchAgentConnectionDiagnostics(id, signal),
    enabled: !!id && (options.enabled ?? true),
  });
}

export function probeProgressPollInterval(
  progress: Pick<ManualProbeProgress, "state"> | undefined,
): number | false {
  return progress && ["completed", "failed", "cancelled"].includes(progress.state)
    ? false
    : 3_000;
}

export function useAgentProbeProgress(id: number, probeID: string) {
  return useQuery({
    queryKey: agentQueryKeys.progress(id, probeID),
    queryFn: ({ signal }) => fetchProbeProgress(id, probeID, signal),
    enabled: !!id && !!probeID,
    refetchInterval: (query) => probeProgressPollInterval(query.state.data),
  });
}

export function useEnqueueConnectivityProbe() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      request,
    }: {
      id: number;
      request: {
        scope?: ProbeScope;
        expected_epoch: string;
        expected_control_generation?: number;
        expected_relay_generation?: number;
      };
    }) => enqueueConnectivityProbe(id, request),
    onSuccess: async (_ack, variables) => {
      await invalidateAgentConnectionViews(queryClient, variables.id);
    },
  });
}

async function invalidateAgentConnectionViews(
  queryClient: ReturnType<typeof useQueryClient>,
  id: number,
) {
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: agentQueryKeys.lists() }),
    queryClient.invalidateQueries({ queryKey: agentQueryKeys.detail(id), exact: true }),
    queryClient.invalidateQueries({ queryKey: agentQueryKeys.connections(id), exact: true }),
    queryClient.invalidateQueries({ queryKey: agentQueryKeys.targets(id) }),
    queryClient.invalidateQueries({ queryKey: agentQueryKeys.progressRoot(id) }),
  ]);
}

export function useAgentOperation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      operation,
      request,
    }: {
      id: number;
      operation: AgentOperation;
      request: AgentOperationRequest;
    }) => runAgentOperation(id, operation, request),
    onSuccess: async (_ack, variables) => {
      await invalidateAgentConnectionViews(queryClient, variables.id);
    },
  });
}

export function useOnlineAgents(options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: ["agents", "online"],
    queryFn: () => api.get<OnlineAgentInfo[]>("/admin/agents/online"),
    refetchInterval: 30000,
    enabled: options.enabled ?? true,
  });
}

export function useCreateAgent() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<AgentRecord>) =>
      api.post<AgentRecord>("/admin/agents", body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["agents"] });
    },
  });
}

export function useUpdateAgent() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: number } & AgentPatch) =>
      patchAgent(id, body),
    onSuccess: async (_agent, variables) => {
      await invalidateAgentConnectionViews(queryClient, variables.id);
    },
  });
}

export function useDeleteAgent() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete<void>(`/admin/agents/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["agents"] });
    },
  });
}

export function useGenerateEnrollmentToken() {
  return useMutation({
    mutationFn: (body: { ttl: number }) =>
      api.post<{ enrollment_token: string; expires_at: number }>("/admin/agents/enrollment-token", body),
  });
}

export function useFullSyncAgents() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { agent_ids?: string[]; all?: boolean }) =>
      api.post<{
        results: Array<{
          agent_id: string;
          success: boolean;
          version?: number;
          duration_ms?: number;
          error?: string;
        }>;
      }>("/admin/agents/full-sync", body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["agents"] });
    },
  });
}

export function useAgentInflight(id: number) {
  return useQuery({
    queryKey: ["agents", id, "inflight"],
    queryFn: () => api.get<InflightSnapshot[]>(`/admin/agents/inflight?id=${id}`),
    enabled: !!id,
    refetchInterval: 5000,
  });
}

export function useAgentGoroutines() {
  return useMutation({
    mutationFn: (id: number) =>
      api.get<GoroutineDump>(`/admin/agents/goroutines?id=${id}`),
  });
}
