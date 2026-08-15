export type CatalogAccessScope =
  | { mode: "admin-all" }
  | { mode: "token"; tokenID: number }
  | { mode: "required" };

const validTokenID = (tokenID: number) => Number.isSafeInteger(tokenID) && tokenID > 0;

export function parseCatalogTokenID(value: string | null): number {
  if (!value || !/^\d+$/.test(value)) return 0;
  const tokenID = Number(value);
  return validTokenID(tokenID) ? tokenID : 0;
}

export function toCatalogAccessScope(isAdmin: boolean, tokenID: number): CatalogAccessScope {
  if (validTokenID(tokenID)) return { mode: "token", tokenID };
  return isAdmin ? { mode: "admin-all" } : { mode: "required" };
}

export function catalogTokenID(scope: CatalogAccessScope): number | undefined {
  return scope.mode === "token" ? scope.tokenID : undefined;
}

export function catalogQueriesEnabled(scope: CatalogAccessScope): boolean {
  return scope.mode !== "required";
}

export function catalogScopeKey(scope: CatalogAccessScope): readonly [string, number] {
  switch (scope.mode) {
    case "admin-all": return ["admin-all", 0];
    case "token": return ["token", scope.tokenID];
    case "required": return ["required", 0];
  }
}
