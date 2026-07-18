import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { ConnectionSnapshot } from "@/lib/types";
import { AgentOperationButtons } from "./agent-operation-buttons";
import { TooltipProvider } from "@/components/ui/tooltip";

const probeMutate = vi.fn();
const operationMutate = vi.fn();
const { toastError } = vi.hoisted(() => ({ toastError: vi.fn() }));
const { mutationState } = vi.hoisted(() => ({
  mutationState: {
    probePending: false,
    operationPending: false,
    operationVariables: undefined as { operation: string } | undefined,
  },
}));

vi.mock("@/lib/api/agents", () => ({
  useEnqueueConnectivityProbe: () => ({
    mutateAsync: probeMutate,
    isPending: mutationState.probePending,
  }),
  useAgentOperation: () => ({
    mutateAsync: operationMutate,
    isPending: mutationState.operationPending,
    variables: mutationState.operationVariables,
  }),
}));

vi.mock("sonner", () => ({ toast: { error: toastError } }));

vi.mock("@/lib/api/error-toast", () => ({
  formatErrorToast: (_error: unknown, fallback: string) => fallback,
}));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => ({
    probe: "Probe",
    reconnect: "Reconnect",
    drain: "Drain",
    disconnect: "Disconnect",
    confirmDrain: "Drain relay?",
    confirmDisconnect: "Disconnect relay?",
    confirmDescription: "This changes relay traffic.",
    cancel: "Cancel",
    confirm: "Confirm",
    staleDenied: "Refresh connection state first",
    operationDenied: "Operation is not allowed",
    operationFailed: "Operation failed",
  } as Record<string, string>)[key] ?? key,
}));

function snapshot(allowed = true): ConnectionSnapshot {
  return {
    version: "v1", snapshot_epoch: "epoch", snapshot_seq: 2, observed_at: 3, agent_id: "a", admin_status: 1,
    control: { state: "connected", health: "healthy", reason_codes: [], session_generation: 4, connected_at: 1, heartbeat_at: 2, runtime_reported_at: 2, last_seen: 2 },
    relay: {
      support: "supported", config: "configured", availability: "ready", accepting_new_streams: true, convergence: "converged",
      desired: { mode: "inherit", configured_uri: "", effective_uri: "wss://relay", desired_generation: 5 },
      active: { uri: "wss://relay", active_generation: 5, session_generation: 6, connected_at: 1, streams: 0, retry_at: 0 }, recent_errors: [],
    },
    direct: { summary: { state: "reachable", reachable: 1, degraded: 0, unreachable: 0, stale: 0, total: 1 }, targets: {} },
    target_summaries: {
      direct: { state: "reachable", reachable: 1, degraded: 0, unreachable: 0, stale: 0, total: 1 },
      relay: { state: "reachable", reachable: 1, unreachable: 0, unavailable: 0, unknown: 0, unsupported: 0, stale: 0, total: 1 },
    },
    allowed_operations: ["probe", "relay_reconnect", "relay_drain", "relay_disconnect"].map((operation) => ({ operation: operation as never, allowed, denial_code: allowed ? undefined : "control_disconnected" })),
  };
}

