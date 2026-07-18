import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
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

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => ({ control: "Control", relay: "Relay", routeTargets: "Route targets", loadingDetail: "Loading" } as Record<string, string>)[key] ?? key }));

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
    control: { state: "connected", health: "healthy", reason_codes: [], session_generation: 1, connected_at: 1, heartbeat_at: 1, runtime_reported_at: 1, last_seen: 1 },
    relay: { support: "supported", config: "configured", availability: "ready", accepting_new_streams: true, convergence: "converged", desired: { mode: "inherit", configured_uri: "", effective_uri: "wss://relay", desired_generation: 1 }, active: { uri: "wss://relay", active_generation: 1, session_generation: 1, connected_at: 1, streams: 0, retry_at: 0 }, recent_errors: [] },
    direct: { generation: 7, summary: { state: "unknown", reachable: 0, degraded: 0, unreachable: 0, stale: 0, total: 0 }, targets: {} },
    target_summaries: {
      direct: { state: "unknown", reachable: 0, degraded: 0, unreachable: 0, stale: 0, total: 0 },
      relay: { state: "unknown", reachable: 0, unreachable: 0, unavailable: 0, unknown: 0, unsupported: 0, stale: 0, total: 0 },
    },
    allowed_operations: [],
  };
}

const value = connection();
const agent: Agent = { id: 7, agent_id: "agent-a", name: "Agent A", status: 1, tags: "", proxy_url: "", relay_mode: "inherit", peer_route_mode: "direct_first", last_seen: 1, created_at: 1, connection: { version: "v1", snapshot_epoch: "epoch", snapshot_seq: 1, observed_at: 1, control: value.control, relay: { support: "supported", config: "configured", availability: "ready", accepting_new_streams: true, convergence: "converged", streams: 0 }, direct: value.direct.summary, targets: value.target_summaries } };
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
    expect(screen.getByText("Relay")).toBeInTheDocument();
    expect(screen.getByText("Route targets")).toBeInTheDocument();
    const rail = screen.getByTestId("agent-connection-rail");
    const control = screen.getByRole("heading", { name: "Control" }).closest("section");
    const relay = screen.getByRole("heading", { name: "Relay" }).closest("section");
    const targets = screen.getByRole("heading", { name: "Route targets" }).closest("section");
    expect(control).toContainElement(relay);
    expect(relay).toContainElement(targets);
    expect(rail.querySelectorAll("svg.size-4")).toHaveLength(3);
    expect(rail.querySelector("[data-slot=empty-icon]"))
      .toHaveClass("[&_svg:not([class*='size-'])]:size-6");
    expect(rail.querySelector("[data-slot=card]")).not.toBeInTheDocument();
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

  it("drops a stale Route Targets page when the monitored observation advances", async () => {
    let currentDirectPage = {
      ...detail.route_targets,
      data: [routeTarget("old-target", "Old target")],
    };
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation((input) => {
      const url = String(input);
      return Promise.resolve(new Response(JSON.stringify(url.endsWith("/detail") ? { ...detail, route_targets: currentDirectPage } : currentDirectPage), {
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
    currentDirectPage = { ...currentDirectPage, snapshot_seq: 8, observed_at: 2, data: [] };
    act(() => {
      queryClient.setQueryData(agentQueryKeys.connections(7), {
        current: next,
        retiredEpochs: [],
        stale: false,
      });
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(screen.queryByText("Old target")).not.toBeInTheDocument());
    view.unmount();
    queryClient.clear();
  });
});
