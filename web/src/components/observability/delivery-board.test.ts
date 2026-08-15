import { describe, expect, it } from "vitest";

import type { AgentQueueRow, DeliveryBoardResponse, DeliveryQueueItem } from "@/lib/types";
import { deliveryQueueIdentity, deliveryQueueOperationTarget, deliveryRiskSummary } from "./delivery-board";

function item(overrides: Partial<DeliveryQueueItem>): DeliveryQueueItem {
  return {
    request_id: "shared",
    bytes: 1,
    attempts: 1,
    degrade_level: 0,
    next_at: 0,
    ...overrides,
  };
}

function agent(overrides: Partial<AgentQueueRow>): AgentQueueRow {
  return {
    agent_id: 1,
    agent_name: "agent-a",
    store_len: 0,
    retry_len: 0,
    total_bytes: 0,
    oldest_ts: 0,
    last_success_at: 0,
    last_error: "",
    inflight: 0,
    shared_usage_dropped: 0,
    api_trace_slimmed: 0,
    items: [],
    ...overrides,
  };
}

describe("deliveryQueueIdentity", () => {
  it("keeps modern LLM and API entries distinct while preserving legacy LLM fallback", () => {
    expect(deliveryQueueIdentity(item({ queue_id: "llm:shared", usage_type: "llm" }))).toBe("llm:shared");
    expect(deliveryQueueIdentity(item({ queue_id: "api:shared", usage_type: "api" }))).toBe("api:shared");
    expect(deliveryQueueIdentity(item({}))).toBe("shared");
  });

  it("uses queue_ids for modern items and request_ids only for legacy items", () => {
    expect(deliveryQueueOperationTarget(item({ queue_id: "api:shared", usage_type: "api" }))).toEqual({
      queue_ids: ["api:shared"],
    });
    expect(deliveryQueueOperationTarget(item({ request_id: "api:shared" }))).toEqual({
      request_ids: ["api:shared"],
    });
  });
});

describe("deliveryRiskSummary", () => {
  // behavior change: the UI keeps shared queue drops distinct from API-only
  // trace slimming instead of presenting both as Generic API totals.
  it("sums shared Agent drops and API-only slim counters with the Master log backlog", () => {
    const data = {
      agents: [
        agent({ agent_id: 1, shared_usage_dropped: 2, api_trace_slimmed: 3 }),
        agent({ agent_id: 2, agent_name: "agent-b", shared_usage_dropped: 5, api_trace_slimmed: 7 }),
      ],
      failed_agents: [],
      log_backlog: {
        pending: 4,
        retry: 2,
        inflight: 1,
        bytes: 42,
        oldest_seconds: 5,
        dropped: 9,
        last_error: "write failed",
        schema_ready: false,
      },
    } as DeliveryBoardResponse;

    expect(deliveryRiskSummary(data)).toEqual({
      sharedUsageDropped: 7,
      apiTraceSlimmed: 10,
      logQueued: 7,
      logDropped: 9,
      logReady: false,
    });
  });

  it("keeps healthy zero values stable", () => {
    expect(deliveryRiskSummary({
      agents: [],
      failed_agents: [],
      log_backlog: {
        pending: 0,
        retry: 0,
        inflight: 0,
        bytes: 0,
        oldest_seconds: 0,
        dropped: 0,
        last_error: "",
        schema_ready: true,
      },
    })).toEqual({ sharedUsageDropped: 0, apiTraceSlimmed: 0, logQueued: 0, logDropped: 0, logReady: true });
  });

  it("treats missing mixed-version fields as zero without a false log alarm", () => {
    const legacy = { agents: [{}], failed_agents: [] } as unknown as DeliveryBoardResponse;
    expect(deliveryRiskSummary(legacy)).toEqual({
      sharedUsageDropped: 0,
      apiTraceSlimmed: 0,
      logQueued: 0,
      logDropped: 0,
      logReady: true,
    });
  });
});
