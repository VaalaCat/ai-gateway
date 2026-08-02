import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ChannelAutoBanBadge } from "./channel-auto-ban-badge";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

vi.mock("@/lib/utils/format", () => ({
  formatDate: (timestamp: number) => `formatted:${timestamp}`,
}));

describe("ChannelAutoBanBadge", () => {
  it("shows a destructive consecutive-error badge and its localized trigger time", async () => {
    const user = userEvent.setup();
    render(
      <ChannelAutoBanBadge
        channel={{
          status: 0,
          auto_ban_state: {
            tripped: true,
            reason: "consecutive_errors",
            tripped_at: 1_723_456_789,
          },
        }}
      />,
    );

    const badge = screen.getByText("autoBanBadge").closest("[data-variant]");
    expect(badge).toHaveAttribute("data-variant", "destructive");

    await user.hover(badge!);
    expect((await screen.findAllByText("autoBanTrippedDescription")).length).toBeGreaterThan(0);
    expect(screen.getAllByText("autoBanTrippedAt formatted:1723456789").length).toBeGreaterThan(0);
  });

  it("does not show an error reason while the channel is enabled", () => {
    render(
      <ChannelAutoBanBadge
        channel={{ status: 1, auto_ban_state: { tripped: true, tripped_at: 1 } }}
      />,
    );

    expect(screen.queryByText("autoBanBadge")).not.toBeInTheDocument();
  });

  it("does not label a manual disable as a consecutive error", () => {
    render(<ChannelAutoBanBadge channel={{ status: 0 }} />);

    expect(screen.queryByText("autoBanBadge")).not.toBeInTheDocument();
  });

  it("does not show a cleared auto-disable state", () => {
    render(
      <ChannelAutoBanBadge
        channel={{ status: 0, auto_ban_state: { tripped: false, tripped_at: 1 } }}
      />,
    );

    expect(screen.queryByText("autoBanBadge")).not.toBeInTheDocument();
  });

  it("keeps the tooltip useful when the historic trigger time is absent", async () => {
    const user = userEvent.setup();
    render(
      <ChannelAutoBanBadge channel={{ status: 0, auto_ban_state: { tripped: true } }} />,
    );

    await user.hover(screen.getByText("autoBanBadge"));
    expect((await screen.findAllByText("autoBanTrippedDescription")).length).toBeGreaterThan(0);
    expect(screen.queryByText(/autoBanTrippedAt/)).not.toBeInTheDocument();
  });

  it("opens its explanation from the keyboard", async () => {
    const user = userEvent.setup();
    render(
      <ChannelAutoBanBadge channel={{ status: 0, auto_ban_state: { tripped: true } }} />,
    );

    await user.tab();
    expect(screen.getByText("autoBanBadge")).toHaveFocus();
    expect((await screen.findAllByText("autoBanTrippedDescription")).length).toBeGreaterThan(0);
  });
});
