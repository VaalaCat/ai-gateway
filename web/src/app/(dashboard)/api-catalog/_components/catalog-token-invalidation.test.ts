import { describe, expect, it } from "vitest";

import {
  initialCatalogTokenInvalidationGuard,
  transitionCatalogTokenInvalidationGuard,
} from "./catalog-token-invalidation";

describe("transitionCatalogTokenInvalidationGuard", () => {
  it("handles one failing Token scope only once", () => {
    const first = transitionCatalogTokenInvalidationGuard(
      initialCatalogTokenInvalidationGuard,
      "token:5",
      true,
    );
    const replay = transitionCatalogTokenInvalidationGuard(first.guard, "token:5", true);

    expect(first.shouldInvalidate).toBe(true);
    expect(replay.shouldInvalidate).toBe(false);
  });

  it("resets after moving into a different non-failing scope", () => {
    const failed = transitionCatalogTokenInvalidationGuard(
      initialCatalogTokenInvalidationGuard,
      "token:5",
      true,
    );
    const reset = transitionCatalogTokenInvalidationGuard(failed.guard, "required:0", false);

    expect(reset.shouldInvalidate).toBe(false);
    expect(reset.guard).toEqual({ scopeIdentity: "required:0", invalidated: false });
  });

  it("handles the same Token again after leaving and re-entering its scope", () => {
    const failed = transitionCatalogTokenInvalidationGuard(
      initialCatalogTokenInvalidationGuard,
      "token:5",
      true,
    );
    const reset = transitionCatalogTokenInvalidationGuard(failed.guard, "admin-all:0", false);
    const reentered = transitionCatalogTokenInvalidationGuard(reset.guard, "token:5", true);

    expect(reentered.shouldInvalidate).toBe(true);
  });
});
