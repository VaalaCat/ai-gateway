import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import en from "@/i18n/en.json";
import zh from "@/i18n/zh.json";
import { ModelName } from "@/components/business/model-name";
import { ModelProviderLogo } from "@/components/business/model-provider-logo";
import type { MarketplaceModelOffer } from "@/lib/api/model-marketplace";
import {
  ChannelAvatarGroup,
  channelInitials,
} from "./channel-avatar-group";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, string | number>) =>
    values ? `${key} ${Object.values(values).join(" ")}` : key,
}));

const mocks = {
  isMobile: false,
};

vi.mock("@/hooks/use-mobile", () => ({
  useIsMobile: () => mocks.isMobile,
}));

vi.mock("@/components/business/provider-avatar", () => ({
  ProviderAvatar: ({ provider, size }: { provider: string; size: number }) => (
    <svg aria-label={`${provider}-${size}`} />
  ),
}));

function offer(
  displayName: string,
  available = true,
  endpoints: MarketplaceModelOffer["supported_endpoints"] = ["responses"],
): MarketplaceModelOffer {
  return {
    offer_ref: displayName.toLowerCase().replaceAll(" ", "-"),
    kind: "platform",
    display_name: displayName,
    ownership: "platform",
    available,
    supported_endpoints: endpoints,
    pricing: {
      reference_price: { input: 1, output: 2, cache_read: 0, cache_write: 0 },
      gateway_charge: { input: 1, output: 2, cache_read: 0, cache_write: 0 },
      estimated_total: { input: 1, output: 2, cache_read: 0, cache_write: 0 },
      accuracy: "exact",
    },
    performance_status: "available",
    performance: {
      status: "operational",
      success_rate: 99,
      ttft_avg_ms: null,
      ttft_p95_ms: null,
      tps_avg: null,
      tps_p5: null,
      duration_p95_ms: null,
      token_units: { input: 0, output: 0, cache_read: 0, cache_write: 0, total: 0 },
    },
    status_history: [],
    trend_series: [],
    usage_references: [],
  };
}

const lookup = (messages: typeof en, key: string) => key.split(".").reduce<unknown>(
  (value, segment) => typeof value === "object" && value !== null
    ? (value as Record<string, unknown>)[segment]
    : undefined,
  messages.modelMarketplace,
);

