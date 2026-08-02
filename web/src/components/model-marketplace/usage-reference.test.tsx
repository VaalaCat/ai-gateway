import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type {
  MarketplaceModelOfferDetail,
  MarketplaceUsageReference,
} from "@/lib/api/model-marketplace";
import { UsageReference } from "./usage-reference";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, string | number>) =>
    values ? `${key} ${Object.values(values).join(" ")}` : key,
}));

function usage(
  scope: MarketplaceUsageReference["scope"],
  total: number,
  overrides: Partial<MarketplaceUsageReference> = {},
): MarketplaceUsageReference {
  return {
    scope,
    window: "24h",
    token_units: {
      input: total + 1,
      output: total + 2,
      cache_read: total + 3,
      cache_write: total + 4,
      total,
    },
    reference_cost: 100_000,
    gateway_charge_cost: 50_000,
    estimated_total_cost: 150_000,
    accuracy: "reference",
    includes_shared_usage: false,
    ...overrides,
  };
}

function makeOffer(
  ref: string,
  usageReferences: MarketplaceUsageReference[],
  kind: MarketplaceModelOfferDetail["kind"] = "private",
): MarketplaceModelOfferDetail {
  return {
    offer_ref: ref,
    kind,
    display_name: ref.toUpperCase(),
    ownership: kind === "private" ? "owned" : "platform",
    available: true,
    supported_endpoints: ["responses"],
    pricing: {
      reference_price: { input: 1, output: 2, cache_read: 3, cache_write: 4 },
      gateway_charge: { input: 1, output: 2, cache_read: 3, cache_write: 4 },
      estimated_total: { input: 1, output: 2, cache_read: 3, cache_write: 4 },
      accuracy: kind === "private" ? "reference" : "exact",
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
    usage_references: usageReferences,
  };
}

describe("marketplace usage reference", () => {
  it("keeps server scopes separate and formats Token and money evidence in billing order", async () => {
    const user = userEvent.setup();
    render(<UsageReference offers={[
      makeOffer("byok", [
        usage("selected_token", 10_000, {
          token_units: {
            input: 1_000,
            cache_read: 2_000,
            output: 3_000,
            cache_write: 4_000,
            total: 10_000,
          },
        }),
        usage("owner_channel_total", 20, { includes_shared_usage: true }),
      ]),
      makeOffer("platform", [usage("offer_total", 30)], "platform"),
    ]} usageStatus="available" />);

    for (const scope of ["selected_token", "owner_channel_total", "offer_total"] as const) {
      expect(screen.getByRole("article", { name: `usageScope.${scope}` })).toBeInTheDocument();
    }
    const selected = screen.getByRole("article", { name: "usageScope.selected_token" });
    expect(within(selected).getAllByTestId("usage-token-bucket").map((node) =>
      node.getAttribute("data-token-key"),
    )).toEqual(["input", "cache_read", "output", "cache_write", "total"]);
    expect(within(selected).getByText("1.00K")).toBeInTheDocument();
    expect(within(selected).getByText("$ 1.00")).toBeInTheDocument();
    expect(within(selected).queryByText("$100000.00")).not.toBeInTheDocument();
    const buckets = within(selected).getAllByTestId("usage-token-bucket");
    expect(buckets[1]).toHaveClass("text-muted-foreground");
    expect(buckets[3]).toHaveClass("text-muted-foreground");
    expect(buckets[0]).not.toHaveClass("text-muted-foreground");
    expect(buckets[2]).not.toHaveClass("text-muted-foreground");

    await user.hover(within(selected).getByText("1.00K"));
    expect(await screen.findByRole("tooltip")).toHaveTextContent("1,000");
    expect(within(screen.getByRole("article", { name: "usageScope.owner_channel_total" })).getByText("20"))
      .toBeInTheDocument();
    expect(screen.getByText("includesSharedUsage")).toBeInTheDocument();
  });

  it("shows the shared exact money format from a compact amount", async () => {
    const user = userEvent.setup();
    render(<UsageReference offers={[
      makeOffer("byok", [usage("selected_token", 10)]),
    ]} usageStatus="available" />);

    await user.hover(screen.getByText("$ 1.00"));

    expect(await screen.findByRole("tooltip")).toHaveTextContent("$ 1.000000");
  });

  it("renders an unknown upstream amount as unknown while preserving a real zero gateway fee", () => {
    render(<UsageReference offers={[
      makeOffer("byok", [usage("selected_token", 10, {
        reference_cost: null,
        gateway_charge_cost: 0,
        estimated_total_cost: null,
      })]),
    ]} usageStatus="available" />);

    const card = screen.getByRole("article", { name: "usageScope.selected_token" });
    expect(within(card).getAllByText("unknownAmount")).toHaveLength(2);
    expect(within(card).getByText("$ 0.00")).toBeInTheDocument();
    expect(within(card).queryByText("$ 0.00", { selector: "[data-cost=upstream]" }))
      .not.toBeInTheDocument();
  });

  it("renders malformed marketplace token and cost values as dashes without changing valid zero", () => {
    render(<UsageReference offers={[
      makeOffer("byok", [usage("selected_token", 10, {
        token_units: {
          input: -1,
          cache_read: Number.NaN,
          output: 0,
          cache_write: Number.POSITIVE_INFINITY,
          total: 10,
        },
        reference_cost: -1,
        gateway_charge_cost: 0,
        estimated_total_cost: Number.POSITIVE_INFINITY,
      })]),
    ]} usageStatus="available" />);

    const card = screen.getByRole("article", { name: "usageScope.selected_token" });
    const buckets = Object.fromEntries(within(card).getAllByTestId("usage-token-bucket").map((node) => [
      node.getAttribute("data-token-key"),
      node,
    ]));
    expect(buckets.input).toHaveTextContent("—");
    expect(buckets.cache_read).toHaveTextContent("—");
    expect(buckets.output).toHaveTextContent("0");
    expect(buckets.cache_write).toHaveTextContent("—");
    expect(buckets.total).toHaveTextContent("10");
    expect(card.querySelector("[data-cost=upstream]")).toHaveTextContent("—");
    expect(card.querySelector("[data-cost=gateway]")).toHaveTextContent("$ 0.00");
    expect(card.querySelector("[data-cost=estimated]")).toHaveTextContent("—");
  });

  it("shows an explicit unavailable state without synthesizing zero usage", () => {
    render(<UsageReference offers={[makeOffer("byok", [])]} usageStatus="unavailable" />);

    expect(screen.getByRole("alert")).toHaveTextContent("usageUnavailableTitle");
    expect(screen.queryByText("totalTokenUnits")).not.toBeInTheDocument();
    expect(screen.queryByText("0")).not.toBeInTheDocument();
  });
});
