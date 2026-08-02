import ColorHash from "color-hash";

export const OTHER_SERIES_COLOR = "var(--muted-foreground)";
export const CHART_LINE_ACTIVE_DOT = { r: 4, strokeWidth: 2 } as const;

const colorHash = new ColorHash();

export function chartColorForSeries(name: string): string {
  if (name === "others") return OTHER_SERIES_COLOR;
  return colorHash.hex(name);
}
