import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createTestQueryClient } from "@/test/render";
import { agentQueryKeys } from "@/lib/api/agents";
import type { ConnectionSnapshot, RouteTargetSnapshot, RouteTargetsPage } from "@/lib/types";
import { AgentConnectionRail } from "./agent-connection-rail";
import { AgentConnectionsPanel } from "./agent-connections-panel";

const enqueueProbe = vi.fn().mockResolvedValue({});

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, string | number>) => ({
    control: "Control",
    relay: "Relay",
    routeTargets: "Route targets",
    direct: "Direct",
    reachable: "Reachable",
    unreachable: "Unreachable",
    ready: "Ready",
    connected: "Connected",
    healthy: "Healthy",
    configuration: "Configuration",
    relayRuntime: "Runtime",
    relayMode: "Relay mode",
    relayModeCustom: "Custom",
    desiredUri: "Desired URI",
    activeUri: "Active URI",
    streams: "Streams",
    streamCount: `${values?.count ?? 0} streams`,
    directCount: `Direct ${values?.reachable}/${values?.total}`,
    relayCount: `Relay ${values?.reachable}/${values?.total}`,
    probeTarget: `Check ${values?.target}`,
    loadMore: "Load more",
    copyTargetDiagnostic: "Copy target diagnostics",
    probe: "Check all",
    moreRelayActions: "More Relay actions",
    title: "Transport policy",
    inbound: "Inbound",
    outbound: "Outbound",
    configured: "Configured",
    effective: "Effective",
    on: "On",
    off: "Off",
    refreshingDetail: "Refreshing connection details",
    stale: "Stale",
  } as Record<string, string>)[key] ?? key,
}));

vi.mock("@/lib/api/agents", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api/agents")>();
  return {
    ...actual,
    useAgentOperation: () => ({ mutateAsync: vi.fn(), isPending: false }),
    useEnqueueConnectivityProbe: () => ({
      mutateAsync: enqueueProbe,
      isPending: false,
      variables: undefined,
    }),
  };
});

function target(): RouteTargetSnapshot {
  return {
    target_agent_id: "agent-b",
    target_name: "Agent B",
    direct: {
      state: "unreachable",
      addresses: [{ url: "https://agent-b.example", tag: "wan" }],
      network: "unreachable",
      identity: "unknown",
      eligible: false,
      checking: false,
      probe_generation: 1,
      address_fingerprint: "direct-fp",
      checked_at: 100,
      latency_ms: 12,
      last_error: { code: "direct_connect", stage: "direct_probe", message: "", occurred_at: 100, count: 1 },
    },
    relay: {
      target_agent_id: "agent-b",
      target_name: "Agent B",
      state: "reachable",
      stage: "response",
      checking: false,
      probe_generation: 2,
      relay_fingerprint: "relay-fp",
      source_relay_generation: 11,
      target_relay_generation: 22,
      checked_at: 100,
      latency_ms: 18,
    },
  };
}

function snapshot(): ConnectionSnapshot {
  return {
    version: "v1",
    snapshot_epoch: "epoch-a",
    snapshot_seq: 8,
    observed_at: 100,
    agent_id: "agent-a",
    admin_status: 1,
    transport_policy: {
      direct_inbound: { configured: true, effective: true },
      direct_outbound: { configured: true, effective: true },
      relay_inbound: { configured: true, effective: true },
      relay_outbound: { configured: false, effective: false },
    },
    control: {
      state: "connected",
      health: "healthy",
      reason_codes: [],
      session_generation: 3,
      connected_at: 1,
      heartbeat_at: 2,
      runtime_reported_at: 3,
      last_seen: 4,
    },
    relay: {
      support: "supported",
      config: "configured",
      availability: "ready",
      accepting_new_streams: true,
      convergence: "converged",
      desired: {
        mode: "custom",
        configured_uri: "wss://relay.example/ws",
        effective_uri: "wss://relay.example/ws",
        desired_generation: 9,
      },
      active: {
        uri: "wss://relay.example/ws",
        active_generation: 9,
        session_generation: 11,
        connected_at: 1,
        streams: 4,
        retry_at: 0,
      },
      recent_errors: [],
    },
    direct: {
      generation: 8,
      summary: { state: "degraded", disabled: 0, reachable: 0, degraded: 0, unreachable: 1, stale: 0, total: 1 },
      targets: {},
    },
    target_summaries: {
      direct: { state: "degraded", disabled: 0, reachable: 0, degraded: 0, unreachable: 1, stale: 0, total: 1 },
      relay: { state: "reachable", disabled: 0, reachable: 1, unreachable: 0, unavailable: 0, unknown: 0, unsupported: 0, stale: 0, total: 1 },
    },
    allowed_operations: [{ operation: "probe", allowed: true }],
  };
}

