import React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";
import messages from "@/i18n/en.json";
import type { LimiterUsageResponse } from "@/lib/types";
import { useLimiterUsage } from "@/lib/api/observability";
import { LimiterTab } from "./limiter-tab";

vi.mock("@/lib/api/observability", () => ({ useLimiterUsage: vi.fn() }));
vi.mock("@/components/business/entity-label", () => ({ EntityLabel: ({ id }: { id: number }) => <span>#{id}</span> }));

const full: LimiterUsageResponse = {
  successful_agent_ids: [1, 2], failed_agents: [],
  buckets: [{ limiter_id: 1, name: "shared rule", bucket: "shared", metric: "requests", key_by: "none", occupied: 9, capacity: 20, waiters: 3, window_end_ms: 0,
    per_agent: [
      { agent_id: 1, agent_name: "alpha", occupied: 4, capacity: 10, waiters: 1 },
      { agent_id: 2, agent_name: "beta", occupied: 5, capacity: 10, waiters: 2 },
    ] }],
};
let query: { data: LimiterUsageResponse | undefined; dataUpdatedAt: number; isFetching: boolean; error: Error | null; refetch: ReturnType<typeof vi.fn> };

function content() {
  return <NextIntlClientProvider locale="en" messages={messages}><LimiterTab /></NextIntlClientProvider>;
}

beforeEach(() => {
  query = { data: full, dataUpdatedAt: 1000, isFetching: false, error: null, refetch: vi.fn() };
  vi.mocked(useLimiterUsage).mockImplementation(() => query as unknown as ReturnType<typeof useLimiterUsage>);
});

describe("LimiterTab reliable refresh", () => {
  it("retains failed-node values separately, keeps successful totals current, and clears stale rows after recovery", () => {
    const view = render(content());
    query = { ...query, dataUpdatedAt: 2000, data: {
      ...full, successful_agent_ids: [1], failed_agents: [{ agent_id: 2, agent_name: "beta", error: "RPC timeout" }],
      buckets: [{ ...full.buckets[0], occupied: 2, capacity: 10, waiters: 0, per_agent: [{ ...full.buckets[0].per_agent[0], occupied: 2, waiters: 0 }] }],
    } };
    view.rerender(content());
    const staleSection = screen.getByRole("region", { name: "Stale node data (1)" });
    expect(within(staleSection).getByText(/beta/)).toBeInTheDocument();
    expect(within(staleSection).getByText(/5 \/ 10/)).toBeInTheDocument();
    expect(within(staleSection).getByText("RPC timeout")).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
    expect(screen.queryByText("7")).not.toBeInTheDocument();
    query = { ...query, dataUpdatedAt: 3000, data: { buckets: [], successful_agent_ids: [1, 2], failed_agents: [] } };
    view.rerender(content());
    expect(screen.queryByRole("region", { name: "Stale node data (1)" })).not.toBeInTheDocument();
  });

  it("keeps prior values and timestamp after whole-request failure, and exposes pause/manual refresh", () => {
    const view = render(content());
    const captured = screen.getByText(/Data captured:/).textContent;
    query = { ...query, error: new Error("network unavailable") };
    view.rerender(content());
    expect(screen.getAllByText("network unavailable")).toHaveLength(3);
    const retained = screen.getByRole("region", { name: "Stale node data (2)" });
    expect(within(retained).getByText(/4 \/ 10/)).toBeInTheDocument();
    expect(within(retained).getByText(/5 \/ 10/)).toBeInTheDocument();
    expect(screen.queryByText("9")).not.toBeInTheDocument();
    expect(screen.getByText(/Data captured:/).textContent).toBe(captured);
    fireEvent.click(screen.getByRole("button", { name: "Pause" }));
    expect(useLimiterUsage).toHaveBeenLastCalledWith({ paused: true });
    expect(screen.getByText(/Data captured:/).textContent).toBe(captured);
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    expect(query.refetch).toHaveBeenCalledOnce();
  });

  it.each([true, false])("does not turn an initial loading/failure into a zero total: fetching=%s", (isFetching) => {
    query = { ...query, data: undefined, dataUpdatedAt: 0, isFetching, error: isFetching ? null : new Error("offline") };
    render(content());
    expect(screen.queryByText("0")).not.toBeInTheDocument();
    expect(screen.queryByText("9")).not.toBeInTheDocument();
    expect(screen.getByText(isFetching ? "Loading monitoring data…" : "No monitoring data available yet")).toBeInTheDocument();
  });
});
