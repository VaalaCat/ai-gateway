import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Agent } from "@/lib/types";
import AgentsPage from "./page";

const { navigation, queryState, viewport } = vi.hoisted(() => ({
  navigation: { push: vi.fn(), query: "", replace: vi.fn() },
  queryState: { current: {} as Record<string, unknown> },
  viewport: { value: "lg+" as "xs" | "lg+" },
}));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, string | number>) =>
    key === "connection.actionsFor" ? `Actions for ${values?.name}` : key.split(".").at(-1) ?? key,
}));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: navigation.push, replace: navigation.replace }),
  usePathname: () => "/agents",
  useSearchParams: () => new URLSearchParams(navigation.query),
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
  direct_inbound_enabled: true,
  direct_outbound_enabled: true,
  relay_inbound_enabled: true,
  relay_outbound_enabled: true,
  last_seen: 1,
  created_at: 1,
  connection: {
    version: "v1",
    snapshot_epoch: "epoch",
    snapshot_seq: 1,
    observed_at: 1,
    transport_policy: {
      direct_inbound: { configured: true, effective: true },
      direct_outbound: { configured: true, effective: true },
      relay_inbound: { configured: true, effective: true },
      relay_outbound: { configured: true, effective: true },
    },
    control: { state: "connected", health: "healthy", reason_codes: [], session_generation: 1, connected_at: 1, heartbeat_at: 1, runtime_reported_at: 1, last_seen: 1 },
    relay: { support: "supported", config: "configured", availability: "ready", accepting_new_streams: true, convergence: "converged", streams: 0 },
    direct: { state: "reachable", disabled: 0, reachable: 1, degraded: 0, unreachable: 0, stale: 0, total: 1 },
    targets: {
      direct: { state: "reachable", disabled: 0, reachable: 1, degraded: 0, unreachable: 0, stale: 0, total: 1 },
      relay: { state: "reachable", disabled: 0, reachable: 1, unreachable: 0, unavailable: 0, unknown: 0, unsupported: 0, stale: 0, total: 1 },
    },
  },
};

const mutation = { isPending: false, mutateAsync: vi.fn() };
const detailState = { data: undefined, isLoading: true, error: null };
vi.mock("@/lib/api/agents", () => ({
  useAgents: () => queryState.current,
  useCreateAgent: () => mutation,
  useUpdateAgent: () => mutation,
  useDeleteAgent: () => mutation,
  useGenerateEnrollmentToken: () => mutation,
  useFullSyncAgents: () => mutation,
  useAgentDetail: () => detailState,
}));

const refetch = vi.fn();

beforeEach(() => {
  viewport.value = "lg+";
  navigation.query = "";
  navigation.push.mockReset();
  navigation.replace.mockReset();
  navigation.replace.mockImplementation((url: string) => {
    navigation.query = url.split("?")[1] ?? "";
  });
  refetch.mockReset();
  queryState.current = {
    data: { data: [agent], total: 1 },
    isPending: false,
    isError: false,
    isFetching: false,
    refetch,
  };
});

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

describe("AgentsPage list states", () => {
  it("shows skeletons with a loading label during the initial request", () => {
    queryState.current = {
      data: undefined,
      isPending: true,
      isError: false,
      isFetching: true,
      refetch,
    };
    const { container } = render(<AgentsPage />);

    expect(screen.getByText("loadingAgents")).toBeInTheDocument();
    expect(container.querySelector("[data-slot=skeleton]")).toBeInTheDocument();
    expect(screen.queryByText("noData")).not.toBeInTheDocument();
  });

  it("shows an alert with retry when the initial request fails", async () => {
    queryState.current = {
      data: undefined,
      isPending: false,
      isError: true,
      isFetching: false,
      refetch,
    };
    const user = userEvent.setup();
    render(<AgentsPage />);

    expect(screen.getByRole("alert")).toHaveTextContent("listLoadFailed");
    expect(screen.queryByText("noData")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "retry" }));
    expect(refetch).toHaveBeenCalledTimes(1);
  });

  it("keeps background refresh inside the stable toolbar slot", () => {
    queryState.current = {
      data: { data: [agent], total: 1 },
      isPending: false,
      isError: false,
      isFetching: true,
      refetch,
    };
    render(<AgentsPage />);

    const slot = screen.getByTestId("agents-refresh-status");
    expect(slot).toHaveAttribute("role", "status");
    expect(slot.querySelector("svg")).toHaveClass("animate-spin");
    expect(screen.getByText("Agent A")).toBeInTheDocument();
    expect(screen.queryByText("refreshing")).not.toBeInTheDocument();
    expect(screen.queryByText("noData")).not.toBeInTheDocument();
    expect(slot.parentElement).toContainElement(screen.getByRole("button", { name: "createAgent" }));
  });

  it("keeps the idle refresh slot mounted", () => {
    render(<AgentsPage />);

    const slot = screen.getByTestId("agents-refresh-status");
    expect(slot).toHaveClass("size-8");
    expect(slot).not.toHaveAttribute("role");
    expect(slot.querySelector("svg")).toHaveClass("invisible");
  });

  it("keeps stale data visible with retry when a refresh fails", async () => {
    queryState.current = {
      data: { data: [agent], total: 1 },
      isPending: false,
      isError: true,
      isFetching: false,
      refetch,
    };
    const user = userEvent.setup();
    render(<AgentsPage />);

    expect(screen.getByText("Agent A")).toBeInTheDocument();
    expect(screen.getByText("dataStale")).toBeInTheDocument();
    expect(screen.getByTestId("agents-refresh-status").querySelector("svg")).toHaveClass("invisible");
    expect(screen.queryByText("noData")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "retry" }));
    expect(refetch).toHaveBeenCalledTimes(1);
  });

  it("shows no data only after a successful empty response", () => {
    queryState.current = {
      data: { data: [], total: 0 },
      isPending: false,
      isError: false,
      isFetching: false,
      refetch,
    };
    render(<AgentsPage />);

    expect(screen.getByText("noData")).toBeInTheDocument();
    expect(screen.queryByText("loadingAgents")).not.toBeInTheDocument();
    expect(screen.queryByText("listLoadFailed")).not.toBeInTheDocument();
  });

  it("clears selected rows when the search changes", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<AgentsPage />);

    await user.click(screen.getByRole("checkbox", { name: "Select row" }));
    await user.click(screen.getAllByRole("button", { name: "more" }).at(-1)!);
    expect(screen.getByRole("menuitem", { name: "fullSyncSelected" })).toBeInTheDocument();
    await user.keyboard("{Escape}");

    await user.type(screen.getByPlaceholderText("search"), "edge");
    await waitFor(() => expect(navigation.replace).toHaveBeenCalled());
    rerender(<AgentsPage />);
    await waitFor(() => expect(screen.getByRole("checkbox", { name: "Select row" })).not.toBeChecked());

    await user.click(screen.getAllByRole("button", { name: "more" }).at(-1)!);
    expect(screen.queryByRole("menuitem", { name: "fullSyncSelected" })).not.toBeInTheDocument();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("menu")).not.toBeInTheDocument());
  });
});
