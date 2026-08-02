import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import en from "@/i18n/en.json";
import zh from "@/i18n/zh.json";
import { MarketplacePriceGrid } from "./marketplace-price-grid";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

describe("marketplace price grid", () => {
  it("projects values in billing order and visually mutes cache buckets", () => {
    render(
      <MarketplacePriceGrid
        prices={{ input: 1, cache_read: 0.25, output: 8, cache_write: 2 }}
        mode="values"
      />,
    );

    const items = screen.getAllByTestId("marketplace-price-item");
    expect(items.map((item) => item.getAttribute("data-price-key"))).toEqual([
      "input",
      "cache_read",
      "output",
      "cache_write",
    ]);
    expect(items[1]).toHaveClass("text-muted-foreground");
    expect(items[3]).toHaveClass("text-muted-foreground");
    expect(screen.getByText("$0.25")).toBeInTheDocument();
  });

  it("formats range, equal, and empty buckets without changing their positions", () => {
    render(
      <MarketplacePriceGrid
        prices={{
          input: [1, 2],
          cache_read: [0.25, 0.25],
          output: [],
          cache_write: [0.00001, 0.00002],
        }}
        mode="range"
      />,
    );

    const items = screen.getAllByTestId("marketplace-price-item");
    expect(items[0]).toHaveTextContent("$1.00 – $2.00");
    expect(items[1]).toHaveTextContent("$0.25");
    expect(items[2]).toHaveTextContent("—");
    expect(items[3]).toHaveTextContent("$0.000010 – $0.000020");
  });

  it("contains every price bucket label in both locales", () => {
    const lookup = (messages: typeof en, key: string) => key.split(".").reduce<unknown>(
      (value, segment) => typeof value === "object" && value !== null
        ? (value as Record<string, unknown>)[segment]
        : undefined,
      messages.modelMarketplace,
    );

    for (const key of [
      "priceBucket.input",
      "priceBucket.cache_read",
      "priceBucket.output",
      "priceBucket.cache_write",
    ]) {
      expect(lookup(en, key), `en:${key}`).toEqual(expect.any(String));
      expect(lookup(zh as typeof en, key), `zh:${key}`).toEqual(expect.any(String));
    }
  });
});
