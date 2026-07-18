import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createTestQueryClient } from "@/test/render";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";
import { TooltipProvider } from "@/components/ui/tooltip";
import { AgentDiagnosticsButton } from "./agent-diagnostics-button";

const { toastError } = vi.hoisted(() => ({ toastError: vi.fn() }));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => ({
    copyDiagnostics: "Copy diagnostics",
    diagnosticsCopied: "Diagnostics copied",
    diagnosticsCopyFailed: "Could not copy diagnostics",
    diagnosticsLoadFailed: "Could not load diagnostics",
  } as Record<string, string>)[key] ?? key,
}));

vi.mock("sonner", () => ({ toast: { error: toastError } }));
vi.mock("@/lib/utils/clipboard", () => ({ copyTextWithFeedback: vi.fn().mockResolvedValue(true) }));

function renderButton() {
  return render(
    <QueryClientProvider client={createTestQueryClient()}>
      <TooltipProvider><AgentDiagnosticsButton agentId={7} /></TooltipProvider>
    </QueryClientProvider>,
  );
}

function diagnosticsResponse() {
  return {
    snapshot_epoch: "epoch-a",
    snapshot_seq: 8,
    observed_at: 100,
    control: { state: "connected", health: "healthy", reason_codes: [], session_generation: 1, connected_at: 1, heartbeat_at: 1, runtime_reported_at: 1, last_seen: 1, recent_errors: [] },
    relay: { support: "supported", config: "configured", availability: "ready", accepting_new_streams: true, convergence: "converged", desired: { mode: "inherit", configured_uri: "", effective_uri: "wss://relay.example/ws", desired_generation: 1 }, active: { uri: "wss://relay.example/ws", active_generation: 1, session_generation: 1, connected_at: 1, streams: 0, retry_at: 0 }, recent_errors: [] },
    direct: { summary: { state: "reachable", reachable: 1, degraded: 0, unreachable: 0, stale: 0, total: 1 }, recent_errors: [] },
    route_failures: [{ request_id: "request-7", source_agent_id: "source", target_agent_id: "target", route_id: 42, path_kind: "relay", stage: "commit", commit_state: "commit_uncertain", reason_code: "relay_commit_uncertain", occurred_at: 99 }],
  };
}

describe("AgentDiagnosticsButton", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    toastError.mockReset();
    vi.mocked(copyTextWithFeedback).mockClear();
  });

  it("does not load diagnostics before the operator asks for them", () => {
    const fetchMock = vi.spyOn(globalThis, "fetch");
    renderButton();

    expect(screen.getByRole("button", { name: "Copy diagnostics" })).toBeEnabled();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("copies bounded correlated diagnostics on demand", async () => {
    const value = diagnosticsResponse();
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify(value), { status: 200, headers: { "content-type": "application/json" } }));
    renderButton();

    await userEvent.click(screen.getByRole("button", { name: "Copy diagnostics" }));

    await waitFor(() => expect(copyTextWithFeedback).toHaveBeenCalledTimes(1));
    expect(fetchMock).toHaveBeenCalledWith("/api/admin/agents/7/connections/diagnostics", expect.any(Object));
    const copied = String(vi.mocked(copyTextWithFeedback).mock.calls[0][0]);
    expect(JSON.parse(copied).route_failures[0]).toMatchObject({
      request_id: "request-7", source_agent_id: "source", target_agent_id: "target", route_id: 42,
      path_kind: "relay", stage: "commit", commit_state: "commit_uncertain", reason_code: "relay_commit_uncertain",
    });
  });

  it("disables the fixed action while diagnostics are loading", async () => {
    let resolve!: (response: Response) => void;
    vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise((done) => { resolve = done; }));
    renderButton();

    await userEvent.click(screen.getByRole("button", { name: "Copy diagnostics" }));
    expect(screen.getByRole("button", { name: "Copy diagnostics" })).toBeDisabled();

    resolve(new Response(JSON.stringify(diagnosticsResponse()), { status: 200, headers: { "content-type": "application/json" } }));
    await waitFor(() => expect(copyTextWithFeedback).toHaveBeenCalledTimes(1));
  });

  it("reports a load failure without copying stale or partial diagnostics", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ message: "failed" }), { status: 500, headers: { "content-type": "application/json" } }));
    renderButton();

    await userEvent.click(screen.getByRole("button", { name: "Copy diagnostics" }));

    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Could not load diagnostics"));
    expect(copyTextWithFeedback).not.toHaveBeenCalled();
  });
});
