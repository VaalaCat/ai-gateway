import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { Channel } from "@/lib/types";
import { ChannelLimitBadge } from "./channel-limit-badge";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

function channel(state: Pick<Channel, "status" | "limit_state" | "auto_ban_state">): Channel {
  return state as Channel;
}

describe("ChannelLimitBadge", () => {
  it("does not render a disable reason for an enabled channel", () => {
    render(<ChannelLimitBadge channel={channel({ status: 1 })} />);

    expect(screen.queryByText(/limitBadge/)).not.toBeInTheDocument();
  });

  it("keeps the manual reason for a disabled channel without owned runtime state", () => {
    render(<ChannelLimitBadge channel={channel({ status: 0 })} />);

    expect(screen.getByText("limitBadgeManual")).toBeInTheDocument();
  });

  it("does not mislabel an error-auto-disabled channel as manual", () => {
    render(
      <ChannelLimitBadge
        channel={channel({ status: 0, auto_ban_state: { tripped: true } })}
      />,
    );

    expect(screen.queryByText("limitBadgeManual")).not.toBeInTheDocument();
    expect(screen.queryByText("limitBadgeAuto")).not.toBeInTheDocument();
  });

  it("keeps the limit reason when both independent disable states are tripped", () => {
    render(
      <ChannelLimitBadge
        channel={channel({
          status: 0,
          limit_state: { tripped: true, reason: "calls/daily" },
          auto_ban_state: { tripped: true },
        })}
      />,
    );

    expect(screen.getByText("limitBadgeAuto")).toBeInTheDocument();
    expect(screen.queryByText("limitBadgeManual")).not.toBeInTheDocument();
  });
});
