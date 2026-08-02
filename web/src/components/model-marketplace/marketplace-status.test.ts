import { describe, expect, it } from "vitest";

import {
  MARKETPLACE_STATUS_PRESENTATION,
  marketplaceOfferStatus,
} from "./marketplace-status";

describe("marketplace status presentation", () => {
  it.each([
    ["operational", "emerald"],
    ["degraded", "amber"],
    ["outage", "red"],
    ["unknown", "gray"],
    ["stale", "gray"],
    ["unavailable", "red"],
    ["in_progress", "amber"],
  ] as const)("maps %s tracker, badge, and dot to %s", (status, color) => {
    const presentation = MARKETPLACE_STATUS_PRESENTATION[status];
    expect(presentation.tracker).toContain(color);
    expect(presentation.badge).toContain(color);
    expect(presentation.dot).toContain(color);
  });

  it("defines in-progress as an amber inset indicator rather than a fill state", () => {
    expect(MARKETPLACE_STATUS_PRESENTATION.in_progress.tracker)
      .toContain("ring-amber-500/80");
    expect(MARKETPLACE_STATUS_PRESENTATION.in_progress.tracker)
      .toContain("ring-inset");
  });

  it.each([
    ["unavailable", "operational", "unavailable"],
    ["stale", "outage", "stale"],
    ["available", "degraded", "degraded"],
  ] as const)("prioritizes %s availability over the recorded health", (performanceStatus, health, expected) => {
    expect(marketplaceOfferStatus({
      performance_status: performanceStatus,
      performance: { status: health },
    } as Parameters<typeof marketplaceOfferStatus>[0])).toBe(expected);
  });
});
