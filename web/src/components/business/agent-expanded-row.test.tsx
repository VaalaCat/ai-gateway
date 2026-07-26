import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type {
  Agent,
  AgentDetail,
  ConnectionSnapshot,
  DirectTargetSnapshot,
  RouteTargetSnapshot,
} from "@/lib/types";
import { createTestQueryClient as makeTestQueryClient } from "@/test/render";
import { agentQueryKeys } from "@/lib/api/agents";
import { AgentExpandedRow } from "./agent-expanded-row";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string, values?: Record<string, string>) => ({ control: "Control", relay: "Relay", routeTargets: "Route targets", loadingDetail: "Loading", detailLoadFailed: "Could not load connection details", retry: "Retry", refreshingDetail: "Refreshing connection details", stale: "Stale", probe: "Check all", reconnect: "Reconnect Relay", drain: "Drain Relay", disconnect: "Disconnect Relay", probeTarget: `Check ${values?.target ?? "target"}`, copyTargetDiagnostic: "Copy target diagnostics", loadMore: "Load more", title: "Transport policy", direct: "Direct", inbound: "Inbound", outbound: "Outbound", effective: "Effective", configured: "Configured", on: "On", off: "Off" } as Record<string, string>)[key] ?? key }));

const queryClients: ReturnType<typeof makeTestQueryClient>[] = [];

function createTestQueryClient() {
  const queryClient = makeTestQueryClient();
  queryClient.setDefaultOptions({
    queries: { gcTime: Infinity, retry: false, experimental_prefetchInRender: true },
    mutations: { gcTime: Infinity, retry: false },
  });
  queryClients.push(queryClient);
  return queryClient;
}

function connection(): ConnectionSnapshot {
  return {
    version: "v1", snapshot_epoch: "epoch", snapshot_seq: 1, observed_at: 1, agent_id: "agent-a", admin_status: 1,
    transport_policy: {
      direct_inbound: { configured: true, effective: true },
      direct_outbound: { configured: true, effective: true },
      relay_inbound: { configured: true, effective: true },
      relay_outbound: { configured: true, effective: true },
    },
    control: { state: "connected", health: "healthy", reason_codes: [], session_generation: 1, connected_at: 1, heartbeat_at: 1, runtime_reported_at: 1, last_seen: 1 },
    relay: { support: "supported", config: "configured", availability: "ready", accepting_new_streams: true, convergence: "converged", desired: { mode: "inherit", configured_uri: "", effective_uri: "wss://relay", desired_generation: 1 }, active: { uri: "wss://relay", active_generation: 1, session_generation: 1, connected_at: 1, streams: 0, retry_at: 0 }, recent_errors: [] },
    direct: { generation: 7, summary: { state: "unknown", disabled: 0, reachable: 0, degraded: 0, unreachable: 0, stale: 0, total: 0 }, targets: {} },
    target_summaries: {
      direct: { state: "unknown", disabled: 0, reachable: 0, degraded: 0, unreachable: 0, stale: 0, total: 0 },
      relay: { state: "unknown", disabled: 0, reachable: 0, unreachable: 0, unavailable: 0, unknown: 0, unsupported: 0, stale: 0, total: 0 },
    },
    allowed_operations: [
      { operation: "probe", allowed: true },
      { operation: "relay_reconnect", allowed: true },
      { operation: "relay_drain", allowed: true },
      { operation: "relay_disconnect", allowed: true },
    ],
  };
}

const value = connection();
const agent: Agent = { id: 7, agent_id: "agent-a", name: "Agent A", status: 1, tags: "", proxy_url: "", relay_mode: "inherit", direct_inbound_enabled: true, direct_outbound_enabled: true, relay_inbound_enabled: true, relay_outbound_enabled: true, last_seen: 1, created_at: 1, connection: { version: "v1", snapshot_epoch: "epoch", snapshot_seq: 1, observed_at: 1, transport_policy: value.transport_policy, control: value.control, relay: { support: "supported", config: "configured", availability: "ready", accepting_new_streams: true, convergence: "converged", streams: 0 }, direct: value.direct.summary, targets: value.target_summaries } };
const detail: AgentDetail = { ...agent, http_addresses: "[]", relay_uri: "", runtime: null, connection: value, route_targets: { snapshot_epoch: "epoch", snapshot_seq: 7, observed_at: 1, summaries: value.target_summaries, data: [], limit: 20 } };

