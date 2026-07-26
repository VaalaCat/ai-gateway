import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { TooltipProvider } from "@/components/ui/tooltip";
import { BackgroundRefreshStatus } from "./background-refresh-status";

function renderStatus(refreshing: boolean) {
  return render(
    <TooltipProvider>
      <BackgroundRefreshStatus
        refreshing={refreshing}
        label="Refreshing connections"
        testId="background-refresh-status"
      />
    </TooltipProvider>,
  );
}

describe("BackgroundRefreshStatus", () => {
  it("keeps an idle fixed-size slot while hiding the spinner", () => {
    renderStatus(false);

    const slot = screen.getByTestId("background-refresh-status");
    const icon = slot.querySelector("svg");

    expect(slot).toHaveClass("inline-flex", "size-8", "shrink-0", "items-center", "justify-center");
    expect(slot).not.toHaveAttribute("role");
    expect(slot).not.toHaveAttribute("aria-label");
    expect(icon).toHaveClass("size-4");
    expect(icon).toHaveAttribute("aria-hidden", "true");
    expect(icon).toHaveClass("invisible");
    expect(icon).not.toHaveClass("animate-spin");
  });

  it("preserves the slot node and exposes refresh status when it starts", () => {
    const view = renderStatus(false);
    const slot = screen.getByTestId("background-refresh-status");

    view.rerender(
      <TooltipProvider>
        <BackgroundRefreshStatus
          refreshing
          label="Refreshing connections"
          testId="background-refresh-status"
        />
      </TooltipProvider>,
    );

    const refreshedSlot = screen.getByTestId("background-refresh-status");
    const icon = refreshedSlot.querySelector("svg");

    expect(refreshedSlot).toBe(slot);
    expect(refreshedSlot).toHaveAttribute("role", "status");
    expect(refreshedSlot).toHaveAttribute("aria-label", "Refreshing connections");
    expect(icon).toHaveClass("size-4");
    expect(icon).toHaveAttribute("aria-hidden", "true");
    expect(icon).toHaveClass("visible", "animate-spin");
  });

  it("preserves the slot node and hides the spinner when refresh completes", () => {
    const view = renderStatus(true);
    const slot = screen.getByTestId("background-refresh-status");

    view.rerender(
      <TooltipProvider>
        <BackgroundRefreshStatus
          refreshing={false}
          label="Refreshing connections"
          testId="background-refresh-status"
        />
      </TooltipProvider>,
    );

    const idleSlot = screen.getByTestId("background-refresh-status");
    const icon = idleSlot.querySelector("svg");

    expect(idleSlot).toBe(slot);
    expect(idleSlot).not.toHaveAttribute("role");
    expect(idleSlot).not.toHaveAttribute("aria-label");
    expect(icon).toHaveClass("size-4");
    expect(icon).toHaveAttribute("aria-hidden", "true");
    expect(icon).toHaveClass("invisible");
    expect(icon).not.toHaveClass("animate-spin");
  });

  it("shows refresh help only while the status is active", async () => {
    const user = userEvent.setup();
    const active = renderStatus(true);

    await user.hover(screen.getByTestId("background-refresh-status"));
    expect(await screen.findByRole("tooltip")).toHaveTextContent("Refreshing connections");

    active.unmount();
    renderStatus(false);
    await user.hover(screen.getByTestId("background-refresh-status"));
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
  });
});
