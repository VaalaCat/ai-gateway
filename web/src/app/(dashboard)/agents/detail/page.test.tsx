import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AgentDetail } from "@/lib/types";
import AgentDetailPage from "./page";

vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams("id=7"),
  useRouter: () => ({ push: vi.fn() }),
}));
vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => ({
    overview: "Overview",
    connections: "Connections",
    runtime: "Runtime",
    agentId: "Agent ID",
    lastSeen: "Last seen",
    tags: "Tags",
    proxyUrl: "Proxy URL",
    fullSync: "Full sync",
    syncing: "Syncing",
    httpAddresses: "HTTP addresses",
    noRuntime: "No runtime data",
  } as Record<string, string>)[key] ?? key,
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock("@/components/business/agent-connections-panel", () => ({
  AgentConnectionsPanel: ({ snapshot, stale, initialRouteTargetsPage }: { snapshot: AgentDetail["connection"]; stale: boolean; initialRouteTargetsPage?: AgentDetail["route_targets"] }) => <div>Authoritative connection panel: {snapshot.control.state}; stale={String(stale)}; direct={initialRouteTargetsPage?.snapshot_seq ?? "none"}</div>,
}));
vi.mock("@/components/business/cache-stats-table", () => ({ CacheStatsTable: () => <div>Cache stats</div> }));
vi.mock("@/components/business/inflight-table", () => ({ InflightTable: () => <div>Inflight rows</div> }));
vi.mock("@/components/observability/inflight-block-detail", () => ({ InflightBlockDetail: () => null }));

const detail: AgentDetail = {
  id: 7, agent_id: "agent-a", name: "Agent A", status: 1, tags: "edge", proxy_url: "", relay_mode: "inherit", relay_uri: "", peer_route_mode: "direct_first", last_seen: 1, created_at: 1, http_addresses: "[]",
  runtime: { uptime: 10, cached_tokens: 1, cached_channels: 2, cached_models: 3, active_connections: 4, version: 5, master_version: 5, pending_usage: 0, cache_stats: {} },
  connection: {
    version: "v1", snapshot_epoch: "epoch", snapshot_seq: 1, observed_at: 1, agent_id: "agent-a", admin_status: 1,
    control: { state: "connected", health: "healthy", reason_codes: [], session_generation: 1, connected_at: 1, heartbeat_at: 1, runtime_reported_at: 1, last_seen: 1 },
    relay: { support: "supported", config: "configured", availability: "ready", accepting_new_streams: true, convergence: "converged", desired: { mode: "inherit", configured_uri: "", effective_uri: "wss://relay", desired_generation: 1 }, active: { uri: "wss://relay", active_generation: 1, session_generation: 1, connected_at: 1, streams: 0, retry_at: 0 }, recent_errors: [] },
    direct: { generation: 7, summary: { state: "unknown", reachable: 0, degraded: 0, unreachable: 0, stale: 0, total: 0 }, targets: {} },
    target_summaries: {
      direct: { state: "unknown", reachable: 0, degraded: 0, unreachable: 0, stale: 0, total: 0 },
      relay: { state: "unknown", reachable: 0, unreachable: 0, unavailable: 0, unknown: 0, unsupported: 0, stale: 0, total: 0 },
    },
    allowed_operations: [],
  },
  route_targets: {
    snapshot_epoch: "epoch", snapshot_seq: 7, observed_at: 1,
    summaries: {
      direct: { state: "unknown", reachable: 0, degraded: 0, unreachable: 0, stale: 0, total: 0 },
      relay: { state: "unknown", reachable: 0, unreachable: 0, unavailable: 0, unknown: 0, unsupported: 0, stale: 0, total: 0 },
    },
    data: [], limit: 20,
  },
};

const detailQueryState = vi.hoisted(() => ({ data: undefined as AgentDetail | undefined, isLoading: false, isError: false, isFetching: false, refetch: vi.fn() }));
const connectionQueryState = vi.hoisted(() => ({ data: undefined as AgentDetail["connection"] | undefined, stale: false, isFetching: false }));

vi.mock("@/lib/api/agents", () => ({
  useAgentDetail: () => detailQueryState,
  useFullSyncAgents: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useAgentInflight: () => ({ data: [], isFetching: false, refetch: vi.fn() }),
  useAgentGoroutines: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useInterruptInflight: () => ({ mutate: vi.fn() }),
}));
vi.mock("@/lib/hooks/use-agent-connections", () => ({
  useAgentConnections: () => connectionQueryState,
}));

describe("AgentDetailPage", () => {
  beforeEach(() => {
    detailQueryState.data = detail;
    detailQueryState.isLoading = false;
    detailQueryState.isError = false;
    detailQueryState.isFetching = false;
    detailQueryState.refetch.mockReset();
    connectionQueryState.data = undefined;
    connectionQueryState.stale = false;
    connectionQueryState.isFetching = false;
  });

  it("organizes existing Agent data under Overview, Connections, and Runtime tabs", async () => {
    const user = userEvent.setup();
    render(<AgentDetailPage />);

    expect(screen.getByRole("tab", { name: "Overview" })).toHaveAttribute("data-state", "active");
    expect(screen.getByRole("tab", { name: "Connections" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Runtime" })).toBeInTheDocument();
    expect(screen.getByText("agent-a")).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: "Connections" }));
    expect(screen.getByText(/Authoritative connection panel/)).toBeInTheDocument();
    expect(screen.getByText(/direct=7/)).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: "Runtime" }));
    expect(screen.getByText("Inflight rows")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /checkConnectivity/i })).not.toBeInTheDocument();
  });

  it("uses the monitored accepted snapshot and disables side effects while stale", async () => {
    const user = userEvent.setup();
    connectionQueryState.data = {
      ...detail.connection,
      snapshot_seq: 2,
      control: { ...detail.connection.control, state: "disconnected" },
    };
    connectionQueryState.stale = true;
    render(<AgentDetailPage />);

    expect(screen.getByRole("button", { name: "Full sync" })).toBeDisabled();
    await user.click(screen.getByRole("tab", { name: "Connections" }));
    expect(screen.getByText("Authoritative connection panel: disconnected; stale=true; direct=7")).toBeInTheDocument();
  });

  it("shows an actionable retry when the initial detail request fails", async () => {
    detailQueryState.data = undefined;
    detailQueryState.isError = true;
    const user = userEvent.setup();
    render(<AgentDetailPage />);

    expect(screen.getByRole("alert")).toHaveTextContent("detailLoadFailed");
    await user.click(screen.getByRole("button", { name: "retry" }));
    expect(detailQueryState.refetch).toHaveBeenCalledTimes(1);
  });

  it("keeps operations enabled when only the detail refetch fails and monitoring is fresh", async () => {
    detailQueryState.isError = true;
    connectionQueryState.data = detail.connection;
    connectionQueryState.stale = false;
    const user = userEvent.setup();
    render(<AgentDetailPage />);

    expect(screen.getByRole("alert")).toHaveTextContent("detailLoadFailed");
    expect(screen.getByRole("button", { name: "Full sync" })).toBeEnabled();
    await user.click(screen.getByRole("tab", { name: "Connections" }));
    expect(screen.getByText("Authoritative connection panel: connected; stale=false; direct=7")).toBeInTheDocument();
  });
});
