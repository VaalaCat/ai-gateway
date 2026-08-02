import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type {
  MarketplaceModelOfferDetail,
} from "@/lib/api/model-marketplace";
import {
  type AdminOfferDiagnosticsView,
  defaultMarketplaceOfferRefs,
  OfferComparison,
} from "./offer-comparison";

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

function makeOffer(
  ref: string,
  overrides: Partial<MarketplaceModelOfferDetail> = {},
): MarketplaceModelOfferDetail {
  return {
    offer_ref: ref,
    kind: "platform",
    display_name: ref.toUpperCase(),
    ownership: "platform",
    available: true,
    supported_endpoints: ["responses", "models"],
    pricing: {
      reference_price: { input: 10, output: 10, cache_read: 2, cache_write: 3 },
      gateway_charge: { input: 10, output: 10, cache_read: 2, cache_write: 3 },
      estimated_total: { input: 10, output: 10, cache_read: 2, cache_write: 3 },
      accuracy: "exact",
    },
    performance_status: "available",
    performance: {
      status: "operational",
      success_rate: 99,
      ttft_avg_ms: 100,
      ttft_p95_ms: 120,
      tps_avg: 40,
      tps_p5: 20,
      duration_p95_ms: 1_000,
      token_units: { input: 1, output: 2, cache_read: 3, cache_write: 4, total: 10 },
    },
    status_history: [],
    trend_series: [],
    usage_references: [],
    ...overrides,
  };
}

describe("marketplace default offer selection", () => {
  it("stops at three when Input, TTFT, and SLA have different winners", () => {
    const offers = [
      makeOffer("input", {
        pricing: {
          ...makeOffer("x").pricing,
          gateway_charge: { input: 1, output: 20, cache_read: 2, cache_write: 3 },
          estimated_total: { input: 1, output: 20, cache_read: 2, cache_write: 3 },
        },
      }),
      makeOffer("ttft", { performance: { ...makeOffer("x").performance, ttft_avg_ms: 10 } }),
      makeOffer("sla", { performance: { ...makeOffer("x").performance, success_rate: 100 } }),
      makeOffer("output", {
        pricing: {
          ...makeOffer("x").pricing,
          gateway_charge: { input: 20, output: 1, cache_read: 2, cache_write: 3 },
          estimated_total: { input: 20, output: 1, cache_read: 2, cache_write: 3 },
        },
      }),
      makeOffer("tps", { performance: { ...makeOffer("x").performance, tps_avg: 100 } }),
      makeOffer("other"),
    ];

    expect(defaultMarketplaceOfferRefs(offers)).toEqual(["input", "ttft", "sla"]);
  });

  it("uses Output and TPS only to fill duplicate primary winners up to three", () => {
    const primary = makeOffer("primary", {
      pricing: {
        ...makeOffer("x").pricing,
        estimated_total: { input: 1, output: 20, cache_read: 2, cache_write: 3 },
      },
      performance: {
        ...makeOffer("x").performance,
        ttft_avg_ms: 1,
        success_rate: 100,
      },
    });
    const output = makeOffer("output", {
      pricing: {
        ...makeOffer("x").pricing,
        estimated_total: { input: 20, output: 1, cache_read: 2, cache_write: 3 },
      },
    });
    const tps = makeOffer("tps", {
      performance: { ...makeOffer("x").performance, tps_avg: 200 },
    });

    expect(defaultMarketplaceOfferRefs([primary, output, tps])).toEqual([
      "primary",
      "output",
      "tps",
    ]);
  });

  it("returns the available finite winners without treating null, NaN, or Infinity as zero", () => {
    const unknown = makeOffer("unknown", {
      pricing: {
        ...makeOffer("x").pricing,
        gateway_charge: {
          input: null as unknown as number,
          output: null as unknown as number,
          cache_read: 0,
          cache_write: 0,
        },
        estimated_total: {
          input: null as unknown as number,
          output: null as unknown as number,
          cache_read: 0,
          cache_write: 0,
        },
      },
      performance: {
        ...makeOffer("x").performance,
        ttft_avg_ms: null,
        success_rate: null,
        tps_avg: null,
      },
    });
    const measured = makeOffer("measured", {
      pricing: {
        ...makeOffer("x").pricing,
        gateway_charge: { input: 8, output: 9, cache_read: 2, cache_write: 3 },
        estimated_total: { input: 8, output: 9, cache_read: 2, cache_write: 3 },
      },
    });
    const nonFinite = makeOffer("non-finite", {
      pricing: {
        ...makeOffer("x").pricing,
        estimated_total: {
          input: Number.NaN,
          output: Number.POSITIVE_INFINITY,
          cache_read: 0,
          cache_write: 0,
        },
      },
      performance: {
        ...makeOffer("x").performance,
        ttft_avg_ms: Number.NaN,
        success_rate: Number.NEGATIVE_INFINITY,
        tps_avg: Number.POSITIVE_INFINITY,
      },
    });

    expect(defaultMarketplaceOfferRefs([unknown, nonFinite, measured])).toEqual(["measured"]);
  });

  it("excludes an unavailable low-price source from every default winner", () => {
    const unavailable = makeOffer("disabled", {
      available: false,
      pricing: {
        ...makeOffer("x").pricing,
        estimated_total: { input: 0, output: 0, cache_read: 0, cache_write: 0 },
      },
      performance: {
        ...makeOffer("x").performance,
        ttft_avg_ms: 0,
        success_rate: 100,
        tps_avg: 1_000,
      },
    });
    const available = makeOffer("available", {
      pricing: {
        ...makeOffer("x").pricing,
        estimated_total: { input: 8, output: 9, cache_read: 2, cache_write: 3 },
      },
    });

    expect(defaultMarketplaceOfferRefs([unavailable, available])).toEqual(["available"]);
  });
});

