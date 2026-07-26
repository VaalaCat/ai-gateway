export const OTHER_SERIES_COLOR = "var(--muted-foreground)";

const SERIES_COLORS = [
  "var(--chart-1)",
  "var(--chart-2)",
  "var(--chart-3)",
  "var(--chart-4)",
  "var(--chart-5)",
] as const;

const SERIES_DASHES = [undefined, "7 3", "2 3", "7 3 2 3"] as const;

function stableSeriesHash(name: string): number {
  let hash = 2166136261;
  for (const character of name) {
    hash ^= character.codePointAt(0) ?? 0;
    hash = Math.imul(hash, 16777619);
  }
  return hash >>> 0;
}

export function chartColorForSeries(name: string): string {
  if (name === "others") {
    return OTHER_SERIES_COLOR;
  }
  return SERIES_COLORS[stableSeriesHash(name) % SERIES_COLORS.length];
}

/** 同名系列跨图表保持同一线型；在基础色碰撞时提供第二视觉编码。 */
export function chartDashForSeries(name: string): string | undefined {
  if (name === "others") return "3 3";
  return SERIES_DASHES[Math.floor(stableSeriesHash(name) / SERIES_COLORS.length) % SERIES_DASHES.length];
}
