import type { ConnectionSnapshot, ConnectionSummary, RouteTargetsPage } from "@/lib/types";

const maxRetiredEpochs = 4;
const stablePollInterval = 15_000;
const transientPollInterval = 3_000;

export type SnapshotMergeState = {
  current?: ConnectionSnapshot;
  retiredEpochs: readonly string[];
  stale: boolean;
};

export function routeTargetsIdentity(page: RouteTargetsPage) {
	return {
		snapshot_epoch: page.snapshot_epoch,
		snapshot_seq: page.snapshot_seq,
	};
}

export function routeTargetsPageMatchesSnapshot(
	page: Pick<RouteTargetsPage, "snapshot_epoch" | "observed_at">,
	snapshot: ConnectionSnapshot,
) {
	return page.snapshot_epoch === snapshot.snapshot_epoch &&
		page.observed_at === snapshot.observed_at;
}

function isConnectionSnapshot(
  snapshot: ConnectionSnapshot | ConnectionSummary,
): snapshot is ConnectionSnapshot {
  return "agent_id" in snapshot;
}

function richerArray<T>(current: T[] | undefined, incoming: T[] | undefined): T[] {
  if (!incoming || incoming.length < (current?.length ?? 0)) {
    return current ?? [];
  }
  return incoming;
}

function mergeEqualSnapshot(
  current: ConnectionSnapshot,
  incoming: ConnectionSnapshot,
): ConnectionSnapshot {
  const currentTargets = current.direct.targets;
  const incomingTargets = incoming.direct?.targets;
  const targets = incomingTargets
    ? { ...currentTargets, ...incomingTargets }
    : currentTargets;

  return {
    ...current,
    ...incoming,
    control: { ...current.control, ...incoming.control },
    relay: {
      ...current.relay,
      ...incoming.relay,
      desired: incoming.relay?.desired ?? current.relay.desired,
      active: incoming.relay?.active ?? current.relay.active,
      recent_errors: richerArray(
        current.relay.recent_errors,
        incoming.relay?.recent_errors,
      ),
    },
		direct: {
      generation: incoming.direct?.generation ?? current.direct.generation,
      summary: incoming.direct?.summary ?? current.direct.summary,
      ...(targets ? { targets } : {}),
		},
		target_summaries: incoming.target_summaries ?? current.target_summaries,
    allowed_operations: richerArray(
      current.allowed_operations,
      incoming.allowed_operations,
    ),
  };
}

function mergeSummary(
  current: ConnectionSnapshot,
  incoming: ConnectionSummary,
): ConnectionSnapshot {
  return {
    ...current,
    version: incoming.version,
    snapshot_epoch: incoming.snapshot_epoch,
    snapshot_seq: incoming.snapshot_seq,
    observed_at: incoming.observed_at,
    control: incoming.control,
    relay: {
      ...current.relay,
      support: incoming.relay.support,
      config: incoming.relay.config,
      availability: incoming.relay.availability,
      accepting_new_streams: incoming.relay.accepting_new_streams,
      convergence: incoming.relay.convergence,
      active: { ...current.relay.active, streams: incoming.relay.streams },
    },
		direct: { ...current.direct, summary: incoming.direct },
		target_summaries: incoming.targets,
  };
}

export function mergeConnectionSnapshot(
  state: SnapshotMergeState,
  incoming: ConnectionSnapshot | ConnectionSummary,
): SnapshotMergeState {
  const current = state.current;
  if (!current) {
    return isConnectionSnapshot(incoming)
      ? { current: incoming, retiredEpochs: state.retiredEpochs, stale: false }
      : state;
  }

  if (incoming.snapshot_epoch === current.snapshot_epoch) {
    if (incoming.snapshot_seq < current.snapshot_seq) {
      return state;
    }
    const next = isConnectionSnapshot(incoming)
      ? incoming.snapshot_seq === current.snapshot_seq
        ? mergeEqualSnapshot(current, incoming)
        : incoming
      : mergeSummary(current, incoming);
    return { ...state, current: next, stale: false };
  }

  if (
    !isConnectionSnapshot(incoming) ||
    state.retiredEpochs.includes(incoming.snapshot_epoch) ||
    incoming.observed_at < current.observed_at
  ) {
    return state;
  }

  const retiredEpochs = [
    current.snapshot_epoch,
    ...state.retiredEpochs.filter((epoch) => epoch !== current.snapshot_epoch),
  ].slice(0, maxRetiredEpochs);
  return { current: incoming, retiredEpochs, stale: false };
}

export function connectionPollInterval(
  snapshot: ConnectionSnapshot | undefined,
  visible: boolean,
  routeTargetsPage?: Pick<RouteTargetsPage, "snapshot_epoch" | "observed_at">,
): number | false {
  if (!visible) {
    return false;
  }
  if (!snapshot) {
    return stablePollInterval;
  }
  const hasRouteTargets = snapshot.target_summaries.direct.total > 0 ||
    snapshot.target_summaries.relay.total > 0;
  const routeTargetsCatchingUp = hasRouteTargets && !!routeTargetsPage &&
    !routeTargetsPageMatchesSnapshot(routeTargetsPage, snapshot);
  const transient =
    snapshot.control.health === "degraded" ||
    snapshot.relay.convergence === "applying" ||
    snapshot.relay.convergence === "degraded" ||
		snapshot.target_summaries.direct.state === "checking" ||
		snapshot.target_summaries.direct.state === "degraded" ||
		snapshot.target_summaries.relay.state === "checking" ||
		snapshot.target_summaries.relay.state === "degraded" ||
		routeTargetsCatchingUp;
  return transient ? transientPollInterval : stablePollInterval;
}