describe("offer comparison", () => {
  it("defaults to three but lets the user select five and rejects a sixth", () => {
    const offers = [
      makeOffer("input", {
        pricing: {
          ...makeOffer("x").pricing,
          gateway_charge: { input: 1, output: 20, cache_read: 2, cache_write: 3 },
          estimated_total: { input: 1, output: 20, cache_read: 2, cache_write: 3 },
        },
      }),
      makeOffer("ttft", { performance: { ...makeOffer("x").performance, ttft_avg_ms: 10 } }),
      makeOffer("sla", { performance: { ...makeOffer("x").performance, success_rate: 100 } }),
      makeOffer("output", {
        pricing: {
          ...makeOffer("x").pricing,
          gateway_charge: { input: 20, output: 1, cache_read: 2, cache_write: 3 },
          estimated_total: { input: 20, output: 1, cache_read: 2, cache_write: 3 },
        },
      }),
      makeOffer("tps", { performance: { ...makeOffer("x").performance, tps_avg: 100 } }),
      makeOffer("sixth"),
    ];
    render(<OfferComparison offers={offers} window="24h" usageStatus="available" />);

    expect(screen.getAllByRole("button", { pressed: true })).toHaveLength(3);
    fireEvent.click(screen.getByRole("button", { name: "OUTPUT" }));
    fireEvent.click(screen.getByRole("button", { name: "TPS" }));
    expect(screen.getAllByRole("button", { pressed: true })).toHaveLength(5);
    fireEvent.click(screen.getByRole("button", { name: "SIXTH" }));

    expect(screen.getAllByRole("button", { pressed: true })).toHaveLength(5);
    expect(screen.getByText("selectionLimit 5")).toBeInTheDocument();
  });

  it("uses the same rows for platform and BYOK while separating invocation from Models API", () => {
    const platform = makeOffer("platform", {
      supported_endpoints: ["chat_completions", "models"],
      pricing: {
        ...makeOffer("x").pricing,
        gateway_charge: { input: 1, output: 9, cache_read: 2, cache_write: 3 },
      },
    });
    const byok = makeOffer("byok", {
      kind: "private",
      ownership: "owned",
      supported_endpoints: ["responses", "models"],
      pricing: {
        reference_price: { input: 3, output: 4, cache_read: 1, cache_write: 2 },
        gateway_charge: { input: 0.3, output: 0.4, cache_read: 0.1, cache_write: 0.2 },
        estimated_total: { input: 3.3, output: 4.4, cache_read: 1.1, cache_write: 2.2 },
        accuracy: "reference",
      },
      performance: { ...makeOffer("x").performance, ttft_avg_ms: 10 },
    });

    render(<OfferComparison offers={[platform, byok]} window="24h" usageStatus="available" />);

    const comparison = screen.getByRole("table", { name: "comparisonTableLabel" });
    expect(within(comparison).getByText("invocationEndpointsLabel")).toBeInTheDocument();
    expect(within(comparison).getByText("modelDiscoveryLabel")).toBeInTheDocument();
    for (const label of within(comparison).getAllByTestId("price-bucket-label")) {
      expect(label).toHaveTextContent("estimatedTotalLabel");
      expect(label).toHaveTextContent("priceUnitLabel");
    }
    expect(within(comparison).queryByText("byokUpstreamPriceLabel")).not.toBeInTheDocument();
    fireEvent.click(within(comparison).getByRole("button", {
      name: "byokPriceBreakdownTriggerLabel BYOK 2",
    }));
    expect(within(comparison).getByText("byokUpstreamPriceLabel")).toBeInTheDocument();
    expect(within(comparison).getByText("gatewayChargeLabel")).toBeInTheDocument();
    expect(within(comparison).getByText("estimatedTotalLabel")).toBeInTheDocument();
    const priceLayers = within(comparison).getAllByTestId("byok-price-layer");
    expect(priceLayers).toHaveLength(3);
    for (const layer of priceLayers) {
      expect(layer).toHaveTextContent("priceUnitLabel");
      expect(within(layer).getAllByTestId("marketplace-price-item").map((item) =>
        item.getAttribute("data-price-key"),
      )).toEqual(["input", "cache_read", "output", "cache_write"]);
    }
    expect(screen.getByText("byokBillingNotice")).toBeInTheDocument();
  });

  it("gives same-name BYOK breakdown buttons unique public positions without diagnostics", () => {
    const alpha = Object.assign(makeOffer("alpha", {
      kind: "private",
      display_name: "Shared BYOK",
      ownership: "owned",
      pricing: {
        ...makeOffer("alpha").pricing,
        estimated_total: { input: 1, output: 20, cache_read: 2, cache_write: 3 },
      },
    }), {
      internal_name: "alpha-secret",
      base_url: "https://alpha-internal.example.test",
    });
    const beta = Object.assign(makeOffer("beta", {
      kind: "private",
      display_name: "Shared BYOK",
      ownership: "owned",
      performance: { ...makeOffer("beta").performance, ttft_avg_ms: 1 },
    }), {
      internal_name: "beta-secret",
      base_url: "https://beta-internal.example.test",
    });

    render(<OfferComparison offers={[alpha, beta]} window="24h" usageStatus="available" />);

    const comparison = screen.getByRole("table", { name: "comparisonTableLabel" });
    expect(within(comparison).getByRole("button", {
      name: "byokPriceBreakdownTriggerLabel Shared BYOK 1",
    })).toBeInTheDocument();
    expect(within(comparison).getByRole("button", {
      name: "byokPriceBreakdownTriggerLabel Shared BYOK 2",
    })).toBeInTheDocument();
    expect(comparison).not.toHaveTextContent(/secret|internal|base_url/i);
  });

  it.each([
    { name: "only invocation", endpoints: [["responses"]] as const, discoveryLabels: 0 },
    { name: "only models", endpoints: [["models"]] as const, discoveryLabels: 2 },
    {
      name: "mixed",
      endpoints: [["responses"], ["models"]] as const,
      discoveryLabels: 2,
    },
    { name: "empty", endpoints: [[]] as const, discoveryLabels: 0 },
  ])("uses one conditional row registry for $name desktop and mobile views", ({
    endpoints,
    discoveryLabels,
  }) => {
    const offers = endpoints.map((supportedEndpoints, index) => makeOffer(`offer-${index}`, {
      supported_endpoints: [...supportedEndpoints],
      performance: {
        ...makeOffer("x").performance,
        ttft_avg_ms: 100 - index,
      },
    }));

    render(<OfferComparison offers={offers} window="24h" usageStatus="available" />);

    expect(screen.getAllByText("invocationEndpointsLabel")).toHaveLength(2);
    expect(screen.queryAllByText("modelDiscoveryLabel")).toHaveLength(discoveryLabels);
  });

  it("keeps an unavailable administrator source unselected by default but allows explicit diagnosis", () => {
    const unavailable = makeOffer("disabled", {
      available: false,
      supported_endpoints: [],
      pricing: {
        ...makeOffer("x").pricing,
        estimated_total: {
          input: null as unknown as number,
          output: null as unknown as number,
          cache_read: 0,
          cache_write: 0,
        },
      },
      performance: {
        ...makeOffer("x").performance,
        ttft_avg_ms: null,
        success_rate: null,
        tps_avg: null,
      },
    });
    const diagnostics: AdminOfferDiagnosticsView = {
      channelId: 42,
      privateChannelId: 0,
      internalName: "disabled-internal",
      publicDisplayName: "Disabled public source",
      ownerId: 7,
      baseUrl: "https://disabled.example",
      endpointPaths: [],
      disabledReasons: ["disabled", "endpoints_not_configured"],
      requestCount: 0,
      successCount: 0,
      failureCount: 0,
      streamRequestCount: 0,
      ttftSampleCount: 0,
      tpsSampleCount: 0,
      durationSampleCount: 0,
    };

    render(
      <OfferComparison
        offers={[makeOffer("available"), unavailable]}
        window="24h"
        usageStatus="available"
        adminDiagnostics={{ disabled: diagnostics }}
      />,
    );

    const disabledButton = screen.getByRole("button", { name: "DISABLED" });
    expect(disabledButton).toHaveAttribute("aria-pressed", "false");
    expect(within(disabledButton).getByText("offerUnavailable")).toBeInTheDocument();

    fireEvent.click(disabledButton);

    expect(screen.getByText("disabledReason.disabled")).toBeInTheDocument();
    expect(screen.getByText("disabledReason.endpoints_not_configured")).toBeInTheDocument();
    expect(screen.queryByText(/credential|cipher|secret/i)).not.toBeInTheDocument();
  });

  it("keeps the billing rows fixed and switches one mobile fact sheet without changing comparison selection", () => {
    const byok = makeOffer("byok", {
      kind: "private",
      display_name: "BYOK",
      ownership: "owned",
      performance: { ...makeOffer("byok").performance, ttft_avg_ms: 5 },
    });
    render(
      <OfferComparison
        offers={[
          makeOffer("input", {
            pricing: {
              ...makeOffer("input").pricing,
              estimated_total: { input: 1, output: 20, cache_read: 2, cache_write: 3 },
            },
          }),
          byok,
          makeOffer("sla", {
            performance: { ...makeOffer("sla").performance, success_rate: 100 },
          }),
        ]}
        window="24h"
        usageStatus="available"
      />,
    );

    const comparison = screen.getByRole("table", { name: "comparisonTableLabel" });
    const comparisonScroll = screen.getByTestId("offer-comparison-scroll");
    const horizontalOverflowOwners = [comparisonScroll, ...comparisonScroll.querySelectorAll("*")]
      .filter((node) => node.classList.contains("overflow-x-auto"));
    expect(horizontalOverflowOwners).toEqual([comparisonScroll]);
    expect(within(comparison).getByText("metricColumn").closest(".overflow-x-auto"))
      .toBe(comparisonScroll);
    expect(screen.getByTestId("offer-status-scroll")).toHaveClass("overflow-x-auto");
    const priceLabels = within(comparison).getAllByTestId("price-bucket-label");
    expect(priceLabels.map((node) => node.getAttribute("data-price-key"))).toEqual([
      "input",
      "cache_read",
      "output",
      "cache_write",
    ]);
    expect(priceLabels[1]).toHaveClass("text-muted-foreground");
    expect(priceLabels[3]).toHaveClass("text-muted-foreground");
    const factSheet = screen.getByTestId("mobile-offer-fact-sheet");
    expect(screen.getAllByTestId("mobile-offer-fact-sheet")).toHaveLength(1);
    const mobilePriceLabels = within(factSheet).getAllByTestId("price-bucket-label");
    expect(mobilePriceLabels).toHaveLength(4);
    for (const label of mobilePriceLabels) {
      expect(label).toHaveTextContent("estimatedTotalLabel");
      expect(label).toHaveTextContent("priceUnitLabel");
    }
    expect(screen.getAllByRole("button", { pressed: true })).toHaveLength(3);

    const mobileOffers = screen.getByRole("group", { name: "mobileOfferSelectionLabel" });
    fireEvent.click(within(mobileOffers).getByRole("radio", { name: "BYOK" }));

    expect(screen.getByTestId("mobile-offer-fact-sheet")).toHaveTextContent("BYOK");
    expect(screen.getAllByRole("button", { pressed: true })).toHaveLength(3);
  });

  it("keeps an explicitly active unavailable mobile offer when the same offers reorder", () => {
    const first = makeOffer("first");
    const unavailable = makeOffer("unavailable", { available: false });
    const third = makeOffer("third");
    const { rerender } = render(
      <OfferComparison
        offers={[first, unavailable, third]}
        window="24h"
        usageStatus="available"
      />,
    );
    const mobileOffers = screen.getByRole("group", { name: "mobileOfferSelectionLabel" });
    fireEvent.click(within(mobileOffers).getByRole("radio", { name: "UNAVAILABLE" }));
    expect(screen.getByTestId("mobile-offer-fact-sheet")).toHaveTextContent("UNAVAILABLE");

    rerender(
      <OfferComparison
        offers={[third, unavailable, first]}
        window="24h"
        usageStatus="available"
      />,
    );

    expect(screen.getByTestId("mobile-offer-fact-sheet")).toHaveTextContent("UNAVAILABLE");
    expect(within(screen.getByRole("group", { name: "mobileOfferSelectionLabel" }))
      .getByRole("radio", { name: "UNAVAILABLE" })).toHaveAttribute("aria-checked", "true");
  });

  it("falls back when the active mobile offer is deleted", () => {
    const first = makeOffer("first");
    const second = makeOffer("second", {
      performance: { ...makeOffer("second").performance, ttft_avg_ms: 5 },
    });
    const { rerender } = render(
      <OfferComparison offers={[first, second]} window="24h" usageStatus="available" />,
    );
    fireEvent.click(within(screen.getByRole("group", { name: "mobileOfferSelectionLabel" }))
      .getByRole("radio", { name: "SECOND" }));

    rerender(
      <OfferComparison offers={[first]} window="24h" usageStatus="available" />,
    );

    expect(screen.getByTestId("mobile-offer-fact-sheet")).toHaveTextContent("FIRST");
  });

  it("renders no mobile fact sheet for an empty offer collection", () => {
    render(<OfferComparison offers={[]} window="24h" usageStatus="available" />);

    expect(screen.queryByTestId("mobile-offer-fact-sheet")).not.toBeInTheDocument();
  });

  it("shows BYOK pricing and billing notice for an active mobile offer outside the defaults", () => {
    const input = makeOffer("input", {
      pricing: {
        ...makeOffer("input").pricing,
        estimated_total: { input: 1, output: 20, cache_read: 2, cache_write: 3 },
      },
    });
    const ttft = makeOffer("ttft", {
      performance: { ...makeOffer("ttft").performance, ttft_avg_ms: 5 },
    });
    const sla = makeOffer("sla", {
      performance: { ...makeOffer("sla").performance, success_rate: 100 },
    });
    const byok = makeOffer("byok-outside-default", {
      kind: "private",
      display_name: "BYOK OUTSIDE DEFAULT",
      ownership: "owned",
      pricing: {
        reference_price: { input: 3, output: 4, cache_read: 1, cache_write: 2 },
        gateway_charge: { input: 0.3, output: 0.4, cache_read: 0.1, cache_write: 0.2 },
        estimated_total: { input: 3.3, output: 4.4, cache_read: 1.1, cache_write: 2.2 },
        accuracy: "reference",
      },
    });
    render(
      <OfferComparison
        offers={[input, ttft, sla, byok]}
        window="24h"
        usageStatus="available"
      />,
    );
    const selectedOffers = screen.getByRole("group", { name: "offerSelectionLabel" });
    const byokSelection = within(selectedOffers).getByRole("button", {
      name: "BYOK OUTSIDE DEFAULT",
    });
    expect(byokSelection).toHaveAttribute("aria-pressed", "false");
    expect(screen.queryByText("byokBillingNotice")).not.toBeInTheDocument();

    fireEvent.click(within(screen.getByRole("group", { name: "mobileOfferSelectionLabel" }))
      .getByRole("radio", { name: "BYOK OUTSIDE DEFAULT" }));

    const factSheet = screen.getByTestId("mobile-offer-fact-sheet");
    expect(factSheet).toHaveTextContent("BYOK OUTSIDE DEFAULT");
    fireEvent.click(within(factSheet).getByRole("button", {
      name: "byokPriceBreakdownTriggerLabel BYOK OUTSIDE DEFAULT 4",
    }));
    expect(within(factSheet).getAllByTestId("byok-price-layer")).toHaveLength(3);
    expect(screen.getByText("byokBillingNotice")).toBeInTheDocument();
    expect(byokSelection).toHaveAttribute("aria-pressed", "false");
    expect(screen.getAllByTestId("offer-status-row")).toHaveLength(3);
    expect(screen.queryByLabelText(`status${"LegendLabel"}`)).not.toBeInTheDocument();
  });

  it("renders internal diagnostics only when the administrator projection is explicit", () => {
    const offer = makeOffer("platform");
    const diagnostics: AdminOfferDiagnosticsView = {
      channelId: 42,
      privateChannelId: 0,
      internalName: "internal-channel",
      publicDisplayName: "Public channel",
      ownerId: 7,
      baseUrl: "https://internal.example",
      endpointPaths: [{ endpoint: "responses", path: "/v1/responses" }],
      disabledReasons: [],
      requestCount: 1,
      successCount: 1,
      failureCount: 0,
      streamRequestCount: 0,
      ttftSampleCount: 1,
      tpsSampleCount: 1,
      durationSampleCount: 1,
    };
    const { rerender } = render(
      <OfferComparison offers={[offer]} window="24h" usageStatus="available" />,
    );

    expect(screen.queryByText("internal-channel")).not.toBeInTheDocument();
    expect(screen.queryByText("https://internal.example")).not.toBeInTheDocument();

    rerender(
      <OfferComparison
        offers={[offer]}
        window="24h"
        usageStatus="available"
        adminDiagnostics={{ platform: diagnostics }}
      />,
    );
    expect(screen.getByText(/internal-channel/)).toBeInTheDocument();
    expect(screen.getByText(/https:\/\/internal\.example/)).toBeInTheDocument();
    expect(screen.queryByText(/key|cipher/i)).not.toBeInTheDocument();
    expect(screen.queryByText("comparisonDescription")).not.toBeInTheDocument();
    expect(screen.queryByText("mobileOfferSummary")).not.toBeInTheDocument();
    expect(screen.queryByText("adminDiagnosticsDescription")).not.toBeInTheDocument();

    const adminCard = screen.getByText("adminDiagnosticsTitle").closest("[data-slot='card']")! as HTMLElement;
    expect(within(adminCard).getByText("adminDiagnosticsAdminOnly"))
      .toHaveAttribute("data-slot", "badge");
    expect(within(adminCard).getByText("adminDiagnosticsAdminOnly")
      .closest("[data-slot='card-action']")).toBeInTheDocument();
    expect(within(adminCard).queryByTestId("channel-avatar-group")).not.toBeInTheDocument();
    expect(within(adminCard).queryByRole("button", { name: /showAllChannels/ })).not.toBeInTheDocument();
    expect(within(adminCard).getAllByTestId("channel-avatar")).toHaveLength(1);
  });

  it("keeps long administrator identity and endpoint values complete in shrinkable definition rows", () => {
    const longInternalName = "internal-channel-without-breaks-".repeat(8);
    const longBaseUrl = `https://diagnostics.example/${"tenant-segment".repeat(12)}`;
    const longEndpointPath = `/v1/${"responses-without-breaks".repeat(12)}`;
    const diagnostics: AdminOfferDiagnosticsView = {
      channelId: 42,
      privateChannelId: 0,
      internalName: longInternalName,
      publicDisplayName: "Public channel",
      ownerId: 7,
      baseUrl: longBaseUrl,
      endpointPaths: [{ endpoint: "responses", path: longEndpointPath }],
      disabledReasons: [],
      requestCount: 1,
      successCount: 1,
      failureCount: 0,
      streamRequestCount: 0,
      ttftSampleCount: 1,
      tpsSampleCount: 1,
      durationSampleCount: 1,
    };

    render(
      <OfferComparison
        offers={[makeOffer("platform")]}
        window="24h"
        usageStatus="available"
        adminDiagnostics={{ platform: diagnostics }}
      />,
    );

    const adminCard = screen.getByText("adminDiagnosticsTitle")
      .closest("[data-slot='card']")! as HTMLElement;
    const internalValue = within(adminCard).getByText(longInternalName);
    const baseUrlValue = within(adminCard).getByText(longBaseUrl);
    const endpointValue = within(adminCard).getByText(longEndpointPath);

    expect(internalValue.tagName).toBe("DD");
    expect(internalValue).toHaveClass("min-w-0", "[overflow-wrap:anywhere]");
    expect(internalValue).not.toHaveClass("truncate");
    expect(baseUrlValue.tagName).toBe("DD");
    expect(baseUrlValue).toHaveClass("min-w-0", "break-all");
    expect(endpointValue.tagName).toBe("DD");
    expect(endpointValue).toHaveClass("min-w-0", "break-all");
    expect(within(adminCard).getByText("responses").tagName).toBe("DT");
    expect(adminCard.querySelector(".overflow-x-auto")).toBeNull();
  });

  it("drives status, one trend workspace, and usage evidence from the same selected offers", () => {
    const offer = makeOffer("platform", {
      trend_series: [{
        started_at: 1,
        ended_at: 2,
        status: "operational",
        in_progress: false,
        success_rate: 99,
        ttft_avg_ms: 100,
        tps_avg: 40,
        token_units: { input: 1, output: 2, cache_read: 3, cache_write: 4, total: 10 },
      }],
    });
    render(<OfferComparison offers={[offer]} window="24h" usageStatus="available" />);

    expect(screen.getByTestId("offer-status-matrix")).toBeInTheDocument();
    expect(screen.getAllByTestId("offer-status-row")).toHaveLength(1);
    const trendWorkspace = screen.getByRole("region", { name: "trendTitle" });
    expect(within(trendWorkspace).getAllByTestId("responsive-chart-frame")).toHaveLength(1);
    expect(within(trendWorkspace).getAllByText("trendTitle")).toHaveLength(1);
    expect(screen.getByText("usageEmptyTitle")).toBeInTheDocument();
  });
});
