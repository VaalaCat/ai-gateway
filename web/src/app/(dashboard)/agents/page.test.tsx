import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { Agent } from "@/lib/types";
import AgentsPage from "./page";

const { viewport } = vi.hoisted(() => ({ viewport: { value: "lg+" as "xs" | "lg+" } }));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, string | number>) =>
    key === "connection.actionsFor" ? `Actions for ${values?.name}` : key.split(".").at(-1) ?? key,
}));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => "/agents",
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() } }));
vi.mock("@/lib/hooks/use-breakpoint", () => ({ useBreakpoint: () => viewport.value }));

const agent: Agent = {
  id: 7,
  agent_id: "agent-a",
  name: "Agent A",
  status: 1,
  tags: "edge",
  proxy_url: "",
  relay_mode: "inherit",
  peer_route_mode: "direct_first",
  last_seen: 1,
  created_at: 1,
  connection: {
    version: "v1",
    snapshot_epoch: "epoch",
    snapshot_seq: 1,
    observed_at: 1,
    control: { state: "connected", health: "healthy", reason_codes: [], session_generation: 1, connected_at: 1, heartbeat_at: 1, runtime_reported_at: 1, last_seen: 1 },
    relay: { support: "supported", config: "configured", availability: "ready", accepting_new_streams: true, convergence: "converged", streams: 0 },
    direct: { state: "reachable", reachable: 1, degraded: 0, unreachable: 0, stale: 0, total: 1 },
    targets: {
      direct: { state: "reachable", reachable: 1, degraded: 0, unreachable: 0, stale: 0, total: 1 },
      relay: { state: "reachable", reachable: 1, unreachable: 0, unavailable: 0, unknown: 0, unsupported: 0, stale: 0, total: 1 },
    },
  },
};

const mutation = { isPending: false, mutateAsync: vi.fn() };
const detailState = { data: undefined, isLoading: true, error: null };
vi.mock("@/lib/api/agents", () => ({
  useAgents: () => ({ data: { data: [agent], total: 1 }, isLoading: false }),
  useCreateAgent: () => mutation,
  useUpdateAgent: () => mutation,
  useDeleteAgent: () => mutation,
  useGenerateEnrollmentToken: () => mutation,
  useFullSyncAgents: () => mutation,
  useAgentDetail: () => detailState,
}));

describe("AgentsPage connection hierarchy", () => {
  it("shows the connection ledger as desktop columns", () => {
    viewport.value = "lg+";
    const { container } = render(<AgentsPage />);

    for (const heading of [/agent/i, /admin/i, /control/i, /direct/i, /relay/i, /actions/i]) {
      expect(screen.getByRole("columnheader", { name: heading })).toBeInTheDocument();
    }
    expect(screen.getByRole("columnheader", { name: "Select all" })).toBeInTheDocument();
    expect(screen.getAllByRole("columnheader")).toHaveLength(7);
    expect(container.querySelector("tbody dl")).not.toBeInTheDocument();
    expect(container.querySelector("[data-slot=table]")).not.toHaveClass("table-fixed");
  });

  it("keeps only Agent and Actions columns on xs and nests all states in the parent cell", () => {
    viewport.value = "xs";
    const { container } = render(<AgentsPage />);

    expect(screen.getByRole("columnheader", { name: /agent/i })).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: /actions/i })).toBeInTheDocument();
    for (const heading of [/admin/i, /control/i, /direct/i, /relay/i]) {
      expect(screen.queryByRole("columnheader", { name: heading })).not.toBeInTheDocument();
    }
    const summary = container.querySelector("tbody dl");
    expect(summary).toBeInTheDocument();
    expect(summary?.querySelectorAll("dt")).toHaveLength(4);
    expect(summary).toHaveTextContent(/admin/i);
    expect(summary).toHaveTextContent(/control/i);
    expect(summary).toHaveTextContent(/direct/i);
    expect(summary).toHaveTextContent(/relay/i);
    expect(container.querySelector("[data-slot=table]")).toHaveClass("table-fixed");
    expect(container.querySelector("col[data-column-id=actions]")).toHaveStyle({ width: "48px" });
  });

  it("opens a detail-first editor instead of initializing a submittable form from the list row", async () => {
    viewport.value = "lg+";
    const user = userEvent.setup();
    render(<AgentsPage />);

    await user.click(screen.getByRole("button", { name: "Actions for Agent A" }));
    await user.click(screen.getByRole("menuitem", { name: "edit" }));

    expect(screen.getByLabelText("loadingEdit")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "save" })).not.toBeInTheDocument();
  });
});
