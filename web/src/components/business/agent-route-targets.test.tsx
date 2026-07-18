import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { TooltipProvider } from "@/components/ui/tooltip";
import type {
  DirectPathState,
  RelayPathState,
  RouteTargetSnapshot,
  RouteTargetsPage,
} from "@/lib/types";
import { AgentRouteTargets } from "./agent-route-targets";

const { toastSuccess, toastError } = vi.hoisted(() => ({
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
}));
const writeText = vi.fn();

vi.mock("sonner", () => ({
  toast: { success: toastSuccess, error: toastError },
}));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, string | number>) => ({
    direct: "Direct",
    relay: "Relay",
    disabled: "Disabled",
    checking: "Checking",
    reachable: "Reachable",
    degraded: "Degraded",
    unreachable: "Unreachable",
    unavailable: "Unavailable",
    unknown: "Unknown",
    unsupported: "Unsupported",
    stale: "Stale",
    noRouteTargets: "No route targets",
    probeTarget: `Check ${values?.target ?? "target"}`,
    copyTargetDiagnostic: "Copy target diagnostics",
    diagnosticCopied: "Target diagnostics copied",
    diagnosticCopyFailed: "Could not copy target diagnostics",
    loadMore: "Load more",
  } as Record<string, string>)[key] ?? key,
}));

function target(
  id: string,
  directState: DirectPathState = "reachable",
  relayState: RelayPathState = "reachable",
): RouteTargetSnapshot {
  return {
    target_agent_id: id,
    target_name: `Agent ${id}`,
    direct: {
      state: directState,
      addresses: [{ url: `https://${id}.example`, tag: "wan" }],
      network: directState === "disabled" || directState === "unsupported" ? "unknown" : directState,
      identity: directState === "reachable" ? "verified" : "unknown",
      eligible: directState === "reachable",
      checking: directState === "checking",
      probe_generation: 1,
      address_fingerprint: `direct-${id}`,
      checked_at: 100,
      latency_ms: directState === "reachable" ? 12 : 0,
      last_error: {
        code: `direct_${directState}`,
        stage: "direct_probe",
        message: "",
        occurred_at: 100,
        count: 1,
      },
    },
    relay: {
      target_agent_id: id,
      target_name: `Agent ${id}`,
      state: relayState,
      stage: "response",
      checking: relayState === "checking",
      probe_generation: 2,
      relay_fingerprint: `relay-${id}`,
      source_relay_generation: 3,
      target_relay_generation: 4,
      checked_at: 100,
      latency_ms: relayState === "reachable" ? 18 : 0,
      last_error: {
        code: `relay_${relayState}`,
        stage: "response",
        message: "",
        occurred_at: 100,
        count: 1,
      },
    },
  };
}

function page(data: RouteTargetSnapshot[]): RouteTargetsPage {
  return {
    snapshot_epoch: "epoch-a",
    snapshot_seq: 7,
    observed_at: 100,
    summaries: {
      direct: { state: "unknown", reachable: 0, degraded: 0, unreachable: 0, stale: 0, total: data.length },
      relay: { state: "unknown", reachable: 0, unreachable: 0, unavailable: 0, unknown: 0, unsupported: 0, stale: 0, total: data.length },
    },
    data,
    limit: 20,
  };
}

function renderTargets(props: Partial<React.ComponentProps<typeof AgentRouteTargets>> = {}) {
  return render(
    <TooltipProvider>
      <AgentRouteTargets
        pages={[page([target("agent-b")])]}
        currentSnapshot={{ snapshot_epoch: "epoch-a", snapshot_seq: 7, observed_at: 100 }}
        {...props}
      />
    </TooltipProvider>,
  );
}

describe("AgentRouteTargets", () => {
  beforeEach(() => {
    toastSuccess.mockReset();
    toastError.mockReset();
    writeText.mockReset().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
  });

  it("shows a Direct failure and a successful Relay result for one directed target", () => {
    renderTargets({ pages: [page([target("agent-b", "unreachable", "reachable")])] });

    expect(screen.getByTestId("route-target-columns")).toHaveTextContent("DirectRelay");
    expect(screen.getByTestId("route-target-columns")).toHaveClass("hidden", "sm:grid");
    expect(screen.getByText("Agent agent-b")).toBeInTheDocument();
    expect(screen.getByText("direct_unreachable")).toBeInTheDocument();
    expect(screen.getByText("Unreachable")).toHaveAttribute("data-variant", "destructive");
    expect(screen.getByText("Reachable")).toHaveAttribute("data-variant", "default");
    expect(screen.getByText("18 ms")).toBeInTheDocument();
  });

  it("keeps unknown, unsupported, and stale neutral and hides their old error codes", () => {
    renderTargets({
      pages: [page([
        target("unknown", "unknown", "unknown"),
        target("unsupported", "unknown", "unsupported"),
        target("stale", "stale", "stale"),
      ])],
    });

    for (const label of ["Unknown", "Unsupported", "Stale"]) {
      for (const badge of screen.getAllByText(label)) {
        expect(badge).not.toHaveAttribute("data-variant", "destructive");
      }
    }
    expect(screen.queryByText(/direct_(unknown|stale)/)).not.toBeInTheDocument();
    expect(screen.queryByText(/relay_(unknown|unsupported|stale)/)).not.toBeInTheDocument();
  });

  it("renders a bounded 100-target window", () => {
    const targets = Array.from({ length: 105 }, (_, index) => target(`agent-${index}`));
    renderTargets({ pages: [page(targets)], hasNextPage: true });

    expect(screen.getAllByRole("listitem")).toHaveLength(100);
    expect(screen.queryByText("Agent agent-4")).not.toBeInTheDocument();
    expect(screen.getByText("Agent agent-5")).toBeInTheDocument();
    expect(screen.getByText("Agent agent-104")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Load more" })).not.toBeInTheDocument();
  });

  it("renders the empty boundary and ignores pages from another snapshot", () => {
    renderTargets({
      pages: [{ ...page([target("agent-b")]), snapshot_seq: 8 }],
    });

    expect(screen.getByText("No route targets")).toBeInTheDocument();
    expect(screen.queryByRole("listitem")).not.toBeInTheDocument();
  });

  it("probes one target and reports clipboard failure without throwing", async () => {
    const onProbeTarget = vi.fn();
    const user = userEvent.setup();
    const clipboardWrite = vi
      .spyOn(navigator.clipboard, "writeText")
      .mockRejectedValueOnce(new Error("clipboard denied"));
    renderTargets({ onProbeTarget });

    await user.click(screen.getByRole("button", { name: "Check Agent agent-b" }));
    expect(onProbeTarget).toHaveBeenCalledWith("agent-b");
    await user.click(screen.getByRole("button", { name: "Copy target diagnostics" }));
    expect(clipboardWrite).toHaveBeenCalledOnce();
    expect(toastError).toHaveBeenCalledWith("Could not copy target diagnostics");
  });
});
