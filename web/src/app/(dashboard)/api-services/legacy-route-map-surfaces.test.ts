import { existsSync } from "node:fs";

import { describe, expect, it } from "vitest";

const legacyPresentationModules = [
  "./_components/route-map/route-card.tsx",
  "./_components/route-map/target-route-group.tsx",
  "./_components/route-map/endpoint-list.tsx",
  "./_components/route-map/route-map.tsx",
  "./_components/route-map/route-map-model.ts",
  "./_components/route-map/route-quick-preview.tsx",
  "./_components/route-map/segmented-url.tsx",
  "./_components/route-routing-sheet.tsx",
] as const;

describe("legacy Route Map surfaces", () => {
  it.each(legacyPresentationModules)("does not retain the obsolete presentation module %s", (modulePath) => {
    expect(existsSync(new URL(modulePath, import.meta.url))).toBe(false);
  });

  it("keeps route preview URL helpers under their behavior-oriented module", async () => {
    const previewURL = await import("./_components/route-table/route-preview-url");

    expect(previewURL.buildPublicSegmentedURL).toBeTypeOf("function");
    expect(previewURL.buildEndpointSegmentedURL).toBeTypeOf("function");
    expect(previewURL.buildRoutePreviewViewModel).toBeTypeOf("function");
  });
});
