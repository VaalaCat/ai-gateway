import { describe, expect, it } from "vitest";

import { applySevenDayDefaultRange } from "./observability-range";

describe("applySevenDayDefaultRange", () => {
  const end = 1_700_604_800;

  it("applies a seven-day window when start is not explicit", () => {
    expect(
      applySevenDayDefaultRange(
        { start: end - 86_400, end, gran: "day" },
        false,
      ),
    ).toEqual({ start: end - 7 * 86_400, end, gran: "day" });
  });

  it("preserves an explicit single-day range", () => {
    const range = { start: end - 86_399, end, gran: "day" as const };
    expect(applySevenDayDefaultRange(range, true)).toBe(range);
  });

  it("preserves an explicit range at the exact 24-hour boundary", () => {
    const range = { start: end - 86_400, end, gran: "day" as const };
    expect(applySevenDayDefaultRange(range, true)).toBe(range);
  });

  it("preserves an explicit long range", () => {
    const range = { start: end - 30 * 86_400, end, gran: "day" as const };
    expect(applySevenDayDefaultRange(range, true)).toBe(range);
  });
});
