import { describe, expect, it } from "vitest";
import type {
  AgentListItem,
  AgentRecord,
  ConnectionSnapshot,
  ConnectionSummary,
  RouteTargetsPage,
} from "@/lib/types";
import {
  connectionPollInterval,
  routeTargetsPageMatchesSnapshot,
  routeTargetsIdentity,
  mergeConnectionSnapshot,
  type SnapshotMergeState,
} from "./agent-connection-snapshot";

function snapshot(
  epoch: string,
  seq: number,
  observedAt = seq,
  overrides: Partial<ConnectionSnapshot> = {},
): ConnectionSnapshot {
  return {
    version: "v1",
    snapshot_epoch: epoch,
    snapshot_seq: seq,
    observed_at: observedAt,
    agent_id: "agent-a",
    admin_status: 1,
    control: {
      state: "connected",
      health: "healthy",
      reason_codes: [],
      session_generation: 3,
      connected_at: 1,
      heartbeat_at: 2,
      runtime_reported_at: 3,
      last_seen: 3,
    },
    relay: {
      support: "supported",
      config: "configured",
      availability: "ready",
      accepting_new_streams: true,
      convergence: "converged",
      desired: {
        mode: "inherit",
        configured_uri: "",
        effective_uri: "wss://relay.example/ws",
        desired_generation: 4,
      },
      active: {
        uri: "wss://relay.example/ws",
        active_generation: 4,
        session_generation: 5,
        connected_at: 2,
        streams: 1,
        retry_at: 0,
      },
      recent_errors: [],
    },
    direct: {
      summary: {
        state: "reachable",
        reachable: 1,
        degraded: 0,
        unreachable: 0,
        stale: 0,
        total: 1,
      },
      targets: {
        "agent-b": {
          target_agent_id: "agent-b",
          target_name: "Agent B",
          addresses: [{ url: "https://agent-b.example", tag: "wan" }],
          network: "reachable",
          identity: "verified",
          eligible: true,
          checking: false,
          probe_generation: 7,
          address_fingerprint: "fingerprint-b",
          checked_at: 3,
          latency_ms: 12,
          recent_errors: [],
        },
      },
    },
    target_summaries: {
      direct: {
        state: "reachable",
        reachable: 1,
        degraded: 0,
        unreachable: 0,
        stale: 0,
        total: 1,
      },
      relay: {
        state: "reachable",
        reachable: 1,
        unreachable: 0,
        unavailable: 0,
        unknown: 0,
        unsupported: 0,
        stale: 0,
        total: 1,
      },
    },
    allowed_operations: [{ operation: "probe", allowed: true }],
    ...overrides,
  };
}

function state(current?: ConnectionSnapshot): SnapshotMergeState {
  return { current, retiredEpochs: [], stale: false };
}

describe("routeTargetsIdentity", () => {
  it("uses the Route Targets content generation independently of the parent read sequence", () => {
    const current = snapshot("epoch-a", 20);
    const page: RouteTargetsPage = {
      snapshot_epoch: "epoch-a",
      snapshot_seq: 7,
      observed_at: current.observed_at,
      summaries: current.target_summaries,
      data: [],
      limit: 20,
    };

    expect(routeTargetsIdentity(page)).toEqual({ snapshot_epoch: "epoch-a", snapshot_seq: 7 });
    expect(routeTargetsPageMatchesSnapshot(page, current)).toBe(true);
  });

  it("matches a page to its parent by epoch and observation time", () => {
    const current = snapshot("epoch-a", 20);
    const page = {
      snapshot_epoch: "epoch-a",
      snapshot_seq: 7,
      observed_at: current.observed_at,
    };

    expect(routeTargetsPageMatchesSnapshot(page, current)).toBe(true);
    expect(routeTargetsPageMatchesSnapshot({ ...page, observed_at: 19 }, current)).toBe(false);
    expect(routeTargetsPageMatchesSnapshot({ ...page, snapshot_epoch: "epoch-b" }, current)).toBe(false);
  });
});

