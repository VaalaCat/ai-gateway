import React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";
import messages from "@/i18n/en.json";
import type { BreakerBoardResponse, DeliveryBoardResponse, LimiterUsageResponse } from "@/lib/types";
import { useBreakerBoard, useDeliveryBoard, useLimiterUsage } from "@/lib/api/observability";
import { BreakerTab } from "./breaker-tab";
import { DeliveryTab } from "./delivery-tab";
import { LimiterTab } from "./limiter-tab";

vi.mock("@/lib/api/observability", () => ({
  useLimiterUsage: vi.fn(), useBreakerBoard: vi.fn(), useDeliveryBoard: vi.fn(),
  useDeliveryOp: () => ({ isPending: false, mutate: vi.fn() }),
}));
vi.mock("@/components/business/entity-label", () => ({ EntityLabel: ({ id }: { id: number }) => <span>#{id}</span> }));

const limiter: LimiterUsageResponse = { successful_agent_ids: [1], failed_agents: [], buckets: [{
  limiter_id: 5, name: "request rule", bucket: "shared", metric: "requests", key_by: "none", occupied: 0, capacity: 10, waiters: 0, window_end_ms: 0,
  per_agent: [{ agent_id: 1, agent_name: "edge-one", occupied: 0, capacity: 10, waiters: 0 }],
}] };
const breaker: BreakerBoardResponse = { successful_agent_ids: [1], failed_agents: [], channels: [{
  source: "llm", channel_id: 9, worst_state: "closed", open_agents: 0, total_agents: 1,
  agents: [{ agent_id: 1, agent_name: "edge-one", state: "closed", remaining_ms: 0, failures: 0, successes: 4, failure_rate: 0 }],
}] };
const delivery: DeliveryBoardResponse = { successful_agent_ids: [1], failed_agents: [], agents: [{
  agent_id: 1, agent_name: "edge-one", store_len: 0, retry_len: 0, total_bytes: 0, oldest_ts: 0, last_success_at: 1, last_error: "", inflight: 0, shared_usage_dropped: 0, api_trace_slimmed: 0, items: [],
}], log_backlog: { pending: 2, retry: 0, inflight: 0, bytes: 40, oldest_seconds: 1, dropped: 0, last_error: "", schema_ready: true } };

let query: { data: LimiterUsageResponse | BreakerBoardResponse | DeliveryBoardResponse; dataUpdatedAt: number; isFetching: boolean; error: Error | null; refetch: ReturnType<typeof vi.fn> };
const scenarios = [
  { name: "limiter", Component: LimiterTab, data: limiter, healthyText: messages.observability.limiter.clusterTitle },
  { name: "breaker", Component: BreakerTab, data: breaker, healthyText: messages.monitoring.breaker.allHealthy },
  { name: "delivery", Component: DeliveryTab, data: delivery, healthyText: messages.monitoring.delivery.allClear },
];

beforeEach(() => {
  query = { data: limiter, dataUpdatedAt: 1000, isFetching: false, error: null, refetch: vi.fn() };
  vi.mocked(useLimiterUsage).mockImplementation(() => query as unknown as ReturnType<typeof useLimiterUsage>);
  vi.mocked(useBreakerBoard).mockImplementation(() => query as unknown as ReturnType<typeof useBreakerBoard>);
  vi.mocked(useDeliveryBoard).mockImplementation(() => query as unknown as ReturnType<typeof useDeliveryBoard>);
});

describe("monitoring whole-request failures", () => {
  it.each(scenarios)("moves every cached $name node to stale details and removes healthy conclusions until recovery", ({ Component, data, healthyText }) => {
    query.data = data;
    const content = () => <NextIntlClientProvider locale="en" messages={messages}><Component /></NextIntlClientProvider>;
    const view = render(content());
    expect(screen.getByText(healthyText)).toBeInTheDocument();
    const capturedAt = screen.getByText(/Data captured:/).textContent;
    query = { ...query, error: new Error("GET timeout") };
    view.rerender(content());
    expect(screen.queryByText(healthyText)).not.toBeInTheDocument();
    const stale = screen.getByRole("region", { name: "Stale node data (1)" });
    expect(within(stale).getByText(/edge-one/)).toBeInTheDocument();
    expect(within(stale).getByText("GET timeout")).toBeInTheDocument();
    expect(within(stale).getByText(/Last success:/)).toBeInTheDocument();
    expect(screen.getByText(/Data captured:/).textContent).toBe(capturedAt);
    expect(query.data.successful_agent_ids).toEqual([1]);
    expect(view.container.querySelector('[class*="green"]')).toBeNull();
    if ("log_backlog" in data) expect(screen.getByText(messages.monitoring.delivery.logBacklog.replace("{queued}", "2").replace("{dropped}", "0"))).toBeInTheDocument();
    query = { ...query, error: null, dataUpdatedAt: 3000 };
    view.rerender(content());
    expect(screen.getByText(healthyText)).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Stale node data (1)" })).not.toBeInTheDocument();
  });

  it.each(scenarios)("treats cached $name data as stale on initial mount after a failed fetch", ({ Component, data, healthyText }) => {
    query = { ...query, data, error: new Error("GET unavailable") };
    render(<NextIntlClientProvider locale="en" messages={messages}><Component /></NextIntlClientProvider>);
    expect(screen.queryByText(healthyText)).not.toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Stale node data (1)" })).toBeInTheDocument();
  });
});
