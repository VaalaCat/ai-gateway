import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { AgentConnectionStatus } from "./agent-connection-status";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, number>) => {
    const labels: Record<string, string> = {
      enabled: "Enabled",
      disabled: "Disabled",
      connected: "Connected",
      disconnected: "Disconnected",
      degraded: "Degraded",
      healthy: "Healthy",
      unsupported: "Unsupported",
      notConfigured: "Not configured",
      ready: "Ready",
      checking: "Checking",
      streamCount: `${values?.count} streams`,
      directCounts: `${values?.reachable}/${values?.total} reachable`,
    };
    return labels[key] ?? key;
  },
}));

describe("AgentConnectionStatus", () => {
  it("uses admin state only and never derives online state from last_seen", () => {
    const { rerender } = render(<AgentConnectionStatus kind="admin" status={1} />);
    expect(screen.getByText("Enabled")).toBeInTheDocument();
    expect(screen.queryByText(/online|offline/i)).not.toBeInTheDocument();

    rerender(<AgentConnectionStatus kind="admin" status={0} />);
    expect(screen.getByText("Disabled")).toBeInTheDocument();
  });

  it("keeps disconnected authoritative even when last_seen is recent", () => {
    render(
      <AgentConnectionStatus
        kind="control"
        value={{
          state: "disconnected",
          health: "healthy",
          reason_codes: [],
          session_generation: 2,
          connected_at: 0,
          heartbeat_at: Date.now(),
          runtime_reported_at: Date.now(),
          last_seen: Date.now(),
        }}
      />,
    );

    expect(screen.getByText("Disconnected")).toBeInTheDocument();
    expect(screen.queryByText("Connected")).not.toBeInTheDocument();
  });

  it("renders neutral relay support/config states and degraded convergence separately", () => {
    const { rerender } = render(
      <AgentConnectionStatus
        kind="relay"
        value={{
          support: "unsupported",
          config: "not_configured",
          availability: "unavailable",
          accepting_new_streams: false,
          convergence: "converged",
          streams: 0,
        }}
      />,
    );
    expect(screen.getByText("Unsupported")).toHaveAttribute("data-variant", "secondary");

    rerender(
      <AgentConnectionStatus
        kind="relay"
        value={{
          support: "supported",
          config: "configured",
          availability: "ready",
          accepting_new_streams: true,
          convergence: "degraded",
          streams: 2,
        }}
      />,
    );
    expect(screen.getByText("Ready")).toBeInTheDocument();
    expect(screen.getByText("Degraded")).toHaveAttribute("data-variant", "destructive");
  });

  it("shows streams for a list summary but leaves full snapshot streams to its Runtime section", () => {
    const summary = {
      support: "supported" as const,
      config: "configured" as const,
      availability: "ready" as const,
      accepting_new_streams: true,
      convergence: "degraded" as const,
      streams: 4,
    };
    const { rerender } = render(<AgentConnectionStatus kind="relay" value={summary} />);
    expect(screen.getByText("4 streams")).toBeInTheDocument();

    rerender(
      <AgentConnectionStatus
        kind="relay"
        value={{
          support: summary.support,
          config: summary.config,
          availability: summary.availability,
          accepting_new_streams: summary.accepting_new_streams,
          convergence: summary.convergence,
          desired: { mode: "custom", configured_uri: "wss://new", effective_uri: "wss://new", desired_generation: 2 },
          active: { uri: "wss://old", active_generation: 1, session_generation: 1, connected_at: 1, streams: 4, retry_at: 2 },
          recent_errors: [],
        }}
      />,
    );
    expect(screen.queryByText("4 streams")).not.toBeInTheDocument();
    expect(screen.getByText("Ready")).toBeInTheDocument();
    expect(screen.getByText("Degraded")).toBeInTheDocument();
  });

  it("retains direct counts while a new check is in progress", () => {
    render(
      <AgentConnectionStatus
        kind="direct"
        value={{ state: "checking", reachable: 3, degraded: 1, unreachable: 1, stale: 0, total: 5 }}
      />,
    );

    expect(screen.getByText("Checking")).toBeInTheDocument();
    expect(screen.getByText("3/5 reachable")).toBeInTheDocument();
  });
});
