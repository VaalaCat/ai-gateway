import { render, screen, waitFor } from "@testing-library/react";
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
    sourceDirectOutboundDisabled: "Source Direct outbound is disabled",
    targetDirectInboundDisabled: "Target Direct inbound is disabled",
    sourceRelayOutboundDisabled: "Source Relay outbound is disabled",
    targetRelayInboundDisabled: "Target Relay inbound is disabled",
    relayConnectionDisabled: "Relay connection is disabled",
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
      direct: { state: "unknown", disabled: 0, reachable: 0, degraded: 0, unreachable: 0, stale: 0, total: data.length },
      relay: { state: "unknown", disabled: 0, reachable: 0, unreachable: 0, unavailable: 0, unknown: 0, unsupported: 0, stale: 0, total: data.length },
    },
    data,
    limit: 20,
  };
}

function allDisabledPage(): RouteTargetsPage {
  const sourcePolicy = target("source-policy", "disabled", "disabled");
  sourcePolicy.direct.policy_reason = "source_direct_outbound_disabled";
  sourcePolicy.relay.policy_reason = "source_relay_outbound_disabled";
  const targetPolicy = target("target-policy", "disabled", "disabled");
  targetPolicy.direct.policy_reason = "target_direct_inbound_disabled";
  targetPolicy.relay.policy_reason = "target_relay_inbound_disabled";
  return {
    ...page([sourcePolicy, targetPolicy]),
    summaries: {
      direct: { state: "disabled", disabled: 2, reachable: 0, degraded: 0, unreachable: 0, stale: 0, total: 2 },
      relay: { state: "disabled", disabled: 2, reachable: 0, unreachable: 0, unavailable: 0, unknown: 0, unsupported: 0, stale: 0, total: 2 },
    },
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

  it("shows all four policy reasons for disabled Direct and Relay paths", async () => {
    const user = userEvent.setup();
    const sourcePolicy = target("source-policy", "disabled", "disabled");
    sourcePolicy.direct.policy_reason = "source_direct_outbound_disabled";
    sourcePolicy.relay.policy_reason = "source_relay_outbound_disabled";
    const targetPolicy = target("target-policy", "disabled", "disabled");
    targetPolicy.direct.policy_reason = "target_direct_inbound_disabled";
    targetPolicy.relay.policy_reason = "target_relay_inbound_disabled";
    renderTargets({ pages: [page([sourcePolicy, targetPolicy])] });

    for (const reasonCode of [
      "source_direct_outbound_disabled",
      "target_direct_inbound_disabled",
      "source_relay_outbound_disabled",
      "target_relay_inbound_disabled",
    ]) {
      expect(screen.getByText(reasonCode)).toBeInTheDocument();
    }
    for (const badge of screen.getAllByText("Disabled")) {
      expect(badge).toHaveAttribute("data-variant", "secondary");
    }

    await user.hover(screen.getByText("source_direct_outbound_disabled"));
    expect(await screen.findByRole("tooltip")).toHaveTextContent("Source Direct outbound is disabled");
  });

  it("renders an all-disabled API page without weakening the mixed summary fixture", () => {
    const mixed = page([target("mixed")]);
    expect(mixed.summaries.direct.disabled).toBe(0);
    expect(mixed.summaries.relay.disabled).toBe(0);

    renderTargets({ pages: [allDisabledPage()] });

    expect(screen.getAllByRole("listitem")).toHaveLength(2);
    expect(screen.getAllByText("Disabled")).toHaveLength(4);
    expect(screen.getByText("source_direct_outbound_disabled")).toBeInTheDocument();
    expect(screen.getByText("target_relay_inbound_disabled")).toBeInTheDocument();
  });

  it("prefers policy_reason over last_error and copies the complete target diagnostic", async () => {
    const user = userEvent.setup();
    const clipboardWrite = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue(undefined);
    const disabled = target("agent-policy", "disabled", "disabled");
    disabled.direct.policy_reason = "target_direct_inbound_disabled";
    disabled.relay.policy_reason = "relay_connection_disabled";
    renderTargets({ pages: [page([disabled])] });

    expect(screen.getByText("target_direct_inbound_disabled")).toBeInTheDocument();
    expect(screen.getByText("relay_connection_disabled")).toBeInTheDocument();
    expect(screen.queryByText("direct_disabled")).not.toBeInTheDocument();
    expect(screen.queryByText("relay_disabled")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Copy target diagnostics" }));
    await waitFor(() => expect(clipboardWrite).toHaveBeenCalledOnce());
    expect(JSON.parse(clipboardWrite.mock.calls[0][0])).toEqual(disabled);
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

  it("shows skeletons only when loading without targets", () => {
    const { container } = renderTargets({ pages: undefined, isLoading: true });

    expect(container.querySelectorAll("[data-slot=skeleton]")).toHaveLength(2);
    expect(screen.queryByRole("listitem")).not.toBeInTheDocument();
  });

  it("keeps matching targets visible during background loading", () => {
    const { container } = renderTargets({
      pages: [page([target("agent-b")])],
      isLoading: true,
    });

    expect(screen.getByText("Agent agent-b")).toBeInTheDocument();
    expect(container.querySelector("[data-slot=skeleton]")).not.toBeInTheDocument();
  });

  it("keeps targets visible while the next page is fetching", () => {
    const first = { ...page([target("agent-b")]), next_cursor: "next" };
    const { container } = renderTargets({
      pages: [first],
      hasNextPage: true,
      isFetchingNextPage: true,
    });

    expect(screen.getByText("Agent agent-b")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Load more" })).toBeDisabled();
    expect(container.querySelector("[data-slot=skeleton]")).not.toBeInTheDocument();
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

  it("disables snapshot-dependent actions while keeping diagnostics available", async () => {
    const user = userEvent.setup();
    const clipboardWrite = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue(undefined);
    const onProbeTarget = vi.fn();
    const onLoadMore = vi.fn();
    renderTargets({
      operationsDisabled: true,
      hasNextPage: true,
      onProbeTarget,
      onLoadMore,
    });

    expect(screen.getByRole("button", { name: "Check Agent agent-b" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Load more" })).toBeDisabled();
    const copyButton = screen.getByRole("button", { name: "Copy target diagnostics" });
    expect(copyButton).toBeEnabled();

    await user.click(copyButton);
    await waitFor(() => expect(clipboardWrite).toHaveBeenCalledOnce());
    expect(onProbeTarget).not.toHaveBeenCalled();
    expect(onLoadMore).not.toHaveBeenCalled();
  });
});