function page(): RouteTargetsPage {
  return {
    snapshot_epoch: "epoch-a",
    snapshot_seq: 91,
    observed_at: 100,
    summaries: snapshot().target_summaries,
    data: [target()],
    limit: 20,
  };
}

function renderPanel() {
  const queryClient = createTestQueryClient();
  return render(
    <QueryClientProvider client={queryClient}>
      <AgentConnectionsPanel
        agentId={7}
        snapshot={snapshot()}
        initialRouteTargetsPage={page()}
      />
    </QueryClientProvider>,
  );
}

describe("AgentConnectionsPanel", () => {
  afterEach(() => {
    cleanup();
    enqueueProbe.mockClear();
  });

  it("nests Relay under Control and route targets under Relay without cards", () => {
    const { container } = renderPanel();
    const control = screen.getByRole("heading", { name: "Control" }).closest("section");
    const relay = screen.getByRole("heading", { name: "Relay" }).closest("section");
    const targets = screen.getByRole("heading", { name: "Route targets" }).closest("section");
    expect(control).toContainElement(relay);
    expect(relay).toContainElement(targets);
    expect(relay).toHaveTextContent("Transport policy");
    expect(screen.getByTestId("agent-transport-policy-status")).toHaveAttribute("data-compact", "false");
    expect(container.querySelector("[data-slot=card]")).not.toBeInTheDocument();
    expect(container.querySelectorAll("svg.size-4").length).toBeGreaterThanOrEqual(3);
  });

  it("keeps refresh feedback in the Control actions slot", () => {
    const value = snapshot();
    const queryClient = createTestQueryClient();
    const view = (refreshing: boolean) => (
      <QueryClientProvider client={queryClient}>
        <AgentConnectionsPanel
          agentId={7}
          snapshot={value}
          initialRouteTargetsPage={page()}
          refreshing={refreshing}
          routeTargetsCurrent
        />
      </QueryClientProvider>
    );
    const { rerender } = render(view(false));
    const rail = screen.getByTestId("agent-connection-rail");
    const control = screen.getByRole("heading", { name: "Control" }).closest("section")!;
    const slot = screen.getByTestId("connection-refresh-status");
    expect(control).toContainElement(slot);
    expect(slot.querySelector("svg")).toHaveClass("invisible");

    rerender(view(true));
    expect(screen.getByTestId("connection-refresh-status")).toBe(slot);
    expect(slot.querySelector("svg")).toHaveClass("animate-spin");
    expect(rail.firstElementChild).toBe(control);
    expect(screen.queryByText("Refreshing connection details")).not.toBeInTheDocument();

    rerender(view(false));
    expect(screen.getByTestId("connection-refresh-status")).toBe(slot);
    expect(slot.querySelector("svg")).toHaveClass("invisible");
  });

  it("keeps the refresh slot in Control actions in compact mode", () => {
    const queryClient = createTestQueryClient();
    render(
      <QueryClientProvider client={queryClient}>
        <AgentConnectionRail
          agentId={7}
          snapshot={snapshot()}
          initialRouteTargetsPage={page()}
          refreshing
          compact
        />
      </QueryClientProvider>,
    );

    const control = screen.getByRole("heading", { name: "Control" }).closest("section")!;
    expect(control).toContainElement(screen.getByTestId("connection-refresh-status"));
    expect(screen.getByTestId("connection-refresh-status").querySelector("svg"))
      .toHaveClass("animate-spin");
  });

  it("keeps stale feedback and hides the background spinner", () => {
    const queryClient = createTestQueryClient();
    render(
      <QueryClientProvider client={queryClient}>
        <AgentConnectionsPanel
          agentId={7}
          snapshot={snapshot()}
          initialRouteTargetsPage={page()}
          stale
          refreshing
        />
      </QueryClientProvider>,
    );

    expect(screen.getByText("Stale")).toBeInTheDocument();
    const slot = screen.getByTestId("connection-refresh-status");
    expect(slot.querySelector("svg")).toHaveClass("invisible");
    expect(slot).not.toHaveAttribute("role");
  });

  it("keeps one fixed slot while route targets catch up", () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(() => new Promise(() => undefined));
    const queryClient = createTestQueryClient();
    render(
      <QueryClientProvider client={queryClient}>
        <AgentConnectionsPanel
          agentId={7}
          snapshot={{ ...snapshot(), observed_at: 200 }}
          initialRouteTargetsPage={page()}
          refreshing
          routeTargetsCurrent={false}
        />
      </QueryClientProvider>,
    );

    expect(screen.getAllByTestId("connection-refresh-status")).toHaveLength(1);
    expect(screen.getByTestId("connection-refresh-status").querySelector("svg"))
      .toHaveClass("animate-spin");
  });

  it("shows Direct and Relay outcomes for the same directed target", () => {
    renderPanel();
    expect(screen.getByText("Agent B")).toBeInTheDocument();
    expect(screen.getByText("agent-b")).toBeInTheDocument();
    expect(screen.getByText("direct_connect")).toBeInTheDocument();
    expect(screen.getAllByText("Reachable").length).toBeGreaterThan(0);
    expect(screen.getByText("12 ms")).toBeInTheDocument();
    expect(screen.getByText("18 ms")).toBeInTheDocument();
  });

  it("shows a loading boundary instead of an empty target state while detail catches up", () => {
    const current = snapshot();
    const olderPage = { ...page(), observed_at: current.observed_at - 1 };
    const queryClient = createTestQueryClient();
    const { container } = render(
      <QueryClientProvider client={queryClient}>
        <AgentConnectionsPanel
          agentId={7}
          snapshot={current}
          initialRouteTargetsPage={olderPage}
        />
      </QueryClientProvider>,
    );

    expect(screen.queryByText("No route targets")).not.toBeInTheDocument();
    expect(container.querySelectorAll("[data-slot=skeleton]")).toHaveLength(2);
  });

  it("retains the old page while a generic cached page mismatches, then atomically accepts a matching page", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(() => new Promise(() => undefined));
    const current = { ...snapshot(), snapshot_seq: 9, observed_at: 200 };
    const retainedPage = {
      ...page(),
      observed_at: 100,
      data: [{ ...target(), target_agent_id: "old-target", target_name: "Old target" }],
      next_cursor: "old-cursor",
    };
    const mismatchedPage = {
      ...page(),
      observed_at: 150,
      data: [{ ...target(), target_agent_id: "wrong-target", target_name: "Wrong target" }],
      next_cursor: "wrong-cursor",
    };
    const queryClient = createTestQueryClient();
    queryClient.setQueryData(agentQueryKeys.targetsPage(7, 20), {
      pages: [mismatchedPage],
      pageParams: [""],
    });
    render(
      <QueryClientProvider client={queryClient}>
        <AgentConnectionsPanel
          agentId={7}
          snapshot={current}
          initialRouteTargetsPage={retainedPage}
          refreshing
          routeTargetsCurrent={false}
        />
      </QueryClientProvider>,
    );

    expect(screen.getByText("Old target")).toBeInTheDocument();
    expect(screen.queryByText("Wrong target")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Check Old target" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Load more" })).toBeDisabled();
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());

    const matchingPage = {
      ...page(),
      observed_at: current.observed_at,
      data: [{ ...target(), target_agent_id: "new-target", target_name: "New target" }],
      next_cursor: "new-cursor",
    };
    act(() => {
      queryClient.setQueryData(agentQueryKeys.targetsPage(7, 20), {
        pages: [matchingPage],
        pageParams: [""],
      });
    });

    expect(await screen.findByText("New target")).toBeInTheDocument();
    expect(screen.queryByText("Old target")).not.toBeInTheDocument();
    expect(screen.queryByText("Wrong target")).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("button", { name: "Check New target" })).toBeEnabled());
    expect(screen.getByRole("button", { name: "Load more" })).toBeEnabled();
  });

  it("runs a strict one-target probe and keeps mobile icon targets at 44px", async () => {
    const user = userEvent.setup();
    renderPanel();
    const check = screen.getByRole("button", { name: "Check Agent B" });
    expect(check).toHaveClass("size-11", "sm:size-8");
    await user.click(check);
    expect(enqueueProbe).toHaveBeenCalledWith({
      id: 7,
      request: {
        scope: { kind: "targets", target_agent_ids: ["agent-b"] },
        expected_epoch: "epoch-a",
        expected_control_generation: 3,
        expected_relay_generation: 11,
      },
    });
    expect(screen.getByRole("button", { name: "More Relay actions" })).toHaveClass("size-11", "sm:hidden");
  });
});
