import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { TooltipProvider } from "@/components/ui/tooltip";
import type { AgentTransportPolicySnapshot } from "@/lib/types";
import { AgentTransportPolicyStatus } from "./agent-transport-policy-status";

vi.mock("next-intl", () => ({
  useTranslations: (namespace: string) => (key: string) => ({
    "agents.transportPolicy.title": "Transport policy",
    "agents.transportPolicy.inbound": "Inbound",
    "agents.transportPolicy.outbound": "Outbound",
    "agents.transportPolicy.direct": "Direct",
    "agents.transportPolicy.relay": "Relay",
    "agents.transportPolicy.configured": "Configured",
    "agents.transportPolicy.effective": "Effective",
    "agents.transportPolicy.on": "On",
    "agents.transportPolicy.off": "Off",
    "agents.connection.sourceDirectOutboundDisabled": "Source Direct outbound is disabled",
    "agents.connection.targetDirectInboundDisabled": "Target Direct inbound is disabled",
    "agents.connection.sourceRelayOutboundDisabled": "Source Relay outbound is disabled",
    "agents.connection.targetRelayInboundDisabled": "Target Relay inbound is disabled",
    "agents.connection.relayConnectionDisabled": "Relay connection is disabled",
  } as Record<string, string>)[`${namespace}.${key}`] ?? key,
}));

function policy(overrides: Partial<AgentTransportPolicySnapshot> = {}): AgentTransportPolicySnapshot {
  return {
    direct_inbound: { configured: true, effective: true },
    direct_outbound: { configured: false, effective: false },
    relay_inbound: { configured: true, effective: true },
    relay_outbound: { configured: false, effective: false },
    ...overrides,
  };
}

function renderStatus(value: AgentTransportPolicySnapshot, compact = false) {
  return render(
    <TooltipProvider>
      <AgentTransportPolicyStatus value={value} compact={compact} />
    </TooltipProvider>,
  );
}

describe("AgentTransportPolicyStatus", () => {
  it("shows configured and effective values for all four directions", () => {
    renderStatus(policy());

    const directInbound = screen.getByTestId("transport-policy-direct-inbound");
    expect(directInbound).toHaveTextContent("Direct");
    expect(directInbound).toHaveTextContent("Inbound");
    expect(directInbound).toHaveTextContent("Effective On");
    expect(directInbound).not.toHaveTextContent("Configured");

    const directOutbound = screen.getByTestId("transport-policy-direct-outbound");
    expect(directOutbound).toHaveTextContent("Effective Off");
    expect(screen.getByTestId("transport-policy-relay-inbound")).toHaveTextContent("Effective On");
    expect(screen.getByTestId("transport-policy-relay-outbound")).toHaveTextContent("Effective Off");
    expect(screen.getAllByTestId(/transport-policy-(direct|relay)-(inbound|outbound)/)).toHaveLength(4);
  });

  it("keeps the configured value visible and exposes the reason when effective differs", async () => {
    const user = userEvent.setup();
    renderStatus(policy({
      direct_outbound: {
        configured: true,
        effective: false,
        reason_code: "source_direct_outbound_disabled",
      },
    }));

    const cell = screen.getByTestId("transport-policy-direct-outbound");
    expect(cell).toHaveTextContent("Effective Off");
    expect(cell).toHaveTextContent("Configured On");
    const reason = within(cell).getByLabelText(/Source Direct outbound is disabled/);
    expect(reason).toHaveAccessibleName(/source_direct_outbound_disabled/);
    await user.hover(reason);
    expect(await screen.findByRole("tooltip")).toHaveTextContent("Source Direct outbound is disabled");
  });

  it("uses the same four-cell status surface in compact mode for RelayMode disabled", () => {
    const value = policy({
      relay_inbound: {
        configured: true,
        effective: false,
        reason_code: "relay_connection_disabled",
      },
      relay_outbound: {
        configured: true,
        effective: false,
        reason_code: "relay_connection_disabled",
      },
    });
    const full = renderStatus(value);
    expect(screen.getByTestId("agent-transport-policy-status")).toHaveAttribute("data-compact", "false");
    expect(screen.getAllByLabelText(/Relay connection is disabled/)).toHaveLength(2);
    full.unmount();

    renderStatus(value, true);
    const root = screen.getByTestId("agent-transport-policy-status");
    expect(root).toHaveAttribute("data-compact", "true");
    expect(within(root).getAllByTestId(/transport-policy-(direct|relay)-(inbound|outbound)/)).toHaveLength(4);
    expect(within(root).getAllByLabelText(/Relay connection is disabled/)).toHaveLength(2);
  });
});
