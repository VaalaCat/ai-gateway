import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { AgentDetail } from "@/lib/types";
import { AgentEditDialog } from "./agent-edit-dialog";

const detailState = vi.hoisted(() => ({ data: undefined as AgentDetail | undefined, isLoading: true, error: null as Error | null, refetch: vi.fn() }));
const updateMutation = vi.hoisted(() => ({ mutateAsync: vi.fn(), isPending: false }));

vi.mock("@/lib/api/agents", () => ({
  useAgentDetail: (_id: number, options?: { enabled?: boolean }) => ({ ...detailState, enabled: options?.enabled }),
  useUpdateAgent: () => updateMutation,
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock("next-intl", () => ({
  useTranslations: (namespace: string) => (key: string) => ({
    "common.edit": "Edit Agent",
    "common.cancel": "Cancel",
    "common.save": "Save",
    "common.name": "Name",
    "common.success": "Saved",
    "common.error": "Could not save",
    "agents.tags": "Tags",
    "agents.proxyUrl": "Proxy URL",
    "agents.loadingEdit": "Loading Agent details",
    "agents.editLoadFailed": "Could not load Agent details",
    "agents.retry": "Retry",
    "agents.connection.relayMode": "Relay mode",
    "agents.connection.relayModeInherit": "Inherit",
    "agents.connection.relayModeCustom": "Custom",
    "agents.connection.relayModeDisabled": "Disabled",
    "agents.connection.relayUri": "Relay URI",
    "agents.peerRouteMode": "Peer route mode",
    "agents.peerRouteModeDescription": "Choose the outbound peer route policy.",
    "agents.peerRouteDirectFirst": "Direct first",
    "agents.peerRouteRelayOnly": "Relay only",
  } as Record<string, string>)[`${namespace}.${key}`] ?? key,
}));

function detail(): AgentDetail {
  return {
    id: 7, agent_id: "agent-a", name: "Agent A", status: 1, tags: "edge", proxy_url: "http://proxy.internal:8080", relay_mode: "inherit", relay_uri: "", peer_route_mode: "direct_first", last_seen: 1, created_at: 1,
    http_addresses: '[{"url":"http://runtime.example:8139","tag":"effective"}]',
    configured_http_addresses: '[{"url":"http://10.0.0.7:8139","tag":"internal"}]',
    effective_http_addresses: '[{"url":"http://runtime.example:8139","tag":"effective"}]',
    runtime: null,
    connection: {
      version: "v1", snapshot_epoch: "epoch", snapshot_seq: 1, observed_at: 1, agent_id: "agent-a", admin_status: 1,
      control: { state: "connected", health: "healthy", reason_codes: [], session_generation: 1, connected_at: 1, heartbeat_at: 1, runtime_reported_at: 1, last_seen: 1 },
      relay: { support: "supported", config: "configured", availability: "ready", accepting_new_streams: true, convergence: "converged", desired: { mode: "inherit", configured_uri: "", effective_uri: "wss://global.example.com/ws", desired_generation: 1 }, active: { uri: "wss://global.example.com/ws", active_generation: 1, session_generation: 1, connected_at: 1, streams: 2, retry_at: 0 }, recent_errors: [] },
      direct: { summary: { state: "unknown", reachable: 0, degraded: 0, unreachable: 0, stale: 0, total: 0 }, targets: {} },
      target_summaries: {
        direct: { state: "unknown", reachable: 0, degraded: 0, unreachable: 0, stale: 0, total: 0 },
        relay: { state: "unknown", reachable: 0, unreachable: 0, unavailable: 0, unknown: 0, unsupported: 0, stale: 0, total: 0 },
      },
      allowed_operations: [],
    },
    route_targets: {
      snapshot_epoch: "epoch", snapshot_seq: 1, observed_at: 1,
      summaries: {
        direct: { state: "unknown", reachable: 0, degraded: 0, unreachable: 0, stale: 0, total: 0 },
        relay: { state: "unknown", reachable: 0, unreachable: 0, unavailable: 0, unknown: 0, unsupported: 0, stale: 0, total: 0 },
      },
      data: [], limit: 20,
    },
  };
}

describe("AgentEditDialog", () => {
  beforeEach(() => {
    detailState.data = undefined;
    detailState.isLoading = true;
    detailState.error = null;
    detailState.refetch.mockReset();
    updateMutation.mutateAsync.mockReset();
    updateMutation.isPending = false;
  });

  afterEach(async () => {
    cleanup();
    await act(async () => undefined);
  });

  it("shows a Skeleton and no submittable form until authoritative detail is loaded", () => {
    render(<AgentEditDialog open agentId={7} onOpenChange={vi.fn()} />);

    expect(screen.getByLabelText("Loading Agent details")).toBeInTheDocument();
    expect(document.querySelectorAll("[data-slot=skeleton]").length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
  });

  it("retries an actionable detail loading failure without a stale list-row form", async () => {
    detailState.isLoading = false;
    detailState.error = new Error("network unavailable");
    const user = userEvent.setup();
    render(<AgentEditDialog open agentId={7} onOpenChange={vi.fn()} />);

    expect(screen.getByRole("alert")).toHaveTextContent("Could not load Agent details");
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(detailState.refetch).toHaveBeenCalledTimes(1);
  });

  it("submits only changed relay keys and preserves unrelated detail fields", async () => {
    detailState.data = detail();
    detailState.isLoading = false;
    updateMutation.mutateAsync.mockResolvedValueOnce({});
    const onOpenChange = vi.fn();
    const user = userEvent.setup();
    render(<AgentEditDialog open agentId={7} onOpenChange={onOpenChange} />);

    expect(screen.getByDisplayValue("http://10.0.0.7:8139")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("http://runtime.example:8139")).not.toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: "Custom" }));
    await user.type(screen.getByRole("textbox", { name: "Relay URI" }), "wss://relay-jp.example.com/ws/agent-relay");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(updateMutation.mutateAsync).toHaveBeenCalledWith({
      id: 7,
      relay_mode: "custom",
      relay_uri: "wss://relay-jp.example.com/ws/agent-relay",
    });
    expect(updateMutation.mutateAsync.mock.calls[0][0]).not.toHaveProperty("proxy_url");
    expect(updateMutation.mutateAsync.mock.calls[0][0]).not.toHaveProperty("tags");
    expect(updateMutation.mutateAsync.mock.calls[0][0]).not.toHaveProperty("http_addresses");
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("does not create a Relay URI patch for an unchanged custom bare host", () => {
    const value = detail();
    detailState.data = {
      ...value,
      relay_mode: "custom",
      relay_uri: "wss://relay.example.com",
      connection: {
        ...value.connection,
        relay: {
          ...value.connection.relay,
          desired: {
            ...value.connection.relay.desired,
            mode: "custom",
            configured_uri: "wss://relay.example.com",
            effective_uri: "wss://relay.example.com",
          },
        },
      },
    };
    detailState.isLoading = false;

    render(<AgentEditDialog open agentId={7} onOpenChange={vi.fn()} />);

    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(updateMutation.mutateAsync).not.toHaveBeenCalled();
  });

  it("submits relay-only as an independent outbound routing policy", async () => {
    detailState.data = detail();
    detailState.isLoading = false;
    updateMutation.mutateAsync.mockResolvedValueOnce({});
    const user = userEvent.setup();
    render(<AgentEditDialog open agentId={7} onOpenChange={vi.fn()} />);

    await user.click(screen.getByRole("radio", { name: "Relay only" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(updateMutation.mutateAsync).toHaveBeenCalledWith({
      id: 7,
      peer_route_mode: "relay_only",
    });
  });

  it("preserves a dirty draft when background detail data gets a new object identity", async () => {
    const first = detail();
    detailState.data = first;
    detailState.isLoading = false;
    updateMutation.mutateAsync.mockResolvedValueOnce({});
    const user = userEvent.setup();
    const { rerender } = render(<AgentEditDialog open agentId={7} onOpenChange={vi.fn()} />);
    const name = screen.getByRole("textbox", { name: "Name" });
    await user.clear(name);
    await user.type(name, "Dirty Name");

    detailState.data = {
      ...first,
      last_seen: 2,
      connection: { ...first.connection, snapshot_seq: 2, observed_at: 2 },
    };
    rerender(<AgentEditDialog open agentId={7} onOpenChange={vi.fn()} />);

    expect(screen.getByRole("textbox", { name: "Name" })).toHaveValue("Dirty Name");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(updateMutation.mutateAsync).toHaveBeenCalledWith({ id: 7, name: "Dirty Name" });
  });

  it("keeps the loaded dirty form during a refetch error and allows retry", async () => {
    detailState.data = detail();
    detailState.isLoading = false;
    const user = userEvent.setup();
    const { rerender } = render(<AgentEditDialog open agentId={7} onOpenChange={vi.fn()} />);
    const name = screen.getByRole("textbox", { name: "Name" });
    await user.clear(name);
    await user.type(name, "Dirty Name");

    detailState.error = new Error("background refresh failed");
    rerender(<AgentEditDialog open agentId={7} onOpenChange={vi.fn()} />);

    expect(screen.getByRole("textbox", { name: "Name" })).toHaveValue("Dirty Name");
    expect(screen.getByRole("alert")).toHaveTextContent("Could not load Agent details");
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(detailState.refetch).toHaveBeenCalledTimes(1);
  });

  it("remounts the loaded editor when switching Agent id", async () => {
    detailState.data = detail();
    detailState.isLoading = false;
    const user = userEvent.setup();
    const { rerender } = render(<AgentEditDialog open agentId={7} onOpenChange={vi.fn()} />);
    const name = screen.getByRole("textbox", { name: "Name" });
    await user.clear(name);
    await user.type(name, "Dirty Name");

    detailState.data = { ...detail(), id: 8, agent_id: "agent-b", name: "Agent B" };
    rerender(<AgentEditDialog open agentId={8} onOpenChange={vi.fn()} />);

    expect(screen.getByRole("textbox", { name: "Name" })).toHaveValue("Agent B");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });
});
