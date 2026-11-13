import { describe, expect, it } from "vitest";
import type { AgentQueueRow, BreakerBoardResponse, DeliveryBoardResponse, LimiterUsageResponse } from "@/lib/types";
import { collectMonitoringAgentSamples, retainMonitoringAgents } from "./monitoring-data";

function limiterResponse(): LimiterUsageResponse {
  return {
    successful_agent_ids: [1, 2], failed_agents: [],
    buckets: [{ limiter_id: 1, name: "shared rule", bucket: "shared", metric: "requests", key_by: "none", occupied: 9, capacity: 20, waiters: 3, window_end_ms: 0,
      per_agent: [
        { agent_id: 1, agent_name: "alpha", occupied: 4, capacity: 10, waiters: 1 },
        { agent_id: 2, agent_name: "beta", occupied: 5, capacity: 10, waiters: 2 },
      ] }],
  };
}

function breakerResponse(): BreakerBoardResponse {
  return { successful_agent_ids: [1, 2], failed_agents: [], channels: [{ source: "llm", channel_id: 8, worst_state: "open", open_agents: 1, total_agents: 2,
    agents: [
      { agent_id: 1, agent_name: "alpha", state: "closed", failures: 0, successes: 3, failure_rate: 0, remaining_ms: 0 },
      { agent_id: 2, agent_name: "beta", state: "open", failures: 3, successes: 0, failure_rate: 1, remaining_ms: 1000 },
    ] }] };
}

function deliveryResponse(): DeliveryBoardResponse {
  const queue = (agent_id: number, agent_name: string): AgentQueueRow => ({ agent_id, agent_name, store_len: 1, retry_len: 0, total_bytes: 80, oldest_ts: 1, last_success_at: 1, last_error: "", inflight: 0, shared_usage_dropped: 2, api_trace_slimmed: 0,
    items: [{ request_id: `req-${agent_id}`, bytes: 80, attempts: 1, degrade_level: 0, next_at: 0 }] });
  return { successful_agent_ids: [1, 2], failed_agents: [], agents: [queue(1, "alpha"), queue(2, "beta")], log_backlog: { pending: 0, retry: 0, inflight: 0, bytes: 0, oldest_seconds: 0, dropped: 0, last_error: "", schema_ready: true } };
}

describe("monitoring per-node history", () => {
  it.each([
    ["limiter", limiterResponse], ["breaker", breakerResponse], ["delivery", deliveryResponse],
  ] as const)("retains failed and absent %s nodes, then clears old values after successful empty results", (_kind, response) => {
    const full = response();
    const original = collectMonitoringAgentSamples(full, 1000);
    const empty = "buckets" in full ? { ...full, buckets: [] } : "channels" in full ? { ...full, channels: [] } : { ...full, agents: [] };
    const partial = { ...empty, successful_agent_ids: [1], failed_agents: [{ agent_id: 2, agent_name: "beta", error: "timeout" }] };
    const retained = retainMonitoringAgents(original, partial, 2000);
    expect(retained.find((node) => node.agentID === 2)).toEqual(original.find((node) => node.agentID === 2));
    expect(retained.find((node) => node.agentID === 1)?.entries).toEqual([]);
    expect(retained.find((node) => node.agentID === 1)?.updatedAt).toBe(2000);
    const absent = retainMonitoringAgents(retained, { ...partial, failed_agents: [] }, 3000);
    expect(absent.find((node) => node.agentID === 2)?.updatedAt).toBe(1000);
    const recovered = retainMonitoringAgents(absent, { ...empty, successful_agent_ids: [1, 2] }, 4000);
    expect(recovered.find((node) => node.agentID === 2)?.entries).toEqual([]);
    expect(recovered.find((node) => node.agentID === 2)?.updatedAt).toBe(4000);
    expect(full).toEqual(response());
  });

  it("keeps old limiter local values out of current aggregates", () => {
    const full = limiterResponse();
    const partial: LimiterUsageResponse = { ...full, successful_agent_ids: [1], failed_agents: [{ agent_id: 2, agent_name: "beta", error: "offline" }],
      buckets: [{ ...full.buckets[0], occupied: 2, capacity: 10, waiters: 0, per_agent: [{ ...full.buckets[0].per_agent[0], occupied: 2, waiters: 0 }] }] };
    const retained = retainMonitoringAgents(collectMonitoringAgentSamples(full, 1000), partial, 2000);
    expect(partial.buckets[0].occupied).toBe(2);
    const staleEntry = retained.find((node) => node.agentID === 2)?.entries[0];
    expect(staleEntry?.kind === "limiter" && staleEntry.cell.occupied).toBe(5);
  });
});
