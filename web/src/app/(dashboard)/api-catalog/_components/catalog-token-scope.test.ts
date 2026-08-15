import { describe, expect, it } from "vitest";

import {
  catalogQueriesEnabled,
  catalogScopeKey,
  catalogTokenID,
  parseCatalogTokenID,
  toCatalogAccessScope,
} from "./catalog-token-scope";

describe("catalog token scope", () => {
  it("maps admin-all to enabled requests without token_id", () => {
    const scope = toCatalogAccessScope(true, 0);

    expect(scope).toEqual({ mode: "admin-all" });
    expect(catalogQueriesEnabled(scope)).toBe(true);
    expect(catalogTokenID(scope)).toBeUndefined();
    expect(catalogScopeKey(scope)).toEqual(["admin-all", 0]);
  });

  it("maps a positive token to a stable token scope key", () => {
    const scope = toCatalogAccessScope(false, 17);

    expect(scope).toEqual({ mode: "token", tokenID: 17 });
    expect(catalogQueriesEnabled(scope)).toBe(true);
    expect(catalogTokenID(scope)).toBe(17);
    expect(catalogScopeKey(scope)).toEqual(["token", 17]);
  });

  it("keeps required scope disabled", () => {
    const scope = toCatalogAccessScope(false, 0);

    expect(scope).toEqual({ mode: "required" });
    expect(catalogQueriesEnabled(scope)).toBe(false);
    expect(catalogTokenID(scope)).toBeUndefined();
    expect(catalogScopeKey(scope)).toEqual(["required", 0]);
  });

  it("rejects zero, negative and unsafe token ids as required", () => {
    expect(toCatalogAccessScope(false, 0)).toEqual({ mode: "required" });
    expect(toCatalogAccessScope(false, -1)).toEqual({ mode: "required" });
    expect(toCatalogAccessScope(false, Number.MAX_SAFE_INTEGER + 1)).toEqual({ mode: "required" });
  });

  it("parses only positive safe remembered Token ids", () => {
    expect(parseCatalogTokenID("17")).toBe(17);
    expect(parseCatalogTokenID("0")).toBe(0);
    expect(parseCatalogTokenID("-1")).toBe(0);
    expect(parseCatalogTokenID(String(Number.MAX_SAFE_INTEGER + 1))).toBe(0);
  });
});
