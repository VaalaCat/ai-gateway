import { describe, expect, it } from "vitest";
import {
  formatPercentAxis,
  formatPercentValue,
  formatPriceValue,
  formatTpsAxis,
  formatTpsValue,
  formatMoneyCompact,
  formatTokensCompact,
  formatTokensExact,
} from "./format";

describe("marketplace unit formatters", () => {
  it.each([
    [0, "$0.00"],
    [2, "$2.00"],
    [0.0042, "$0.0042"],
    [0.000042, "$0.000042"],
    [Number.NaN, "—"],
    [Number.POSITIVE_INFINITY, "—"],
  ])("formats raw USD-per-million price %s", (value, expected) => {
    expect(formatPriceValue(value)).toBe(expected);
  });

  it("formats an already-percent success rate without multiplying it again", () => {
    expect(formatPercentValue(99.982)).toBe("99.98%");
    expect(formatPercentAxis(99.982)).toBe("100%");
  });

  it.each([-0.01, Number.NaN, Number.POSITIVE_INFINITY])(
    "renders invalid raw USD-per-million price %s as unavailable",
    (value) => {
      expect(formatPriceValue(value)).toBe("—");
    },
  );

  it.each([-0.01, Number.NaN, Number.POSITIVE_INFINITY])(
    "renders invalid percentage value %s as unavailable",
    (value) => {
      expect(formatPercentValue(value)).toBe("—");
    },
  );

  it.each([-0.01, Number.NaN, Number.POSITIVE_INFINITY])(
    "renders invalid percentage axis value %s as unavailable",
    (value) => {
      expect(formatPercentAxis(value)).toBe("—");
    },
  );

  it.each([-0.01, Number.NaN, Number.POSITIVE_INFINITY])(
    "renders invalid TPS value %s as unavailable",
    (value) => {
      expect(formatTpsValue(value)).toBe("—");
    },
  );

  it.each([-0.01, Number.NaN, Number.POSITIVE_INFINITY])(
    "renders invalid TPS axis value %s as unavailable",
    (value) => {
      expect(formatTpsAxis(value)).toBe("—");
    },
  );

  it("accepts 100 percent but rejects values above the percentage range", () => {
    expect(formatPercentValue(100)).toBe("100.00%");
    expect(formatPercentAxis(100)).toBe("100%");
    expect(formatPercentValue(100.001)).toBe("—");
    expect(formatPercentAxis(101)).toBe("—");
  });

  it("distinguishes TPS axis and precise value units", () => {
    expect(formatTpsAxis(82.44)).toBe("82");
    expect(formatTpsValue(82.44)).toBe("82.4 tok/s");
  });

  it("keeps compact token thresholds and exact token values separate", () => {
    expect(formatTokensCompact(999)).toBe("999");
    expect(formatTokensCompact(1_000)).toBe("1.00K");
    expect(formatTokensCompact(1_000_000)).toBe("1.00M");
    expect(formatTokensExact(1_047_576)).toBe("1,047,576");
  });

  it("keeps raw model prices separate from quota-denominated money", () => {
    expect(formatPriceValue(2)).toBe("$2.00");
    expect(formatMoneyCompact(100_000)).toBe("$ 1.00");
  });
});