describe("marketplace channel avatar group", () => {
  beforeEach(() => {
    mocks.isMobile = false;
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it.each([
    ["OpenAI Direct", "OD"],
    ["ZenMux", "ZE"],
    ["硅基流动", "硅"],
    ["  ", "?"],
  ])("derives stable initials for %j", (displayName, expected) => {
    expect(channelInitials(displayName)).toBe(expected);
  });

  it("falls back to code points when Intl.Segmenter is unavailable", () => {
    vi.stubGlobal("Intl", { ...Intl, Segmenter: undefined });

    expect(channelInitials("🤖AI")).toBe("🤖A");
  });

  it("sorts available channels first and exposes only their public identity", () => {
    const unavailable = {
      ...offer("Internal Relay", false),
      channel_id: 71,
      base_url: "https://internal.example.test",
      internal_name: "channel_id base_url internal",
    };
    const offers = [unavailable, offer("OpenAI Direct"), offer("ZenMux")];

    render(<ChannelAvatarGroup offers={offers} desktopLimit={2} mobileLimit={2} />);

    const avatars = [
      screen.getByLabelText("OpenAI Direct"),
      screen.getByLabelText("ZenMux"),
    ];
    expect(avatars.map((avatar) => avatar.getAttribute("aria-label"))).toEqual([
      "OpenAI Direct",
      "ZenMux",
    ]);
    expect(screen.getByText("+1")).toBeInTheDocument();
    expect(screen.queryByText(/internal|channel_id|base_url/i)).not.toBeInTheDocument();
  });

  it("keeps unavailable offer order stable when every channel is unavailable", () => {
    render(
      <ChannelAvatarGroup
        offers={[offer("First Down", false), offer("Second Down", false)]}
        desktopLimit={2}
        mobileLimit={2}
      />,
    );

    expect([screen.getByLabelText("First Down"), screen.getByLabelText("Second Down")]
      .map((avatar) => avatar.getAttribute("aria-label"))).toEqual([
      "First Down",
      "Second Down",
    ]);
  });

  it("renders no avatar group for an empty offer list", () => {
    render(<ChannelAvatarGroup offers={[]} />);

    expect(screen.queryByLabelText("channelGroupLabel")).not.toBeInTheDocument();
  });

  it.each([1, 2, 3, 4, 5])("renders %i desktop offers without a trailing group control", (count) => {
    render(<ChannelAvatarGroup offers={Array.from({ length: count }, (_, index) => offer(`Channel ${index + 1}`))} />);

    expect(screen.getAllByTestId("channel-avatar")).toHaveLength(count);
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(document.querySelector("[data-slot='avatar-group-count']")).toBeNull();
    expect(document.querySelector("[data-lucide='list']")).toBeNull();
  });

  it("uses five default 32-pixel avatars and reports the exact desktop overflow", () => {
    render(<ChannelAvatarGroup offers={Array.from({ length: 6 }, (_, index) => offer(`Channel ${index + 1}`))} />);

    expect(screen.getByLabelText("Channel 5")).toBeInTheDocument();
    expect(screen.queryByLabelText("Channel 6")).not.toBeInTheDocument();
    const trigger = screen.getByRole("button", { name: "showAllChannels 6" });
    expect(trigger).toHaveTextContent("+1");
    expect(trigger).toHaveAttribute("data-slot", "avatar-group-count");
    expect(screen.getByLabelText("Channel 1")).toHaveAttribute("data-size", "default");
    expect(trigger).toHaveClass("size-8", "ring-2", "ring-background");
  });

  it("uses three mobile avatars and one overflow trigger", () => {
    mocks.isMobile = true;
    render(<ChannelAvatarGroup offers={Array.from({ length: 4 }, (_, index) => offer(`Channel ${index + 1}`))} />);

    expect(screen.getByLabelText("Channel 3")).toBeInTheDocument();
    expect(screen.queryByLabelText("Channel 4")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "showAllChannels 4" })).toHaveTextContent("+1");
  });

  it.each([0, -1])("turns a %i explicit limit into one overflow trigger without crashing", (limit) => {
    render(<ChannelAvatarGroup
      offers={[offer("First"), offer("Second")]}
      desktopLimit={limit}
      mobileLimit={limit}
    />);

    expect(screen.queryByTestId("channel-avatar")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "showAllChannels 2" })).toHaveTextContent("+2");
  });

  it("shows a focusable tooltip and all six public offers in the overflow popover", async () => {
    const user = userEvent.setup();
    const offers = Array.from({ length: 6 }, (_, index) => offer(`Channel ${index + 1}`));
    offers[0] = { ...offers[0], supported_endpoints: null as never };
    Object.assign(offers[5] as MarketplaceModelOffer & {
      channel_id: number;
      base_url: string;
      internal_name: string;
    }, {
      channel_id: 99,
      base_url: "https://internal.example.test",
      internal_name: "internal channel_id base_url",
    });

    render(<ChannelAvatarGroup offers={offers} desktopLimit={2} mobileLimit={2} />);

    const firstAvatar = screen.getByLabelText("Channel 1");
    expect(firstAvatar).toHaveAttribute("tabindex", "0");
    await user.hover(firstAvatar);
    const tooltip = await screen.findByRole("tooltip");
    expect(tooltip).toHaveTextContent("Channel 1");
    const title = within(tooltip).getByText("Channel 1");
    const details = title.parentElement?.querySelector("dl");
    expect(details).toBeInTheDocument();
    expect(title).not.toHaveClass("text-popover-foreground", "text-muted-foreground");
    expect(details).not.toHaveClass("text-popover-foreground", "text-muted-foreground");
    expect(details).toHaveClass("text-current", "opacity-75");

    await user.click(screen.getByRole("button", { name: "showAllChannels 6" }));
    const dialog = await screen.findByRole("dialog", { name: "allChannelsDialogLabel 6" });
    expect(dialog).toHaveTextContent("Channel 1");
    expect(dialog).toHaveTextContent("Channel 6");
    expect(dialog).not.toHaveTextContent(/internal|channel_id|base_url/i);
  });

  it("uses one same-sized avatar count trigger and semantic availability badges", () => {
    render(
      <ChannelAvatarGroup
        offers={Array.from({ length: 6 }, (_, index) => offer(
          index === 0 ? "OpenAI Direct" : `Channel ${index + 1}`,
          index !== 5,
        ))}
        desktopLimit={5}
        mobileLimit={5}
      />,
    );

    const trigger = screen.getByRole("button", { name: "showAllChannels 6" });
    expect(trigger).toHaveAttribute("data-slot", "avatar-group-count");
    expect(trigger.querySelector("[data-slot='button']")).toBeNull();
    expect(screen.getByLabelText("OpenAI Direct")
      .querySelector("[data-slot='avatar-badge']")).toHaveClass("bg-emerald-500");
  });

  it("keeps avatar badge space outside image and fallback clipping", () => {
    const fallback = render(<Avatar><AvatarFallback>CH</AvatarFallback></Avatar>);
    expect(fallback.container.querySelector("[data-slot='avatar-fallback']"))
      .toHaveClass("overflow-hidden", "rounded-full");
    fallback.unmount();

    vi.stubGlobal("Image", class {
      private value = "";
      complete = true;
      naturalWidth = 1;
      get src() { return this.value; }
      set src(value: string) { this.value = value; }
      addEventListener() {}
      removeEventListener() {}
    });
    const { container } = render(
      <Avatar>
        <AvatarImage src="/channel.png" alt="Channel" />
      </Avatar>,
    );

    const avatar = container.querySelector("[data-slot='avatar']");
    expect(avatar).not.toHaveClass("overflow-hidden");
    expect(container.querySelector("[data-slot='avatar-image']"))
      .toHaveClass("overflow-hidden", "rounded-full");
  });

  it("presents private offers as BYOK instead of looking up a nonexistent private kind", async () => {
    const user = userEvent.setup();
    render(
      <ChannelAvatarGroup
        offers={[{ ...offer("Private Channel"), kind: "private" }]}
        desktopLimit={1}
        mobileLimit={1}
      />,
    );

    await user.hover(screen.getByLabelText("Private Channel"));

    const tooltip = await screen.findByRole("tooltip");
    expect(tooltip).toHaveTextContent("kind.byok");
    expect(tooltip).not.toHaveTextContent("kind.private");
  });
});

