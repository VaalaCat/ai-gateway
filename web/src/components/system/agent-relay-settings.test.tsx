import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AgentRelaySettings } from "./agent-relay-settings";

const mocks = vi.hoisted(() => ({
  query: {
    data: {
      settings: {
        "agent.relay_default_uri": "",
        "agent.relay_fallback_enabled": "0",
        "agent.connectivity_probe_success_ttl_seconds": "300",
        "agent.connectivity_probe_failure_retry_min_seconds": "30",
        "agent.connectivity_probe_failure_retry_max_seconds": "300",
      },
    },
    isPending: false,
    isError: false,
    isFetching: false,
    refetch: vi.fn(),
  },
  mutateAsync: vi.fn(),
}));

vi.mock("@/lib/api/system", () => ({
  useSettings: () => mocks.query,
  useUpdateAgentRelaySettings: () => ({
    mutateAsync: mocks.mutateAsync,
    isPending: false,
  }),
}));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => ({
    title: "Agent relay",
    description: "Control the default relay route and fallback admission.",
    defaultUri: "Default Relay URI",
    defaultUriDescription: "Inherited by Agents without a custom URI.",
    defaultUriPlaceholder: "wss://relay.example.com/ws/agent-relay",
    fallbackEnabled: "Relay fallback",
    fallbackEnabledDescription: "Allow new requests to use Relay after Direct fails.",
    probeSuccessTtl: "Success result TTL",
    probeSuccessTtlDescription: "Seconds to keep a successful result.",
    probeRetryMin: "Minimum failure retry",
    probeRetryMinDescription: "Seconds after the first failure.",
    probeRetryMax: "Maximum failure retry",
    probeRetryMaxDescription: "Maximum repeated failure backoff.",
    rangeSeconds: "Enter a valid number of seconds.",
    probeRetryMaxError: "Maximum retry must be at least the minimum.",
    derivedFromMaster: "Derived from Master URL",
    derivedFromMasterDescription: "Each Agent derives its Relay URI from its Master URL when possible.",
    invalidUri: "Use an absolute ws:// or wss:// URI without credentials, fragments, or malformed query escapes.",
    uriTooLong: "Relay URI must be at most 2048 bytes.",
    save: "Save relay settings",
    saving: "Saving",
    loading: "Loading relay settings",
    loadFailed: "Relay settings could not be loaded",
    loadFailedDescription: "Retry before changing relay settings.",
    refreshFailed: "Relay settings could not be refreshed",
    refreshFailedDescription: "Showing the last loaded values. Your draft has been kept.",
    retry: "Retry",
    saveFailed: "Relay settings were not saved. Check the values and try again.",
    saved: "Relay settings saved",
  } as Record<string, string>)[key] ?? key,
}));

function setSettings(
  uri: string,
  enabled: "0" | "1",
  timings = { successTTL: "300", retryMin: "30", retryMax: "300" },
) {
  mocks.query.data = {
    settings: {
      "agent.relay_default_uri": uri,
      "agent.relay_fallback_enabled": enabled,
      "agent.connectivity_probe_success_ttl_seconds": timings.successTTL,
      "agent.connectivity_probe_failure_retry_min_seconds": timings.retryMin,
      "agent.connectivity_probe_failure_retry_max_seconds": timings.retryMax,
    },
  };
}

