import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { UsageSection } from "./usage";

const useChannelModelBreakdown = vi.fn();
const breakdownProps = vi.fn();

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/api/billing", () => ({
  useChannelModelBreakdown: (...args: unknown[]) => useChannelModelBreakdown(...args),
}));
vi.mock("@/components/business/channel-model-breakdown", () => ({
  ChannelModelBreakdown: (props: unknown) => {
    breakdownProps(props);
    return <div data-testid="breakdown" />;
  },
}));

describe("UsageSection", () => {
  beforeEach(() => {
    vi.spyOn(Date, "now").mockReturnValue(1_800_000_000_000);
    useChannelModelBreakdown.mockReset();
    useChannelModelBreakdown.mockReturnValue({ data: { rows: [] } });
    breakdownProps.mockReset();
  });

  it("queries and renders the default 30-day window", () => {
    render(<UsageSection channelId={7} />);

    expect(useChannelModelBreakdown).toHaveBeenLastCalledWith(7, 1_797_408_000, 1_800_000_000);
    expect(breakdownProps).toHaveBeenLastCalledWith({
      channelId: 7,
      start: 1_797_408_000,
      end: 1_800_000_000,
    });
  });

  it("switches to seven days without changing the window end", async () => {
    const user = userEvent.setup();
    render(<UsageSection channelId={7} />);
    vi.mocked(Date.now).mockReturnValue(1_900_000_000_000);

    await user.click(screen.getByRole("tab", { name: "usageWindow7d" }));

    expect(useChannelModelBreakdown).toHaveBeenLastCalledWith(7, 1_799_395_200, 1_800_000_000);
  });

  it("shows the saved-channel placeholder when channelId is absent", () => {
    render(<UsageSection />);

    expect(screen.getByText("usageSavedPlaceholder")).toBeInTheDocument();
    expect(screen.queryByTestId("breakdown")).not.toBeInTheDocument();
    expect(useChannelModelBreakdown).toHaveBeenCalledWith(
      undefined,
      1_797_408_000,
      1_800_000_000,
    );
  });
});