describe("mergeConnectionSnapshot", () => {
  it("accepts a newer sequence in the same epoch", () => {
    const incoming = snapshot("epoch-a", 2);
    const result = mergeConnectionSnapshot(state(snapshot("epoch-a", 1)), incoming);

    expect(result.current).toBe(incoming);
    expect(result.stale).toBe(false);
  });

  it("rejects an older sequence in the same epoch", () => {
    const current = snapshot("epoch-a", 2);
    const result = mergeConnectionSnapshot(state(current), snapshot("epoch-a", 1));

    expect(result.current).toBe(current);
    expect(result.retiredEpochs).toEqual([]);
  });

  it("accepts an equal sequence without moving backwards", () => {
    const current = snapshot("epoch-a", 2);
    const result = mergeConnectionSnapshot(state(current), snapshot("epoch-a", 2));

    expect(result.current?.snapshot_seq).toBe(2);
  });

  it("accepts a restart epoch observed after the current snapshot", () => {
    const incoming = snapshot("epoch-b", 1, 20);
    const result = mergeConnectionSnapshot(state(snapshot("epoch-a", 9, 10)), incoming);

    expect(result.current).toBe(incoming);
    expect(result.retiredEpochs).toEqual(["epoch-a"]);
  });

  it("rejects a new epoch whose observation is older", () => {
    const current = snapshot("epoch-a", 9, 20);
    const result = mergeConnectionSnapshot(state(current), snapshot("epoch-b", 1, 19));

    expect(result.current).toBe(current);
    expect(result.retiredEpochs).toEqual([]);
  });

  it("rejects a late snapshot from a retired epoch", () => {
    const current = snapshot("epoch-b", 2, 30);
    const result = mergeConnectionSnapshot(
      { current, retiredEpochs: ["epoch-a"], stale: false },
      snapshot("epoch-a", 99, 40),
    );

    expect(result.current).toBe(current);
    expect(result.retiredEpochs).toEqual(["epoch-a"]);
  });

  it("bounds retired epochs to the four most recent entries", () => {
    let result = state(snapshot("epoch-0", 1, 1));
    for (let index = 1; index <= 6; index += 1) {
      result = mergeConnectionSnapshot(result, snapshot(`epoch-${index}`, 1, index + 1));
    }

    expect(result.current?.snapshot_epoch).toBe("epoch-6");
    expect(result.retiredEpochs).toEqual([
      "epoch-5",
      "epoch-4",
      "epoch-3",
      "epoch-2",
    ]);
  });

  it("keeps targets and detail when an equal-sequence payload omits them", () => {
    const current = snapshot("epoch-a", 2);
    const withoutDetails = {
      ...snapshot("epoch-a", 2),
      relay: {
        support: "supported",
        config: "configured",
        availability: "ready",
        accepting_new_streams: true,
        convergence: "converged",
      },
      direct: { summary: current.direct.summary },
      allowed_operations: undefined,
    } as unknown as ConnectionSnapshot;

    const result = mergeConnectionSnapshot(state(current), withoutDetails);

    expect(result.current?.direct.targets).toEqual(current.direct.targets);
    expect(result.current?.relay.desired).toEqual(current.relay.desired);
    expect(result.current?.allowed_operations).toEqual(current.allowed_operations);
  });

  it("lets a same-sequence payload add missing target detail", () => {
    const incoming = snapshot("epoch-a", 2);
    const current = snapshot("epoch-a", 2, 2, {
      direct: { summary: incoming.direct.summary },
    });

    const result = mergeConnectionSnapshot(state(current), incoming);

    expect(result.current?.direct.targets).toEqual(incoming.direct.targets);
  });

  it("merges a newer list summary without overwriting detail fields", () => {
    const current = snapshot("epoch-a", 2);
    const summary: ConnectionSummary = {
      version: "v1",
      snapshot_epoch: "epoch-a",
      snapshot_seq: 3,
      observed_at: 3,
      control: { ...current.control, heartbeat_at: 30 },
      relay: {
        support: "supported",
        config: "configured",
        availability: "ready",
        accepting_new_streams: true,
        convergence: "converged",
        streams: 8,
      },
      direct: { ...current.direct.summary, reachable: 2, total: 2 },
      targets: current.target_summaries,
    };

    const result = mergeConnectionSnapshot(state(current), summary);

    expect(result.current).toMatchObject({
      snapshot_seq: 3,
      control: { heartbeat_at: 30 },
      relay: { active: { streams: 8 } },
      direct: { summary: { reachable: 2, total: 2 } },
    });
    expect(result.current?.direct.targets).toEqual(current.direct.targets);
    expect(result.current?.relay.desired).toEqual(current.relay.desired);
  });
});