describe("AgentOperationButtons", () => {
  beforeEach(() => {
    probeMutate.mockReset();
    operationMutate.mockReset();
    toastError.mockReset();
    mutationState.probePending = false;
    mutationState.operationPending = false;
    mutationState.operationVariables = undefined;
  });

  it("disables stale operations and explains why", async () => {
    const user = userEvent.setup();
    render(<TooltipProvider><AgentOperationButtons agentId={7} snapshot={snapshot()} stale /></TooltipProvider>);
    const probe = screen.getByRole("button", { name: "Probe" });
    expect(probe).toBeDisabled();
    await user.hover(probe.parentElement!);
    expect(await screen.findByRole("tooltip")).toHaveTextContent("Refresh connection state first");
  });

  it("disables denied operations and shows the denial code", async () => {
    const user = userEvent.setup();
    render(<TooltipProvider><AgentOperationButtons agentId={7} snapshot={snapshot(false)} /></TooltipProvider>);
    const reconnect = screen.getByRole("button", { name: "Reconnect" });
    expect(reconnect).toBeDisabled();
    await user.hover(reconnect.parentElement!);
    expect(await screen.findByRole("tooltip")).toHaveTextContent("control_disconnected");
  });

  it("supports keyboard activation for immediate actions", async () => {
    const user = userEvent.setup();
    probeMutate.mockResolvedValueOnce({ state: "queued" });
    render(<TooltipProvider><AgentOperationButtons agentId={7} snapshot={snapshot()} /></TooltipProvider>);
    await user.tab();
    expect(screen.getByRole("button", { name: "Probe" })).toHaveFocus();
    await user.keyboard("{Enter}");
    expect(probeMutate).toHaveBeenCalledWith(expect.objectContaining({
      id: 7,
      request: expect.objectContaining({ expected_relay_generation: 6 }),
    }));
  });

  it("reports a rejected immediate operation instead of leaking its promise", async () => {
    const user = userEvent.setup();
    operationMutate.mockRejectedValueOnce(new Error("socket closed"));
    render(<TooltipProvider><AgentOperationButtons agentId={7} snapshot={snapshot()} /></TooltipProvider>);

    await user.click(screen.getByRole("button", { name: "Reconnect" }));
    expect(toastError).toHaveBeenCalledWith("Operation failed");
  });

  it("requires confirmation before drain", async () => {
    const user = userEvent.setup();
    operationMutate.mockResolvedValueOnce({ state: "accepted" });
    render(<TooltipProvider><AgentOperationButtons agentId={7} snapshot={snapshot()} /></TooltipProvider>);
    await user.click(screen.getByRole("button", { name: "Drain" }));
    expect(screen.getByRole("alertdialog", { name: "Drain relay?" })).toBeInTheDocument();
    expect(operationMutate).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Confirm" }));
    expect(operationMutate).toHaveBeenCalledWith(expect.objectContaining({ operation: "relay_drain" }));
  });

  it.each(["stale", "loading", "denied"] as const)(
    "blocks a previously opened confirmation after state becomes %s",
    async (state) => {
      const user = userEvent.setup();
      const { rerender } = render(
        <TooltipProvider>
          <AgentOperationButtons agentId={7} snapshot={snapshot()} />
        </TooltipProvider>,
      );
      await user.click(screen.getByRole("button", { name: "Drain" }));

      rerender(
        <TooltipProvider>
          <AgentOperationButtons
            agentId={7}
            snapshot={state === "denied" ? snapshot(false) : snapshot()}
            stale={state === "stale"}
            loading={state === "loading"}
          />
        </TooltipProvider>,
      );
      const confirm = screen.getByRole("button", { name: "Confirm" });
      expect(confirm).toBeDisabled();
      await user.click(confirm);
      expect(operationMutate).not.toHaveBeenCalled();
    },
  );

  it("disables every operation while a probe is pending and spins only Probe", () => {
    mutationState.probePending = true;
    render(<TooltipProvider><AgentOperationButtons agentId={7} snapshot={snapshot()} /></TooltipProvider>);

    for (const name of ["Probe", "Reconnect", "Drain", "Disconnect"]) {
      expect(screen.getByRole("button", { name })).toBeDisabled();
    }
    expect(screen.getByRole("button", { name: "Probe" }).querySelector(".animate-spin")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Drain" }).querySelector(".animate-spin")).not.toBeInTheDocument();
  });

  it("disables every operation while a relay operation is pending and spins only that operation", () => {
    mutationState.operationPending = true;
    mutationState.operationVariables = { operation: "relay_drain" };
    render(<TooltipProvider><AgentOperationButtons agentId={7} snapshot={snapshot()} /></TooltipProvider>);

    for (const name of ["Probe", "Reconnect", "Drain", "Disconnect"]) {
      expect(screen.getByRole("button", { name })).toBeDisabled();
    }
    expect(screen.getByRole("button", { name: "Drain" }).querySelector(".animate-spin")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Probe" }).querySelector(".animate-spin")).not.toBeInTheDocument();
  });
});
