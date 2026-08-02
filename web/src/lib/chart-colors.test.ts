import ColorHash from "color-hash";
import { describe, expect, it } from "vitest";

import { chartColorForSeries, OTHER_SERIES_COLOR } from "./chart-colors";

const colorHash = new ColorHash();

describe("chartColorForSeries", () => {
  it("uses color-hash for stable series colors regardless of ordering", () => {
    const names = ["gpt-5.6-sol", "claude-opus-4", "gemini-2.5-pro"];
    const forward = new Map(names.map((name) => [name, chartColorForSeries(name)]));
    const reverse = new Map([...names].reverse().map((name) => [name, chartColorForSeries(name)]));

    for (const name of names) {
      expect(forward.get(name)).toBe(colorHash.hex(name));
      expect(reverse.get(name)).toBe(forward.get(name));
    }
  });

  it("keeps only the exact backend aggregate identity neutral", () => {
    expect(chartColorForSeries("others")).toBe(OTHER_SERIES_COLOR);
    for (const name of ["other", "Other", "Others", "其他", " others "]) {
      expect(chartColorForSeries(name)).toBe(colorHash.hex(name));
      expect(chartColorForSeries(name)).not.toBe(OTHER_SERIES_COLOR);
    }
  });

  it("delegates empty, Chinese, RTL, and long names to color-hash", () => {
    for (const name of ["", "中文模型", "نموذج", "x".repeat(512)]) {
      expect(chartColorForSeries(name)).toBe(colorHash.hex(name));
    }
  });
});
