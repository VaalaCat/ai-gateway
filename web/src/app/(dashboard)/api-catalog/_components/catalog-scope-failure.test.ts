import { describe, expect, it, vi } from "vitest";

import { findCatalogScopeFailure } from "./catalog-scope-failure";

describe("findCatalogScopeFailure", () => {
  it("collects every active catalog access failure and retries only those queries", () => {
    const retryServices = vi.fn();
    const retryRoutes = vi.fn();
    const retrySearch = vi.fn();

    const failure = findCatalogScopeFailure([
      { error: { status: 503, body: { code: "catalog_access_unavailable" } }, retry: retryServices },
      { error: undefined, retry: retryRoutes },
      { error: { status: 503 }, retry: retrySearch },
    ]);

    expect(failure?.kind).toBe("access_unavailable");
    failure?.retry();
    expect(retryServices).toHaveBeenCalledOnce();
    expect(retryRoutes).not.toHaveBeenCalled();
    expect(retrySearch).toHaveBeenCalledOnce();
  });

  it("prioritizes token_not_available from an active Route query", () => {
    const retryRoute = vi.fn();
    const retryEffective = vi.fn();

    const failure = findCatalogScopeFailure([
      { error: { status: 404, body: { code: "token_not_available" } }, retry: retryRoute },
      { error: { status: 503, body: { code: "catalog_access_unavailable" } }, retry: retryEffective },
    ]);

    expect(failure?.kind).toBe("token_not_available");
    failure?.retry();
    expect(retryRoute).toHaveBeenCalledOnce();
    expect(retryEffective).toHaveBeenCalledOnce();
  });

  it("recognizes an Effective or active Search catalog access error", () => {
    const retryEffective = vi.fn();
    const retrySearch = vi.fn();

    const failure = findCatalogScopeFailure([
      { error: { status: 503 }, retry: retryEffective },
      { error: { body: { code: "catalog_access_unavailable" } }, retry: retrySearch },
    ]);

    expect(failure?.kind).toBe("access_unavailable");
    failure?.retry();
    expect(retryEffective).toHaveBeenCalledOnce();
    expect(retrySearch).toHaveBeenCalledOnce();
  });
});
