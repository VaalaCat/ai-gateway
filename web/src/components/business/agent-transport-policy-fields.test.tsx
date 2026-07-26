import { useState } from "react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { AgentTransportPolicy } from "@/lib/types";
import { AgentTransportPolicyFields } from "./agent-transport-policy-fields";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => ({
    title: "Transport policy",
    inbound: "Inbound",
    outbound: "Outbound",
    direct: "Direct peer-to-peer transport with a deliberately long operational label",
    relay: "Relay",
    direct_inbound_enabled: "Allow Direct inbound traffic",
    direct_outbound_enabled: "Allow Direct outbound traffic",
    relay_inbound_enabled: "Allow Relay inbound traffic",
    relay_outbound_enabled: "Allow Relay outbound traffic",
  } as Record<string, string>)[key] ?? key,
}));

const initialValue: AgentTransportPolicy = {
  direct_inbound_enabled: true,
  direct_outbound_enabled: false,
  relay_inbound_enabled: false,
  relay_outbound_enabled: true,
};

function Harness({ disabled = false }: { disabled?: boolean }) {
  const [value, setValue] = useState(initialValue);
  return (
    <>
      <AgentTransportPolicyFields value={value} disabled={disabled} onChange={setValue} />
      <output data-testid="policy-value">{JSON.stringify(value)}</output>
    </>
  );
}

describe("AgentTransportPolicyFields", () => {
  it("reflects all four initial directions and toggles each one independently", async () => {
    const user = userEvent.setup();
    render(<Harness />);

    const switches = {
      directInbound: screen.getByRole("switch", { name: "Allow Direct inbound traffic" }),
      directOutbound: screen.getByRole("switch", { name: "Allow Direct outbound traffic" }),
      relayInbound: screen.getByRole("switch", { name: "Allow Relay inbound traffic" }),
      relayOutbound: screen.getByRole("switch", { name: "Allow Relay outbound traffic" }),
    };
    expect(switches.directInbound).toBeChecked();
    expect(switches.directOutbound).not.toBeChecked();
    expect(switches.relayInbound).not.toBeChecked();
    expect(switches.relayOutbound).toBeChecked();

    await user.click(switches.directInbound);
    expect(screen.getByTestId("policy-value")).toHaveTextContent(JSON.stringify({
      ...initialValue,
      direct_inbound_enabled: false,
    }));
    await user.click(switches.directOutbound);
    await user.click(switches.relayInbound);
    await user.click(switches.relayOutbound);

    expect(screen.getByTestId("policy-value")).toHaveTextContent(JSON.stringify({
      direct_inbound_enabled: false,
      direct_outbound_enabled: true,
      relay_inbound_enabled: true,
      relay_outbound_enabled: false,
    }));
  });

  it("disables all four directions without changing the controlled value", async () => {
    const user = userEvent.setup();
    render(<Harness disabled />);

    const switches = screen.getAllByRole("switch");
    expect(switches).toHaveLength(4);
    for (const transportSwitch of switches) {
      expect(transportSwitch).toBeDisabled();
      await user.click(transportSwitch);
    }
    expect(screen.getByTestId("policy-value")).toHaveTextContent(JSON.stringify(initialValue));
  });

  it("keeps the longest label inside a stable min-width responsive grid", () => {
    render(<Harness />);

    const grid = screen.getByTestId("agent-transport-policy-grid");
    expect(grid).toHaveClass("grid", "min-w-0");
    expect(grid.className).toContain("minmax(0,1fr)");
    const longLabel = screen.getByText("Direct peer-to-peer transport with a deliberately long operational label");
    expect(longLabel).toHaveClass("min-w-0", "break-words");
    expect(within(grid).getAllByRole("switch")).toHaveLength(4);
  });
});
