import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { RelayMode } from "@/lib/types";
import {
  AgentRelayConfigFields,
  validateRelayURI,
} from "./agent-relay-config-fields";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => ({
    relayMode: "Relay mode",
    relayModeInherit: "Inherit",
    relayModeCustom: "Custom",
    relayModeDisabled: "Disabled",
    relayUri: "Relay URI",
    effectiveRelayUri: "Effective Relay URI",
    activeStreams: "Active streams: {count}",
    relayUriRequired: "Relay URI is required",
    relayUriInvalid: "Use a valid ws:// or wss:// URI without credentials or fragments",
    relayUriTooLong: "Relay URI must be at most 2048 bytes",
  } as Record<string, string>)[key]?.replace("{count}", "3") ?? key,
}));

function Harness({ initialMode = "inherit", initialURI = "" }: { initialMode?: RelayMode; initialURI?: string }) {
  const state = { mode: initialMode, uri: initialURI };
  return (
    <AgentRelayConfigFields
      mode={state.mode}
      uri={state.uri}
      effectiveRelayURI="wss://master.example.com/agent-relay"
      activeStreams={3}
      onModeChange={vi.fn()}
      onURIChange={vi.fn()}
    />
  );
}

describe("AgentRelayConfigFields", () => {
  it("uses a single segmented Relay mode control and hides custom input for inherit", () => {
    render(<Harness />);

    const group = screen.getByRole("group", { name: "Relay mode" });
    expect(group).toHaveAttribute("data-slot", "toggle-group");
    expect(screen.getByRole("radio", { name: "Inherit" })).toHaveAttribute("data-state", "on");
    expect(screen.queryByRole("textbox", { name: "Relay URI" })).not.toBeInTheDocument();
    expect(screen.getByText("Effective Relay URI")).toBeInTheDocument();
    expect(screen.getByText("wss://master.example.com/agent-relay")).toBeInTheDocument();
  });

  it("shows active stream state when Relay is disabled", () => {
    render(<Harness initialMode="disabled" />);

    expect(screen.queryByRole("textbox", { name: "Relay URI" })).not.toBeInTheDocument();
    expect(screen.getByText("Active streams: 3")).toBeInTheDocument();
  });

  it("marks an invalid custom URI and exposes an actionable error", () => {
    render(<Harness initialMode="custom" initialURI="https://relay.example.com/#token" />);

    const input = screen.getByRole("textbox", { name: "Relay URI" });
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("alert")).toHaveTextContent("Use a valid ws:// or wss:// URI");
  });

  it("trims ASCII whitespace and enforces the server 2048-byte boundary", async () => {
    expect(validateRelayURI(" \tWSS://relay.example.com/ws?region=jp\r\n")).toEqual({
      normalized: "WSS://relay.example.com/ws?region=jp",
    });
    expect(validateRelayURI(" wss://relay.example.com ")).toEqual({
      normalized: "wss://relay.example.com",
    });
    expect(validateRelayURI(`wss://relay.example.com/${"a".repeat(2024)}`)).toEqual({
      normalized: `wss://relay.example.com/${"a".repeat(2024)}`,
    });
    expect(validateRelayURI(`wss://relay.example.com/${"é".repeat(1013)}`)).toEqual({
      error: "too_long",
    });

    const onURIChange = vi.fn();
    const user = userEvent.setup();
    render(
      <AgentRelayConfigFields
        mode="custom"
        uri=""
        effectiveRelayURI=""
        activeStreams={0}
        onModeChange={vi.fn()}
        onURIChange={onURIChange}
      />,
    );
    await user.type(screen.getByRole("textbox", { name: "Relay URI" }), " wss://relay.example.com/ws ");
    expect(onURIChange).toHaveBeenCalled();
  });

  it("enforces the canonical storage byte boundary without rewriting the submitted value", () => {
    const prefix = "wss://relay.example/";
    const canonicalBoundary = `${prefix}${"a".repeat(2048 - prefix.length - "%E7%95%8C".length)}界`;
    const canonicalOverflow = `${prefix}${"a".repeat(2048 - prefix.length - new TextEncoder().encode("界").length)}界`;

    expect(new TextEncoder().encode(new URL(canonicalBoundary).toString())).toHaveLength(2048);
    expect(validateRelayURI(canonicalBoundary)).toEqual({ normalized: canonicalBoundary });
    expect(new TextEncoder().encode(canonicalOverflow)).toHaveLength(2048);
    expect(new TextEncoder().encode(new URL(canonicalOverflow).toString())).toHaveLength(2054);
    expect(validateRelayURI(canonicalOverflow)).toEqual({ error: "too_long" });
  });

  it.each([
    ["empty fragment delimiter", "wss://relay.example/tunnel#"],
    ["invalid path escape", "wss://relay.example/path%zz"],
  ])("rejects %s", (_name, uri) => {
    expect(validateRelayURI(uri)).toEqual({ error: "invalid" });
  });

  it("accepts percent-encoded hashes in path and query", () => {
    const uri = "wss://relay.example/path%23segment?marker=%23";
    expect(validateRelayURI(uri)).toEqual({ normalized: uri });
  });

  it("rejects empty authority userinfo without rejecting literal at signs outside authority", () => {
    expect(validateRelayURI("wss://@relay.example/tunnel")).toEqual({ error: "invalid" });
    const valid = "wss://relay.example/path@segment?email=a@b";
    expect(validateRelayURI(valid)).toEqual({ normalized: valid });
  });
});