describe("AgentRelaySettings", () => {
  beforeEach(() => {
    setSettings("", "0");
    mocks.query.isPending = false;
    mocks.query.isError = false;
    mocks.query.isFetching = false;
    mocks.query.refetch.mockReset().mockResolvedValue({});
    mocks.mutateAsync.mockReset().mockResolvedValue({ settings: {} });
  });

  it("keeps an unchanged form unsavable and sends only the changed switch", async () => {
    const user = userEvent.setup();
    render(<AgentRelaySettings />);

    const save = screen.getByRole("button", { name: "Save relay settings" });
    expect(save).toBeDisabled();
    expect(screen.getByText("Derived from Master URL")).toBeInTheDocument();
    expect(screen.getByText("Each Agent derives its Relay URI from its Master URL when possible.")).toBeInTheDocument();
    expect(screen.getByRole("spinbutton", { name: "Success result TTL" })).toHaveValue(300);
    expect(screen.getByRole("spinbutton", { name: "Minimum failure retry" })).toHaveValue(30);
    expect(screen.getByRole("spinbutton", { name: "Maximum failure retry" })).toHaveValue(300);

    await user.click(screen.getByRole("switch", { name: "Relay fallback" }));
    await user.click(save);

    expect(mocks.mutateAsync).toHaveBeenCalledWith({
      settings: { "agent.relay_fallback_enabled": "1" },
    });
  });

  it("saves all changed Scheduler timings in one settings patch", async () => {
    const user = userEvent.setup();
    render(<AgentRelaySettings />);

    fireEvent.change(screen.getByRole("spinbutton", { name: "Success result TTL" }), { target: { value: "600" } });
    fireEvent.change(screen.getByRole("spinbutton", { name: "Minimum failure retry" }), { target: { value: "45" } });
    fireEvent.change(screen.getByRole("spinbutton", { name: "Maximum failure retry" }), { target: { value: "900" } });
    await user.click(screen.getByRole("button", { name: "Save relay settings" }));

    expect(mocks.mutateAsync).toHaveBeenCalledWith({
      settings: {
        "agent.connectivity_probe_success_ttl_seconds": "600",
        "agent.connectivity_probe_failure_retry_min_seconds": "45",
        "agent.connectivity_probe_failure_retry_max_seconds": "900",
      },
    });
  });

  it("rejects Scheduler timing values outside their configured ranges", () => {
    render(<AgentRelaySettings />);
    const successTTL = screen.getByRole("spinbutton", { name: "Success result TTL" });
    const retryMin = screen.getByRole("spinbutton", { name: "Minimum failure retry" });

    fireEvent.change(successTTL, { target: { value: "29" } });
    fireEvent.change(retryMin, { target: { value: "4" } });

    expect(successTTL).toHaveAttribute("aria-invalid", "true");
    expect(retryMin).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("button", { name: "Save relay settings" })).toBeDisabled();
    expect(mocks.mutateAsync).not.toHaveBeenCalled();
  });

  it("rejects a maximum retry below the minimum retry", () => {
    render(<AgentRelaySettings />);
    const retryMin = screen.getByRole("spinbutton", { name: "Minimum failure retry" });
    const retryMax = screen.getByRole("spinbutton", { name: "Maximum failure retry" });

    fireEvent.change(retryMin, { target: { value: "60" } });
    fireEvent.change(retryMax, { target: { value: "59" } });

    expect(retryMax).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByText("Maximum retry must be at least the minimum.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save relay settings" })).toBeDisabled();
  });

  it("renders a compact disabled loading state before the first authoritative response", () => {
    mocks.query.data = undefined as never;
    mocks.query.isPending = true;
    const { container } = render(<AgentRelaySettings />);

    expect(screen.getByRole("status", { name: "Loading relay settings" })).toBeInTheDocument();
    expect(container.querySelectorAll('[data-slot="skeleton"]')).toHaveLength(3);
    expect(screen.getByRole("button", { name: "Save relay settings" })).toBeDisabled();
    expect(screen.queryByRole("textbox", { name: "Default Relay URI" })).not.toBeInTheDocument();
  });

  it("shows an actionable initial error and retries without creating an empty baseline", async () => {
    const user = userEvent.setup();
    mocks.query.data = undefined as never;
    mocks.query.isError = true;
    render(<AgentRelaySettings />);

    expect(screen.getByRole("alert")).toHaveTextContent("Retry before changing relay settings");
    expect(screen.queryByRole("textbox", { name: "Default Relay URI" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save relay settings" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(mocks.query.refetch).toHaveBeenCalledOnce();
  });

  it("keeps cached values and a dirty draft through a background refetch error", async () => {
    const user = userEvent.setup();

    setSettings("wss://relay.example.com/known", "1");
    const { rerender } = render(<AgentRelaySettings />);
    const input = screen.getByRole("textbox", { name: "Default Relay URI" });
    expect(input).toHaveValue("wss://relay.example.com/known");
    await user.clear(input);
    await user.type(input, "wss://relay.example.com/draft");

    mocks.query.isError = true;
    rerender(<AgentRelaySettings />);

    expect(screen.getByRole("alert")).toHaveTextContent("Showing the last loaded values");
    expect(input).toHaveValue("wss://relay.example.com/draft");
    expect(screen.getByRole("button", { name: "Save relay settings" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(mocks.query.refetch).toHaveBeenCalledOnce();
  });

  it("saves a valid URI and switch atomically while omitting unchanged fields", async () => {
    const user = userEvent.setup();
    render(<AgentRelaySettings />);

    await user.type(
      screen.getByRole("textbox", { name: "Default Relay URI" }),
      " wss://relay-jp.example.com/ws/agent-relay ",
    );
    await user.click(screen.getByRole("switch", { name: "Relay fallback" }));
    await user.click(screen.getByRole("button", { name: "Save relay settings" }));

    expect(mocks.mutateAsync).toHaveBeenCalledWith({
      settings: {
        "agent.relay_default_uri": "wss://relay-jp.example.com/ws/agent-relay",
        "agent.relay_fallback_enabled": "1",
      },
    });
  });

  it("sends only a changed URI when fallback admission is unchanged", async () => {
    setSettings("wss://relay.example.com/old", "1");
    const user = userEvent.setup();
    render(<AgentRelaySettings />);

    expect(screen.queryByText("Derived from Master URL")).not.toBeInTheDocument();
    const input = screen.getByRole("textbox", { name: "Default Relay URI" });
    await user.clear(input);
    await user.type(input, "wss://relay.example.com/new");
    await user.click(screen.getByRole("button", { name: "Save relay settings" }));

    expect(mocks.mutateAsync).toHaveBeenCalledWith({
      settings: { "agent.relay_default_uri": "wss://relay.example.com/new" },
    });
  });

  it("accepts an empty URI but disables save and explains invalid non-empty values", async () => {
    const user = userEvent.setup();
    render(<AgentRelaySettings />);

    const input = screen.getByRole("textbox", { name: "Default Relay URI" });
    expect(input).toHaveAttribute("aria-invalid", "false");
    await user.type(input, "https://relay.example.com/#secret");

    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("alert")).toHaveTextContent("Use an absolute ws:// or wss:// URI");
    expect(screen.getByRole("button", { name: "Save relay settings" })).toBeDisabled();
    expect(mocks.mutateAsync).not.toHaveBeenCalled();
  });

  it("retains the draft and renders actionable server feedback after a failed save", async () => {
    mocks.mutateAsync.mockRejectedValueOnce(new Error("upstream validation unavailable"));
    const user = userEvent.setup();
    render(<AgentRelaySettings />);

    const input = screen.getByRole("textbox", { name: "Default Relay URI" });
    await user.type(input, "wss://relay.example.com/new");
    await user.click(screen.getByRole("button", { name: "Save relay settings" }));

    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Check the values and try again"));
    expect(input).toHaveValue("wss://relay.example.com/new");
    expect(screen.getByRole("button", { name: "Save relay settings" })).toBeEnabled();
  });

  it("treats the canonical 2048-byte boundary as valid and overflow as invalid", async () => {
    const prefix = "wss://relay.example/";
    const boundary = `${prefix}${"a".repeat(2048 - prefix.length - "%E7%95%8C".length)}界`;
    const overflow = `${prefix}${"a".repeat(2048 - prefix.length - new TextEncoder().encode("界").length)}界`;
    const { unmount } = render(<AgentRelaySettings />);

    const input = screen.getByRole("textbox", { name: "Default Relay URI" });
    fireEvent.change(input, { target: { value: boundary } });
    expect(input).toHaveAttribute("aria-invalid", "false");

    unmount();
    setSettings(overflow, "0");
    render(<AgentRelaySettings />);
    expect(screen.getByRole("textbox", { name: "Default Relay URI" })).toHaveAttribute("aria-invalid", "true");
  });
});