describe("connectionPollInterval", () => {
  it("uses the stable interval for a healthy snapshot", () => {
    expect(connectionPollInterval(snapshot("epoch-a", 1), true)).toBe(15_000);
  });

  it.each([
    snapshot("epoch-a", 1, 1, {
      control: { ...snapshot("epoch-a", 1).control, health: "degraded" },
    }),
    snapshot("epoch-a", 1, 1, {
      relay: { ...snapshot("epoch-a", 1).relay, convergence: "applying" },
    }),
    snapshot("epoch-a", 1, 1, {
      target_summaries: {
        ...snapshot("epoch-a", 1).target_summaries,
        direct: { ...snapshot("epoch-a", 1).target_summaries.direct, state: "checking" },
      },
    }),
  ])("uses the transient interval for checking or degraded state", (value) => {
    expect(connectionPollInterval(value, true)).toBe(3_000);
  });

  it("uses the stable interval before the first snapshot", () => {
    expect(connectionPollInterval(undefined, true)).toBe(15_000);
  });

  it("uses the stable interval for converged stale diagnostics with a current target page", () => {
    const value = snapshot("epoch-a", 1);
    value.target_summaries.relay = {
      ...value.target_summaries.relay,
      state: "unknown",
      stale: 1,
      total: 1,
    };
    const page: RouteTargetsPage = {
      snapshot_epoch: value.snapshot_epoch,
      snapshot_seq: 7,
      observed_at: value.observed_at,
      summaries: value.target_summaries,
      data: [],
      limit: 20,
    };

    expect(connectionPollInterval(value, true, page)).toBe(15_000);
  });

  it("uses the transient interval while a non-empty target page is catching up", () => {
    const value = snapshot("epoch-a", 2);
    value.target_summaries.relay = {
      ...value.target_summaries.relay,
      stale: 1,
      total: 1,
    };
    const olderPage: RouteTargetsPage = {
      snapshot_epoch: value.snapshot_epoch,
      snapshot_seq: 7,
      observed_at: value.observed_at - 1,
      summaries: value.target_summaries,
      data: [],
      limit: 20,
    };

    expect(connectionPollInterval(value, true, olderPage)).toBe(3_000);
  });

  it("does not poll an empty target set on the transient interval", () => {
    expect(connectionPollInterval(snapshot("epoch-a", 1), true, undefined)).toBe(15_000);
  });

  it("pauses polling while the document is hidden", () => {
    expect(connectionPollInterval(snapshot("epoch-a", 1), false)).toBe(false);
  });
});

describe("agent wire records", () => {
  it("keeps database records separate from list projections", () => {
    const record = {
      id: 1,
      agent_id: "agent-a",
      secret: "secret",
      name: "Agent A",
      status: 1,
      http_addresses: "[]",
      tags: "wan",
      proxy_url: "",
      relay_mode: "custom",
      relay_uri: "wss://relay.example/ws",
      peer_route_mode: "direct_first",
      last_seen: 10,
      created_at: 1,
    } satisfies AgentRecord;
    const value = snapshot("epoch-a", 1);
    const listItem = {
      id: 1,
      agent_id: "agent-a",
      name: "Agent A",
      status: 1,
      tags: "wan",
      proxy_url: "",
      relay_mode: "custom",
      peer_route_mode: "direct_first",
      last_seen: 10,
      created_at: 1,
      connection: {
        version: "v1",
        snapshot_epoch: value.snapshot_epoch,
        snapshot_seq: value.snapshot_seq,
        observed_at: value.observed_at,
        control: value.control,
        relay: {
          support: value.relay.support,
          config: value.relay.config,
          availability: value.relay.availability,
          accepting_new_streams: value.relay.accepting_new_streams,
          convergence: value.relay.convergence,
          streams: value.relay.active.streams,
        },
        direct: value.direct.summary,
        targets: value.target_summaries,
      },
    } satisfies AgentListItem;

    expect(record.relay_uri).toBe("wss://relay.example/ws");
    expect(listItem.connection.snapshot_epoch).toBe("epoch-a");
    expect("connection" in record).toBe(false);
    expect("relay_uri" in listItem).toBe(false);
  });
});
