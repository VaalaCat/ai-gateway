import { describe, expect, it } from "vitest";

import {
  catalogScopeVisitIdentity,
  transitionCatalogScopeVisit,
} from "./catalog-scope-visit";

describe("transitionCatalogScopeVisit", () => {
  it("preserves a visit when storage reports the selected Token again", () => {
    const current = { tokenID: 17, epoch: 4 };
    const transition = transitionCatalogScopeVisit(current, 17);

    expect(transition.changed).toBe(false);
    expect(transition.visit).toEqual(current);
    expect(catalogScopeVisitIdentity("token:17", transition.visit)).toBe("token:17:4");
  });

  it("uses new identities when storage and picker move A to B and back to A", () => {
    const storageA = { tokenID: 17, epoch: 4 };
    const pickerB = transitionCatalogScopeVisit(storageA, 18);
    const pickerA = transitionCatalogScopeVisit(pickerB.visit, 17);

    expect(pickerB.changed).toBe(true);
    expect(pickerA.changed).toBe(true);
    expect(catalogScopeVisitIdentity("token:17", pickerA.visit)).not.toBe(
      catalogScopeVisitIdentity("token:17", storageA),
    );
  });
});
