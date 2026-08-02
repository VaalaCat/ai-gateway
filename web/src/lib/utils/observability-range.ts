import type { ObsRange } from "@/lib/types/observability";

const HOUR_MAX_WINDOW = 7 * 86_400;

export function applySevenDayDefaultRange(
  range: ObsRange,
  hasExplicitStart: boolean,
): ObsRange {
  if (hasExplicitStart) return range;
  return { ...range, start: range.end - HOUR_MAX_WINDOW };
}

export function switchLongRangeToDay(
  range: ObsRange,
): { range: ObsRange; adjusted: boolean } {
  if (range.gran !== "hour" || range.end - range.start <= HOUR_MAX_WINDOW) {
    return { range, adjusted: false };
  }
  return { range: { ...range, gran: "day" }, adjusted: true };
}