describe("shared model provider logo", () => {
  it("renders a recognized logo and omits an unrecognized one", () => {
    const { container, rerender } = render(<ModelProviderLogo modelName="gpt-4o" />);
    expect(screen.getByLabelText("OpenAI-14")).toBeInTheDocument();

    rerender(<ModelProviderLogo modelName="unrecognized-model" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("preserves ModelName text for recognized and unrecognized models", () => {
    const { rerender } = render(<ModelName name="gpt-4o" />);
    expect(screen.getByText("gpt-4o")).toBeInTheDocument();

    rerender(<ModelName name="unrecognized-model" />);
    expect(screen.getByText("unrecognized-model")).toBeInTheDocument();
  });
});

describe("marketplace channel avatar translations", () => {
  it("contains every new channel-avatar key in both locales", () => {
    const keys = [
      "channelGroupLabel",
      "showAllChannels",
      "channelKindLabel",
      "channelAvailabilityLabel",
      "channelAvailable",
      "channelUnavailable",
      "channelEndpointsLabel",
      "channelNoEndpoints",
      "allChannelsDialogLabel",
      "kind.byok",
    ];

    for (const key of keys) {
      const englishValue = lookup(en, key);
      const chineseValue = lookup(zh as typeof en, key);
      expect(typeof englishValue === "string" && englishValue.length > 0, `en:${key}`).toBe(true);
      expect(typeof chineseValue === "string" && chineseValue.length > 0, `zh:${key}`).toBe(true);
    }
  });
});
