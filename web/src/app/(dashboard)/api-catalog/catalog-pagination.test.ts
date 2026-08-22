import { describe, expect, it } from "vitest";

import { catalogPageExhausted, MAX_CATALOG_PAGE } from "./_components/catalog-pagination";

describe("catalogPageExhausted", () => {
  it("keeps a page open while a non-empty response adds new identities below total", () => {
    expect(catalogPageExhausted({ page: 2, pageItemCount: 1, previousLoadedCount: 50, loadedCount: 51, total: 100 })).toBe(false);
  });

  it("stops when a non-empty later page merges to no new identity", () => {
    expect(catalogPageExhausted({ page: 2, pageItemCount: 50, previousLoadedCount: 50, loadedCount: 50, total: 100 })).toBe(true);
  });

  it("stops on an empty page or when the reported total is reached", () => {
    expect(catalogPageExhausted({ page: 2, pageItemCount: 0, previousLoadedCount: 50, loadedCount: 50, total: 100 })).toBe(true);
    expect(catalogPageExhausted({ page: 2, pageItemCount: 1, previousLoadedCount: 50, loadedCount: 51, total: 51 })).toBe(true);
  });

  it("enforces a finite maximum page even when every response claims more stale total", () => {
    expect(catalogPageExhausted({ page: MAX_CATALOG_PAGE, pageItemCount: 1, previousLoadedCount: 998, loadedCount: 999, total: 10_000 })).toBe(true);
  });
});
