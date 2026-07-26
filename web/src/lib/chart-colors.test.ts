import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

import { chartColorForSeries, chartDashForSeries, OTHER_SERIES_COLOR } from "./chart-colors";

type Oklch = { l: number; c: number; h: number };

const globalsCss = readFileSync(`${process.cwd()}/src/app/globals.css`, "utf8");

function themeBlock(selector: ":root" | ".dark"): string {
  const block = globalsCss.match(new RegExp(`\\${selector}\\s*\\{([\\s\\S]*?)\\n\\}`))?.[1];
  if (!block) throw new Error(`missing ${selector} color block`);
  return block;
}

function readOklch(block: string, variable: string): Oklch {
  const value = block.match(new RegExp(`--${variable}:\\s*oklch\\(([^)]+)\\)`))?.[1];
  if (!value) throw new Error(`missing --${variable}`);
  const [l, c, h] = value.trim().split(/\s+/).map(Number);
  return { l, c, h };
}

function relativeLuminance({ l, c, h }: Oklch): number {
  const radians = h * Math.PI / 180;
  const a = c * Math.cos(radians);
  const b = c * Math.sin(radians);
  const lPrime = l + 0.3963377774 * a + 0.2158037573 * b;
  const mPrime = l - 0.1055613458 * a - 0.0638541728 * b;
  const sPrime = l - 0.0894841775 * a - 1.291485548 * b;
  const [ll, m, s] = [lPrime ** 3, mPrime ** 3, sPrime ** 3];
  const clamp = (value: number) => Math.min(1, Math.max(0, value));
  const red = clamp(4.0767416621 * ll - 3.3077115913 * m + 0.2309699292 * s);
  const green = clamp(-1.2684380046 * ll + 2.6097574011 * m - 0.3413193965 * s);
  const blue = clamp(-0.0041960863 * ll - 0.7034186147 * m + 1.707614701 * s);
  return 0.2126 * red + 0.7152 * green + 0.0722 * blue;
}

function contrastRatio(first: Oklch, second: Oklch): number {
  const [lighter, darker] = [relativeLuminance(first), relativeLuminance(second)].sort((a, b) => b - a);
  return (lighter + 0.05) / (darker + 0.05);
}

function hueFamily(hue: number): string {
  if (hue >= 170 && hue <= 205) return "teal";
  if (hue >= 230 && hue <= 270) return "blue";
  if (hue >= 65 && hue <= 95) return "amber";
  if (hue >= 350 || hue <= 25) return "rose";
  if (hue >= 125 && hue <= 155) return "green";
  return "other";
}

describe("chartColorForSeries", () => {
  it("maps a series name to the same existing CSS color regardless of ordering", () => {
    const names = ["gpt-5.6-sol", "claude-opus-4", "gemini-2.5-pro"];
    const forward = new Map(names.map((name) => [name, chartColorForSeries(name)]));
    const reverse = new Map([...names].reverse().map((name) => [name, chartColorForSeries(name)]));

    for (const name of names) {
      expect(reverse.get(name)).toBe(forward.get(name));
      expect(forward.get(name)).toMatch(/^var\(--chart-[1-5]\)$/);
    }
  });

  it("keeps only the exact backend aggregate identity neutral", () => {
    expect(chartColorForSeries("others")).toBe(OTHER_SERIES_COLOR);
    for (const entityName of ["other", "Other", "Others", "其他", " others "]) {
      expect(chartColorForSeries(entityName)).not.toBe(OTHER_SERIES_COLOR);
    }
  });

  it("handles empty, Chinese, RTL, and long unbroken names deterministically", () => {
    for (const name of ["", "中文模型", "نموذج", "x".repeat(512)]) {
      expect(chartColorForSeries(name)).toBe(chartColorForSeries(name));
      expect(chartColorForSeries(name)).toMatch(/^var\(--chart-[1-5]\)$/);
    }
  });

  it("uses a finite multi-token palette across 20 series", () => {
    const colors = new Set(Array.from({ length: 20 }, (_, index) => chartColorForSeries(`series-${index}`)));
    expect(colors.size).toBeGreaterThanOrEqual(4);
    expect(colors.size).toBeLessThanOrEqual(5);
  });

  it.each([":root", ".dark"] as const)("keeps every %s chart token distinguishable from card", (selector) => {
    const block = themeBlock(selector);
    const card = readOklch(block, "card");
    for (let index = 1; index <= 5; index += 1) {
      expect(contrastRatio(readOklch(block, `chart-${index}`), card)).toBeGreaterThanOrEqual(3);
    }
  });

  it.each([":root", ".dark"] as const)("keeps %s palette across five semantic hue families", (selector) => {
    const block = themeBlock(selector);
    const families = Array.from({ length: 5 }, (_, index) =>
      hueFamily(readOklch(block, `chart-${index + 1}`).h),
    );
    expect(families).toEqual(["teal", "blue", "amber", "rose", "green"]);
  });
});

describe("chartDashForSeries", () => {
  it("uses the aggregate dash only for exact lowercase others", () => {
    expect(chartDashForSeries("others")).toBe("3 3");
    for (const entityName of ["other", "Other", "Others", "其他", " others "]) {
      expect(chartDashForSeries(entityName)).not.toBe("3 3");
    }
  });

  it("is stable across ordering and distinguishes at least one colliding color pair", () => {
    const names = Array.from({ length: 20 }, (_, index) => `series-${index}`);
    for (const name of names) expect(chartDashForSeries(name)).toBe(chartDashForSeries(name));
    const pair = names.flatMap((name, index) => names.slice(index + 1).map((other) => [name, other] as const))
      .find(([a, b]) => chartColorForSeries(a) === chartColorForSeries(b) && chartDashForSeries(a) !== chartDashForSeries(b));
    expect(pair).toBeDefined();
  });
});
