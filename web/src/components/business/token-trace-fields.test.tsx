import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { TokenTraceBadge, TokenTraceFields } from "./token-trace-fields";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));

describe("TokenTraceFields", () => {
  it("associates each label with its own switch when rendered twice", () => {
    render(
      <>
        <TokenTraceFields
          enabled={false}
          mode="full"
          onEnabledChange={vi.fn()}
          onModeChange={vi.fn()}
        />
        <TokenTraceFields
          enabled={false}
          mode="headers"
          onEnabledChange={vi.fn()}
          onModeChange={vi.fn()}
        />
      </>,
    );

    const labels = screen.getAllByText("traceEnabled");
    const switches = screen.getAllByRole("switch", { name: "traceEnabled" });
    expect(labels[0]).toHaveAttribute("for", switches[0].id);
    expect(labels[1]).toHaveAttribute("for", switches[1].id);
    expect(switches[0].id).not.toBe(switches[1].id);
  });

  it("selects full as the default mode", () => {
    render(
      <TokenTraceFields
        enabled
        mode="full"
        onEnabledChange={vi.fn()}
        onModeChange={vi.fn()}
      />,
    );

    expect(screen.getByRole("radio", { name: "traceModeFull" })).toHaveAttribute(
      "data-state",
      "on",
    );
  });

  it("selects headers without changing the enabled switch", async () => {
    const onModeChange = vi.fn();
    const onEnabledChange = vi.fn();
    render(
      <TokenTraceFields
        enabled
        mode="full"
        onEnabledChange={onEnabledChange}
        onModeChange={onModeChange}
      />,
    );

    await userEvent.click(screen.getByRole("radio", { name: "traceModeHeaders" }));

    expect(onModeChange).toHaveBeenCalledWith("headers");
    expect(onEnabledChange).not.toHaveBeenCalled();
  });

  it("hides the mode group while disabled without overwriting mode", async () => {
    const onEnabledChange = vi.fn();
    const onModeChange = vi.fn();
    render(
      <TokenTraceFields
        enabled={false}
        mode="headers"
        onEnabledChange={onEnabledChange}
        onModeChange={onModeChange}
      />,
    );

    expect(screen.queryByRole("group", { name: "traceContent" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("switch", { name: "traceEnabled" }));

    expect(onEnabledChange).toHaveBeenCalledWith(true);
    expect(onModeChange).not.toHaveBeenCalled();
  });

  it("keeps the current single selection when clicked again", async () => {
    const onModeChange = vi.fn();
    render(
      <TokenTraceFields
        enabled
        mode="headers"
        onEnabledChange={vi.fn()}
        onModeChange={onModeChange}
      />,
    );

    await userEvent.click(screen.getByRole("radio", { name: "traceModeHeaders" }));

    expect(onModeChange).not.toHaveBeenCalled();
    expect(screen.getByRole("radio", { name: "traceModeHeaders" })).toHaveAttribute(
      "data-state",
      "on",
    );
  });
});

describe("TokenTraceBadge", () => {
  it.each([
    { enabled: false, mode: "headers" as const, label: "traceDisabled" },
    { enabled: true, mode: "full" as const, label: "traceModeFull" },
    { enabled: true, mode: undefined, label: "traceModeFull" },
    { enabled: true, mode: "headers" as const, label: "traceModeHeaders" },
  ])("renders $label", ({ enabled, mode, label }) => {
    render(<TokenTraceBadge enabled={enabled} mode={mode} />);

    expect(screen.getByText(label)).toBeInTheDocument();
  });
});