function directTarget(id: string, name: string): DirectTargetSnapshot {
  return {
    target_agent_id: id,
    target_name: name,
    addresses: [],
    network: "reachable",
    identity: "verified",
    eligible: true,
    checking: false,
    probe_generation: 1,
    address_fingerprint: id,
    checked_at: 1,
    latency_ms: 1,
    recent_errors: [],
  };
}

function routeTarget(id: string, name: string): RouteTargetSnapshot {
  const direct = directTarget(id, name);
  return {
    target_agent_id: id,
    target_name: name,
    direct: {
      state: "reachable",
      addresses: direct.addresses,
      network: direct.network,
      identity: direct.identity,
      eligible: direct.eligible,
      checking: direct.checking,
      probe_generation: direct.probe_generation,
      address_fingerprint: direct.address_fingerprint,
      checked_at: direct.checked_at,
      latency_ms: direct.latency_ms,
    },
    relay: {
      target_agent_id: id,
      target_name: name,
      state: "reachable",
      stage: "response",
      checking: false,
      probe_generation: 1,
      relay_fingerprint: id,
      source_relay_generation: 1,
      target_relay_generation: 1,
      checked_at: 1,
      latency_ms: 1,
    },
  };
}

describe("AgentExpandedRow", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  afterEach(async () => {
    await Promise.all(queryClients.map((queryClient) => queryClient.cancelQueries()));
    cleanup();
    for (const queryClient of queryClients.splice(0)) queryClient.clear();
    await act(async () => undefined);
  });

  it("does not fetch while collapsed and fetches connection data on first expansion", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation((input) => {
      const url = String(input);
      return Promise.resolve(new Response(JSON.stringify(url.endsWith("/detail") ? detail : detail.route_targets), { status: 200, headers: { "content-type": "application/json" } }));
    });
    const queryClient = createTestQueryClient();
    const { rerender } = render(<QueryClientProvider client={queryClient}><AgentExpandedRow agent={agent} expanded={false} /></QueryClientProvider>);
    expect(fetchMock).not.toHaveBeenCalled();

    rerender(<QueryClientProvider client={queryClient}><AgentExpandedRow agent={agent} expanded /></QueryClientProvider>);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("Control")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Relay" })).toBeInTheDocument();
    expect(screen.getByText("Route targets")).toBeInTheDocument();
    const rail = screen.getByTestId("agent-connection-rail");
    const control = screen.getByRole("heading", { name: "Control" }).closest("section");
    const relay = screen.getByRole("heading", { name: "Relay" }).closest("section");
    const targets = screen.getByRole("heading", { name: "Route targets" }).closest("section");
    expect(control).toContainElement(relay);
    expect(relay).toContainElement(targets);
    expect(screen.getByTestId("agent-transport-policy-status")).toHaveAttribute("data-compact", "true");
    expect(rail.querySelectorAll("svg.size-4")).toHaveLength(4);
    expect(rail.querySelector("[data-slot=empty-icon]"))
      .toHaveClass("[&_svg:not([class*='size-'])]:size-6");
    expect(rail.querySelector("[data-slot=card]")).not.toBeInTheDocument();
  });

  it("shows skeletons only while the first detail request has no cached snapshot", () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(() => new Promise(() => undefined));
    const queryClient = createTestQueryClient();
    const { container } = render(
      <QueryClientProvider client={queryClient}>
        <AgentExpandedRow agent={agent} expanded />
      </QueryClientProvider>,
    );

    expect(screen.getByLabelText("Loading")).toBeInTheDocument();
    expect(container.querySelectorAll("[data-slot=skeleton]")).toHaveLength(3);
    expect(screen.queryByTestId("agent-connection-rail")).not.toBeInTheDocument();
  });

  it("keeps the accepted rail mounted during a background detail refetch", async () => {
    let resolveRefetch!: (response: Response) => void;
    const refetchResponse = new Promise<Response>((resolve) => {
      resolveRefetch = resolve;
    });
    const fetchMock = vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response(JSON.stringify(detail), {
        status: 200,
        headers: { "content-type": "application/json" },
      }))
      .mockReturnValueOnce(refetchResponse);
    const queryClient = createTestQueryClient();
    render(
      <QueryClientProvider client={queryClient}>
        <AgentExpandedRow agent={agent} expanded />
      </QueryClientProvider>,
    );

    const railBefore = await screen.findByTestId("agent-connection-rail");
    const controlBefore = screen.getByRole("heading", { name: "Control" }).closest("section");
    void queryClient.invalidateQueries({ queryKey: agentQueryKeys.connections(agent.id), exact: true });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));

    expect(screen.getByTestId("agent-connection-rail")).toBe(railBefore);
    expect(screen.getByRole("heading", { name: "Control" }).closest("section")).toBe(controlBefore);
    expect(screen.queryByLabelText("Loading")).not.toBeInTheDocument();
    expect(screen.getByTestId("connection-refresh-status").querySelector("svg"))
      .toHaveClass("animate-spin");

    resolveRefetch(new Response(JSON.stringify(detail), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    await waitFor(() => {
      expect(screen.getByTestId("connection-refresh-status").querySelector("svg"))
        .toHaveClass("invisible");
    });
    expect(screen.getByTestId("agent-connection-rail")).toBe(railBefore);
  });

  it("renders only the bounded direct page when detail contains a larger target snapshot", async () => {
    const detailTargets = Object.fromEntries(
      Array.from({ length: 30 }, (_, index) => {
        const id = `detail-${index}`;
        return [id, directTarget(id, `Detail target ${index}`)];
      }),
    );
    const pagedTargets = Array.from({ length: 20 }, (_, index) => {
      const id = `page-${index}`;
      return routeTarget(id, `Paged target ${index}`);
    });
    const routeTargetsPage = { ...detail.route_targets, data: pagedTargets, limit: 20 };
    const detailed: AgentDetail = {
      ...detail,
      route_targets: routeTargetsPage,
      connection: {
        ...detail.connection,
        direct: { ...detail.connection.direct, targets: detailTargets },
      },
    };
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation((input) => {
      const url = String(input);
      return Promise.resolve(new Response(JSON.stringify(url.endsWith("/detail") ? detailed : routeTargetsPage), {
        status: 200,
        headers: { "content-type": "application/json" },
      }));
    });
    const queryClient = createTestQueryClient();
    render(
      <QueryClientProvider client={queryClient}>
        <AgentExpandedRow agent={agent} expanded />
      </QueryClientProvider>,
    );

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(await screen.findAllByRole("listitem")).toHaveLength(20);
    expect(screen.getByText("Paged target 19")).toBeInTheDocument();
    expect(screen.queryByText("Detail target 0")).not.toBeInTheDocument();
  });

  it("keeps the old Route Targets page while the matching page refreshes", async () => {
    const currentDirectPage = {
      ...detail.route_targets,
      data: [routeTarget("old-target", "Old target")],
      next_cursor: "old-cursor",
    };
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation((input) => {
      const url = String(input);
      if (!url.endsWith("/detail")) return new Promise(() => undefined);
      return Promise.resolve(new Response(JSON.stringify({ ...detail, route_targets: currentDirectPage }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }));
    });
    const queryClient = createTestQueryClient();
    const view = render(
      <QueryClientProvider client={queryClient}>
        <AgentExpandedRow agent={agent} expanded />
      </QueryClientProvider>,
    );
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("Old target")).toBeInTheDocument();

    const next = { ...value, snapshot_seq: 2, observed_at: 2, direct: { ...value.direct, generation: 8 } };
    act(() => {
      queryClient.setQueryData(agentQueryKeys.connections(7), {
        current: next,
        routeTargetsPage: currentDirectPage,
        retiredEpochs: [],
        stale: false,
      });
    });

    expect(await screen.findByTestId("connection-refresh-status")).toHaveAttribute(
      "aria-label",
      "Refreshing connection details",
    );
    expect(screen.getByText("Old target")).toBeInTheDocument();
    for (const name of ["Check all", "Reconnect Relay", "Drain Relay", "Disconnect Relay", "Check Old target"]) {
      expect(screen.getByRole("button", { name })).toBeDisabled();
    }
    expect(screen.getByRole("button", { name: "Copy target diagnostics" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Load more" })).toBeDisabled();

    const matchingPage = {
      ...currentDirectPage,
      snapshot_seq: 8,
      observed_at: 2,
      data: [routeTarget("new-target", "New target")],
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
    expect(screen.getByTestId("connection-refresh-status").querySelector("svg"))
      .toHaveClass("invisible");
    expect(fetchMock).toHaveBeenCalled();
    for (const name of ["Check all", "Reconnect Relay", "Drain Relay", "Disconnect Relay", "Check New target"]) {
      expect(screen.getByRole("button", { name })).toBeEnabled();
    }
    expect(screen.getByRole("button", { name: "Load more" })).toBeEnabled();

    act(() => {
      queryClient.setQueryData(agentQueryKeys.targetsPage(7, 20), {
        pages: [{ ...matchingPage, next_cursor: undefined }],
        pageParams: [""],
      });
    });
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: "Load more" })).not.toBeInTheDocument();
    });
    view.unmount();
    queryClient.clear();
  });

  it("shows a retryable error instead of a blank row when the first detail request fails", async () => {
    const user = userEvent.setup();
    const fetchMock = vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response(JSON.stringify({ message: "unavailable" }), {
        status: 500,
        headers: { "content-type": "application/json" },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify(detail), {
        status: 200,
        headers: { "content-type": "application/json" },
      }));
    const queryClient = createTestQueryClient();
    render(
      <QueryClientProvider client={queryClient}>
        <AgentExpandedRow agent={agent} expanded />
      </QueryClientProvider>,
    );

    expect(await screen.findByRole("alert")).toHaveTextContent("Could not load connection details");
    await user.click(screen.getByRole("button", { name: "Retry" }));

    expect(await screen.findByText("Control")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("keeps connection details visible when a background refetch fails", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response(JSON.stringify(detail), {
        status: 200,
        headers: { "content-type": "application/json" },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ message: "unavailable" }), {
        status: 500,
        headers: { "content-type": "application/json" },
      }));
    const queryClient = createTestQueryClient();
    render(
      <QueryClientProvider client={queryClient}>
        <AgentExpandedRow agent={agent} expanded />
      </QueryClientProvider>,
    );
    expect(await screen.findByText("Control")).toBeInTheDocument();

    await queryClient.invalidateQueries({ queryKey: agentQueryKeys.connections(7), exact: true });

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    expect(screen.getByText("Control")).toBeInTheDocument();
    expect(await screen.findByText("Stale")).toBeInTheDocument();
  });

  it("disables snapshot-dependent controls when Route Targets pagination conflicts", async () => {
    const user = userEvent.setup();
    const currentPage = {
      ...detail.route_targets,
      data: [routeTarget("conflict-target", "Conflict target")],
      next_cursor: "cursor-1",
    };
    const detailed = { ...detail, route_targets: currentPage };
    let detailRequests = 0;
    vi.spyOn(globalThis, "fetch").mockImplementation((input) => {
      const url = String(input);
      if (url.endsWith("/detail")) {
        detailRequests += 1;
        if (detailRequests === 1) {
          return Promise.resolve(new Response(JSON.stringify(detailed), {
            status: 200,
            headers: { "content-type": "application/json" },
          }));
        }
        return new Promise(() => undefined);
      }
      return Promise.resolve(new Response(JSON.stringify({
        code: "route_targets_cursor_snapshot_changed",
        message: "snapshot changed",
      }), {
        status: 409,
        headers: { "content-type": "application/json" },
      }));
    });
    const queryClient = createTestQueryClient();
    render(
      <QueryClientProvider client={queryClient}>
        <AgentExpandedRow agent={agent} expanded />
      </QueryClientProvider>,
    );
    expect(await screen.findByText("Conflict target")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Check all" })).toBeEnabled();

    await user.click(screen.getByRole("button", { name: "Load more" }));

    expect(await screen.findByText("Stale")).toBeInTheDocument();
    for (const name of [
      "Check all",
      "Reconnect Relay",
      "Drain Relay",
      "Disconnect Relay",
      "Check Conflict target",
      "Load more",
    ]) {
      expect(screen.getByRole("button", { name })).toBeDisabled();
    }
    expect(screen.getByRole("button", { name: "Copy target diagnostics" })).toBeEnabled();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });
});
