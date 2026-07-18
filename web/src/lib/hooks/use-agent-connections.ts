"use client";

import { useEffect, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { ConnectionSnapshot, ConnectionSummary, RouteTargetsPage } from "@/lib/types";
import { agentQueryKeys, fetchAgentDetail } from "@/lib/api/agents";
import {
  connectionPollInterval,
  routeTargetsPageMatchesSnapshot,
  mergeConnectionSnapshot,
  type SnapshotMergeState,
} from "@/lib/agent-connection-snapshot";
import { useDocumentVisibility } from "./use-document-visibility";

const emptyMergeState: SnapshotMergeState = { retiredEpochs: [], stale: false };

type ConnectionQueryState = SnapshotMergeState & {
  routeTargetsPage?: RouteTargetsPage;
};

function isAbortError(error: unknown, signal?: AbortSignal) {
  return signal?.aborted || (error instanceof DOMException && error.name === "AbortError");
}

export function useAgentConnections(
  id: number,
  summary?: ConnectionSummary,
  options: { enabled?: boolean; initialSnapshot?: ConnectionSnapshot } = {},
) {
  const visible = useDocumentVisibility();
  const queryClient = useQueryClient();
  const queryKey = useMemo(() => agentQueryKeys.connections(id), [id]);
  const query = useQuery<ConnectionQueryState, Error>({
    queryKey,
    queryFn: async ({ signal }) => {
      try {
        const detail = await fetchAgentDetail(id, signal);
        return {
          ...emptyMergeState,
          current: detail.connection,
          routeTargetsPage: detail.route_targets,
        };
      } catch (error) {
        if (isAbortError(error, signal)) {
          throw error;
        }
        queryClient.setQueryData<ConnectionQueryState>(queryKey, (latest) => ({
          ...(latest ?? emptyMergeState),
          stale: true,
        }));
        throw error;
      }
    },
    initialData: options.initialSnapshot
      ? { ...emptyMergeState, current: options.initialSnapshot }
      : undefined,
    refetchOnMount: options.initialSnapshot ? false : true,
    refetchOnWindowFocus: options.initialSnapshot ? false : true,
    enabled: !!id && (options.enabled ?? true),
    structuralSharing: (oldData, newData) => {
      const latest = oldData as ConnectionQueryState | undefined;
      const incoming = newData as ConnectionQueryState;
      if (!latest || !incoming.current) {
        return incoming;
      }
      const merged = mergeConnectionSnapshot(latest, incoming.current);
      const acceptedIncoming = merged.current?.snapshot_epoch === incoming.current.snapshot_epoch &&
        merged.current.snapshot_seq === incoming.current.snapshot_seq;
      const result: ConnectionQueryState = {
        ...merged,
        routeTargetsPage: acceptedIncoming ? incoming.routeTargetsPage : latest.routeTargetsPage,
      };
      return incoming.stale ? { ...result, stale: true } : result;
    },
    refetchInterval: (activeQuery) =>
      connectionPollInterval(
        activeQuery.state.data?.current,
        visible,
        activeQuery.state.data?.routeTargetsPage,
      ),
  });
  const currentEpoch = query.data?.current?.snapshot_epoch;
  const currentSequence = query.data?.current?.snapshot_seq;

  useEffect(() => {
    if (!id || !options.initialSnapshot) return;
    queryClient.setQueryData<ConnectionQueryState>(queryKey, (current) => {
      if (!current?.current) {
        return { ...emptyMergeState, current: options.initialSnapshot };
      }
      return {
        ...mergeConnectionSnapshot(current, options.initialSnapshot!),
        routeTargetsPage: current.routeTargetsPage,
      };
    });
  }, [id, options.initialSnapshot, queryClient, queryKey]);

  useEffect(() => {
    if (!id || !summary) {
      return;
    }
    queryClient.setQueryData<ConnectionQueryState>(queryKey, (current) => {
      if (!current?.current) {
        return current;
      }
      return { ...mergeConnectionSnapshot(current, summary), routeTargetsPage: current.routeTargetsPage };
    });
  }, [currentEpoch, currentSequence, id, queryClient, queryKey, summary]);

  const acceptedSnapshot = query.data?.current;
  const acceptedRouteTargetsPage = query.data?.routeTargetsPage;
  const routeTargetsPage = acceptedSnapshot && acceptedRouteTargetsPage &&
    routeTargetsPageMatchesSnapshot(acceptedRouteTargetsPage, acceptedSnapshot)
    ? acceptedRouteTargetsPage
    : undefined;
  return {
    ...query,
    data: acceptedSnapshot as ConnectionSnapshot | undefined,
    routeTargetsPage,
    stale: query.data?.stale ?? (query.isError && !isAbortError(query.error)),
  };
}
