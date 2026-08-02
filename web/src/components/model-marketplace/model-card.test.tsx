import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import en from "@/i18n/en.json";
import zh from "@/i18n/zh.json";
import type {
  MarketplaceHealthStatus,
  MarketplaceModel,
  MarketplaceModelOffer,
} from "@/lib/api/model-marketplace";
import { ModelCard } from "./model-card";

vi.mock("next/link", () => ({
  default: ({ href, children, ...props }: React.ComponentProps<"a">) => (
    <a href={href} {...props}>{children}</a>
  ),
}));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, string | number>) =>
    values ? `${key} ${Object.values(values).join(" ")}` : key,
}));

vi.mock("@/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

vi.mock("@/components/business/provider-avatar", () => ({
  ProviderAvatar: ({ provider, size }: { provider: string; size: number }) => (
    <svg aria-label={`${provider}-${size}`} />
  ),
}));

function offer(
  displayName: string,
  supportedEndpoints: MarketplaceModelOffer["supported_endpoints"],
  available = true,
  gatewayCharge: Partial<MarketplaceModelOffer["pricing"]["gateway_charge"]> = {},
): MarketplaceModelOffer {
  const prices = { input: 1, output: 2, cache_read: 0, cache_write: 0, ...gatewayCharge };
  return {
    offer_ref: displayName,
    kind: "platform",
    display_name: displayName,
    ownership: "platform",
    available,
    supported_endpoints: supportedEndpoints,
    pricing: {
      reference_price: { input: 1, output: 2, cache_read: 0, cache_write: 0 },
      gateway_charge: prices,
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

function realModel(
  offers: MarketplaceModelOffer[],
  aggregateStatus: MarketplaceHealthStatus = "operational",
  description = "",
): Extract<MarketplaceModel, { kind: "real" }> {
  return {
    kind: "real",
    real: {
      model_name: "model-a",
      metadata: {
        display_name: "Model A",
        description,
        provider: "Provider",
        input_modalities: ["text"],
        output_modalities: ["text"],
        context_length: 1,
        max_output_tokens: 1,
        supported_parameters: [],
        tool_calling: false,
        structured_output: false,
        reasoning: false,
        prompt_cache: false,
      },
      aggregate_status: aggregateStatus,
      available_offer_count: 99,
      platform_offer_count: 99,
      private_offer_count: 0,
      offers,
      performance: {
        performance_status: "available",
        window: "24h",
        success_rate: 99.42,
        cache_hit_rate: 78.3,
        status_history: Array.from({ length: 24 }, (_, index) => ({
          started_at: 1_752_494_400 + index * 3_600,
          ended_at: 1_752_498_000 + index * 3_600,
          status: index === 1 ? "degraded" : "operational",
          in_progress: index === 23,
          success_rate: index === 1 ? 97.5 : 99.42,
        })),
      },
    },
  };
}

function routingModel(warnings: string[], reachable: string[] = []): MarketplaceModel {
  return {
    kind: "routing",
    routing: {
      model_name: "route-a",
      display_name: "Route A",
      reachable_real_models: reachable,
      flattened_destinations: [],
      routing_warnings: warnings,
      guidance: "view_reachable_real_models",
    },
  };
}

describe("real model marketplace card", () => {
  it("links the selected Token catalog result to its 24-hour detail", () => {
    render(<ModelCard model={realModel([offer("Offer", ["responses"])])} detailTokenId={17} />);

    const row = screen.getByRole("article", { name: "Model A" });
    const rowLink = within(row).getByRole("link", { name: "Model A" });
    expect(rowLink).toHaveAttribute("data-testid", "marketplace-row-link");
    expect(rowLink).toHaveClass("after:absolute", "after:inset-0");
    expect(rowLink).toHaveAttribute(
      "href",
      "/model-marketplace/detail?model=model-a&token_id=17&window=24h",
    );
    expect(row.querySelector("a a")).not.toBeInTheDocument();
  });

  it("links an administrator global catalog result without inventing a Token", () => {
    render(<ModelCard model={realModel([offer("Offer", ["responses"])])} />);

    expect(screen.getByRole("link", { name: "Model A" })).toHaveAttribute(
      "href",
      "/model-marketplace/detail?model=model-a&window=24h",
    );
  });

  it.each([
    { name: "short", description: "Short description", rendered: true },
    {
      name: "long",
      description: "A very long model description that must stay on one dense catalog row without adding a second line",
      rendered: true,
    },
    { name: "empty", description: "", rendered: false },
  ])("keeps the $name description on the one-line row contract", ({ description, rendered }) => {
    render(<ModelCard model={realModel([offer("Offer", ["responses"])], "operational", description)} />);

    const identity = screen.getByTestId("marketplace-model-identity");
    if (!rendered) {
      expect(identity.querySelectorAll("p")).toHaveLength(1);
      return;
    }
    const descriptionNode = within(identity).getByText(description);
    expect(descriptionNode).toHaveClass("truncate", "leading-4");
    expect(descriptionNode).not.toHaveClass("line-clamp-2");
  });

  it("renders one dense row with model performance, prices, endpoints, and channel identities", () => {
    render(<ModelCard model={realModel([
      offer("Platform", ["chat_completions", "responses"], true, {
        input: 1,
        cache_read: 0.25,
        output: 8,
        cache_write: 2,
      }),
      offer("BYOK", ["responses"], true, {
        input: 2,
        cache_read: 0.5,
        output: 10,
        cache_write: 4,
      }),
    ])} />);

    const row = screen.getByRole("article", { name: "Model A" });
    expect(within(row).getByTestId("marketplace-model-identity")).toBeInTheDocument();
    expect(within(row).getByLabelText("channelGroupLabel")).toBeInTheDocument();
    expect(within(row).queryByText(/platformOffers|privateOffers/)).not.toBeInTheDocument();
    const strip = within(row).getByTestId("model-performance-strip");
    expect(strip).toHaveTextContent("performanceWindow24h");
    expect(strip).toHaveTextContent("99.42%");
    expect(strip).toHaveTextContent("78.30%");
    expect(strip).toHaveClass("relative", "z-10", "tabular-nums");
    expect(within(strip).getAllByRole("img")).toHaveLength(24);
    expect(within(row).queryByTestId("offer-availability-tracker")).not.toBeInTheDocument();
    expect(within(row).queryByText("status.operational")).not.toBeInTheDocument();
    expect(within(row).getAllByTestId("marketplace-price-item").map((item) =>
      item.getAttribute("data-price-key"))).toEqual([
      "input",
      "cache_read",
      "output",
      "cache_write",
    ]);
  });

  it("keeps unavailable offers in channel identity while excluding them from prices", () => {
    render(<ModelCard model={realModel([
      offer("Available One", ["responses"], true, { input: 1 }),
      offer("Unavailable Secret", ["chat_completions"], false, { input: 999 }),
      offer("Available Two", ["messages"], true, { input: 2 }),
    ])} />);

    const row = screen.getByRole("article", { name: "Model A" });
    const inputPrice = within(row).getAllByTestId("marketplace-price-item")[0];
    expect(inputPrice).toHaveTextContent("$1.00 – $2.00");
    expect(inputPrice).not.toHaveTextContent("$999.00");

    // behavior change: unavailable offers remain visible as channel identities.
    const unavailableAvatar = within(row).getByLabelText("Unavailable Secret");
    expect(unavailableAvatar.querySelector("[data-slot='avatar-badge']"))
      .toHaveClass("bg-red-500");
    expect(within(row).queryByTestId("offer-availability-tracker")).not.toBeInTheDocument();
  });

  it("keeps an empty performance history stable and leaves null metrics unavailable", () => {
    const model = realModel([offer("Offer", ["responses"])]);
    model.real.performance = {
      performance_status: "unavailable",
      window: "24h",
      success_rate: null,
      cache_hit_rate: null,
      status_history: [],
    };
    render(<ModelCard model={model} />);

    const strip = screen.getByTestId("model-performance-strip");
    expect(strip).toHaveTextContent("—");
    expect(within(strip).queryAllByRole("img")).toHaveLength(0);
  });

  it("keeps hourly status color, UTC observation, and collection state on the model strip", async () => {
    const user = userEvent.setup();
    render(<ModelCard model={realModel([offer("Offer", ["responses"])])} />);

    const strip = screen.getByTestId("model-performance-strip");
    const blocks = within(strip).getAllByRole("img");
    expect(blocks[0]).toHaveClass("bg-emerald-600");
    expect(blocks[1]).toHaveClass("bg-amber-500");
    expect(blocks.at(-1)).toHaveClass("ring-amber-500/80");

    await user.hover(blocks.at(-1)!);
    const tooltip = await screen.findByRole("tooltip");
    expect(tooltip).toHaveTextContent("UTC");
    expect(tooltip).toHaveTextContent("modelSlaBucketTooltip 99.42");
    expect(tooltip).toHaveTextContent("detail.statusState.in_progress");
  });

  it("keeps stale summary values but presents every hourly block and explanation as stale", async () => {
    const user = userEvent.setup();
    const model = realModel([offer("Offer", ["responses"])]);
    model.real.performance.performance_status = "stale";
    render(<ModelCard model={model} />);

    const strip = screen.getByTestId("model-performance-strip");
    expect(strip).toHaveTextContent("99.42%");
    expect(strip).toHaveTextContent("78.30%");
    const blocks = within(strip).getAllByRole("img");
    expect(blocks).toHaveLength(24);
    for (const block of blocks) {
      expect(block).toHaveClass("bg-gray-400");
      expect(block).toHaveAccessibleName(/detail\.statusState\.stale/);
      expect(block).not.toHaveClass("bg-emerald-600", "bg-amber-500", "bg-red-600");
    }

    await user.hover(blocks[0]);
    expect(await screen.findByRole("tooltip")).toHaveTextContent("detail.statusState.stale");

    await user.unhover(blocks[0]);
    const slaTrigger = screen.getByLabelText(
      "modelSlaSummaryTooltip performanceWindow24h 99.42% · detail.statusState.stale",
    );
    await user.hover(slaTrigger);
    expect(await screen.findByRole("tooltip")).toHaveTextContent("detail.statusState.stale");

    await user.unhover(slaTrigger);
    const cacheTrigger = screen.getByLabelText(
      "modelCacheHitRateSummaryTooltip performanceWindow24h 78.30% · detail.statusState.stale",
    );
    await user.hover(cacheTrigger);
    expect(await screen.findByRole("tooltip")).toHaveTextContent("detail.statusState.stale");
  });

  it("makes SLA and cache metric explanations reachable with the keyboard", async () => {
    const user = userEvent.setup();
    render(<ModelCard model={realModel([offer("Offer", ["responses"])])} />);

    await user.tab();
    await user.tab();
    const slaTrigger = screen.getByLabelText(
      "modelSlaSummaryTooltip performanceWindow24h 99.42%",
    );
    expect(slaTrigger).toHaveFocus();
    expect(await screen.findByRole("tooltip")).toHaveTextContent(
      "modelSlaSummaryTooltip performanceWindow24h 99.42%",
    );

    await user.tab();
    const cacheTrigger = screen.getByLabelText(
      "modelCacheHitRateSummaryTooltip performanceWindow24h 78.30%",
    );
    expect(cacheTrigger).toHaveFocus();
    expect(await screen.findByRole("tooltip")).toHaveTextContent(
      "modelCacheHitRateSummaryTooltip performanceWindow24h 78.30%",
    );
  });

  it("keeps channel tooltip and overflow actions above the row detail overlay", async () => {
    const user = userEvent.setup();
    render(<ModelCard model={realModel(Array.from(
      { length: 6 },
      (_, index) => offer(`Channel ${index + 1}`, ["responses"]),
    ))} />);

    const row = screen.getByRole("article", { name: "Model A" });
    const channelGroup = within(row).getByLabelText("channelGroupLabel");
    expect(channelGroup).toHaveClass("relative", "z-10");

    await user.hover(within(row).getByLabelText("Channel 1"));
    expect(await screen.findByRole("tooltip")).toHaveTextContent("Channel 1");

    await user.click(within(row).getByRole("button", { name: "showAllChannels 6" }));
    expect(await screen.findByRole("dialog", { name: "allChannelsDialogLabel 6" }))
      .toHaveTextContent("Channel 6");
    expect(within(row).getByTestId("marketplace-row-link")).toHaveAttribute(
      "href",
      "/model-marketplace/detail?model=model-a&window=24h",
    );
  });

  it.each([
    { endpoints: ["models"] as const, invocation: false, discovery: true },
    { endpoints: ["chat_completions", "models"] as const, invocation: true, discovery: true },
    { endpoints: ["chat_completions"] as const, invocation: true, discovery: false },
    { endpoints: [] as const, invocation: false, discovery: false },
  ])("separates invocation and discovery semantics for $endpoints", ({ endpoints, invocation, discovery }) => {
    render(<ModelCard model={realModel([offer("Offer", [...endpoints])])} />);

    const card = screen.getByRole("article", { name: "Model A" });
    expect(within(card).getByText("invocationEndpointsLabel")).toBeInTheDocument();
    expect(within(card).queryByText("modelDiscoveryLabel") !== null).toBe(discovery);
    expect(within(card).queryByText("endpoint.chat_completions") !== null).toBe(invocation);
    expect(within(card).queryByText("endpoint.models") !== null).toBe(discovery);
  });
});

describe("routing model marketplace card", () => {
  it("renders reachability without synthesizing offer availability or prices", () => {
    render(<ModelCard model={routingModel([], ["gpt-4o"])} />);

    const row = screen.getByRole("article", { name: "Route A" });
    expect(within(row).getByTestId("marketplace-model-identity")).toBeInTheDocument();
    expect(within(row).queryByTestId("offer-availability-tracker")).not.toBeInTheDocument();
    expect(within(row).queryByTestId("model-performance-strip")).not.toBeInTheDocument();
    expect(within(row).queryByTestId("marketplace-price-item")).not.toBeInTheDocument();
    expect(within(row).queryByLabelText("channelGroupLabel")).not.toBeInTheDocument();
    const rowLink = within(row).getByTestId("marketplace-row-link");
    expect(rowLink).toHaveClass("after:absolute", "after:inset-0");
    expect(rowLink).toHaveAttribute(
      "href",
      "/model-marketplace/detail?model=route-a&window=24h",
    );
    const reachableLink = within(row).getByRole("link", { name: "gpt-4o" });
    expect(reachableLink).toHaveAttribute(
      "href",
      "/model-marketplace/detail?model=gpt-4o&window=24h",
    );
    expect(reachableLink).toHaveClass("relative", "z-10");
    expect(row.querySelector("a a")).not.toBeInTheDocument();
  });

  it("keeps Token and window context on each reachable real-model link", () => {
    render(<ModelCard model={routingModel([], ["gpt-4o", "claude-3-7"])} detailTokenId={17} />);

    expect(screen.getByRole("link", { name: "gpt-4o" })).toHaveAttribute(
      "href",
      "/model-marketplace/detail?model=gpt-4o&token_id=17&window=24h",
    );
    expect(screen.getByRole("link", { name: "claude-3-7" })).toHaveAttribute(
      "href",
      "/model-marketplace/detail?model=claude-3-7&token_id=17&window=24h",
    );
  });

  it.each(["cycle", "max_depth", "disabled", "model_not_found", "no_visible_offer"])(
    "renders the safe localized %s warning",
    (warning) => {
      render(<ModelCard model={routingModel([warning])} />);
      const alert = screen.getByRole("alert");
      expect(alert).toHaveTextContent(`routingWarning.code.${warning}`);
    },
  );

  it("maps an unknown server warning to generic copy without exposing the raw value", () => {
    render(<ModelCard model={routingModel(["backend secret diagnostics"]) } />);

    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("routingWarning.code.unknown");
    expect(alert).not.toHaveTextContent("backend secret diagnostics");
  });

  it("distinguishes an unwarned route with zero destinations from a warning", () => {
    render(<ModelCard model={routingModel([])} />);

    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByText("routingNoReachableModels")).toBeInTheDocument();
  });
});

describe("marketplace runtime translations", () => {
  it("separates localized summary windows from the UTC bucket SLA formula", () => {
    expect(en.modelMarketplace.modelSlaSummaryTooltip)
      .toBe("Observed success rate over {window}: {value}");
    expect(zh.modelMarketplace.modelSlaSummaryTooltip)
      .toBe("{window}内观测成功率：{value}");
    expect(en.modelMarketplace.modelCacheHitRateSummaryTooltip)
      .toBe("Over {window}, cache-read tokens are {value} of input plus cache-read tokens.");
    expect(zh.modelMarketplace.modelCacheHitRateSummaryTooltip)
      .toBe("{window}内，缓存读取 Token 占输入 Token 加缓存读取 Token 的 {value}。");
    expect(en.modelMarketplace.modelSlaBucketTooltip)
      .toBe("Observed success rate for this UTC interval: {value}");
    expect(zh.modelMarketplace.modelSlaBucketTooltip)
      .toBe("该 UTC 时间段的观测成功率：{value}");
  });

  it("contains every new warning and endpoint-group key in both locales", () => {
    const keys = [
      "invocationEndpointsLabel",
      "modelDiscoveryLabel",
      "routingNoReachableModels",
      "routingWarning.title",
      "routingWarning.code.cycle",
      "routingWarning.code.max_depth",
      "routingWarning.code.disabled",
      "routingWarning.code.model_not_found",
      "routingWarning.code.no_visible_offer",
      "routingWarning.code.unknown",
      "offerAvailabilityLabel",
      "performanceWindow24h",
      "modelSlaLabel",
      "modelCacheHitRateLabel",
      "modelSlaSummaryTooltip",
      "modelCacheHitRateSummaryTooltip",
      "modelSlaBucketTooltip",
      "detail.statusState.stale",
    ];
    const lookup = (messages: typeof en, key: string) => key.split(".").reduce<unknown>(
      (value, segment) => typeof value === "object" && value !== null
        ? (value as Record<string, unknown>)[segment]
        : undefined,
      messages.modelMarketplace,
    );

    for (const key of keys) {
      expect(lookup(en, key), `en:${key}`).toEqual(expect.any(String));
      expect(lookup(zh as typeof en, key), `zh:${key}`).toEqual(expect.any(String));
    }
  });

  it("resolves every Task 7 loading and usage key to non-empty text in both locales", () => {
    const keys = [
      "catalogLoading",
      "detail.loading",
      "detail.usageTitle",
      "detail.usageDescription",
      "detail.usageScope.selected_token",
      "detail.usageScope.owner_channel_total",
      "detail.usageScope.offer_total",
      "detail.tokenUnit.input",
      "detail.tokenUnit.cache_read",
      "detail.tokenUnit.output",
      "detail.tokenUnit.cache_write",
      "detail.totalTokenUnits",
      "detail.includesSharedUsage",
      "detail.usageUpstreamCost",
      "detail.usageGatewayCost",
      "detail.usageEstimatedTotal",
      "detail.unknownAmount",
      "detail.usageReferenceNotice",
      "detail.usageExactNotice",
      "detail.usageUnavailableTitle",
      "detail.usageUnavailableDescription",
      "detail.usageEmptyTitle",
      "detail.usageEmptyDescription",
    ];
    const lookup = (messages: typeof en, key: string) => key.split(".").reduce<unknown>(
      (value, segment) => typeof value === "object" && value !== null
        ? (value as Record<string, unknown>)[segment]
        : undefined,
      messages.modelMarketplace,
    );

    for (const key of keys) {
      const englishValue = lookup(en, key);
      const chineseValue = lookup(zh as typeof en, key);
      expect(typeof englishValue === "string" && englishValue.length > 0, `en:${key}`)
        .toBe(true);
      expect(typeof chineseValue === "string" && chineseValue.length > 0, `zh:${key}`)
        .toBe(true);
    }
  });
});
