import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { copyTextWithFeedback } from "@/lib/utils/clipboard";
import { AgentURI } from "./agent-uri";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => ({
    copyUri: "Copy URI",
    copied: "Copied",
    copyFailed: "Copy failed",
    relayUri: "Relay URI",
  } as Record<string, string>)[key] ?? key,
}));

vi.mock("@/lib/utils/clipboard", () => ({
  copyTextWithFeedback: vi.fn(),
}));

describe("AgentURI", () => {
  beforeEach(() => {
    vi.mocked(copyTextWithFeedback).mockReset().mockResolvedValue(true);
  });

  const renderURI = (uri: string) => render(<AgentURI uri={uri} />);

  it("truncates one complete long URI and exposes both full-value copy actions", async () => {
    const user = userEvent.setup();
    const uri = "wss://edge.example.com/a/very/long/relay/path?region=ap-southeast-1";
    const { container } = renderURI(uri);

    expect(container.firstChild).toHaveClass("flex", "min-w-0", "max-w-full", "gap-1.5");
    expect(container.querySelector("[data-slot=uri-prefix]")).not.toBeInTheDocument();
    expect(container.querySelector("[data-slot=uri-suffix]")).not.toBeInTheDocument();
    const trigger = screen.getByRole("button", { name: uri });
    expect(trigger).toHaveClass("min-w-0", "flex-1", "shrink", "truncate", "text-left");
    expect(trigger).not.toHaveClass("shrink-0");
    const triggerValue = screen.getByText(uri, { selector: "button > span" });
    expect(triggerValue).toHaveClass("block", "min-w-0", "flex-1", "truncate", "text-left");
    expect(screen.getAllByText(uri)).toHaveLength(1);

    await user.click(trigger);

    expect(await screen.findAllByText(uri)).toHaveLength(2);
    const dialog = screen.getByRole("dialog", { name: "Relay URI" });
    expect(dialog).toHaveClass(
      "max-h-(--radix-popover-content-available-height)",
      "overflow-y-auto",
      "w-[min(22rem,calc(100vw-2rem))]",
    );
    const fullValue = screen.getByText(uri, { selector: "code" });
    expect(fullValue).toHaveClass("min-w-0", "flex-1", "select-all", "break-all", "font-mono", "text-xs");
    const copyButtons = screen.getAllByRole("button", { name: "Copy URI" });
    expect(copyButtons).toHaveLength(2);
    for (const button of copyButtons) {
      expect(button.querySelector("svg")).toHaveAttribute("data-icon", "inline-start");
    }
    await user.click(copyButtons[1]);
    expect(copyTextWithFeedback).toHaveBeenCalledWith(uri, {
      success: "Copied",
      error: "Copy failed",
    });
    expect(screen.getByRole("dialog", { name: "Relay URI" })).toBeInTheDocument();

    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(screen.queryByRole("dialog", { name: "Relay URI" })).not.toBeInTheDocument();
    });
    expect(trigger).toHaveFocus();
  });

  it("shows a short URI unchanged and passes its full value when copy feedback fails", async () => {
    const user = userEvent.setup();
    const uri = "wss://relay.local/ws";
    vi.mocked(copyTextWithFeedback).mockResolvedValueOnce(false);
    renderURI(uri);

    expect(screen.getByRole("button", { name: uri })).toHaveTextContent(uri);
    await user.click(screen.getByRole("button", { name: "Copy URI" }));

    expect(copyTextWithFeedback).toHaveBeenCalledWith(uri, {
      success: "Copied",
      error: "Copy failed",
    });
  });

  it("shows a placeholder without actions for an empty URI", () => {
    renderURI("");

    expect(screen.getByText("-")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});
