export interface CatalogScopeQuery {
  error: unknown;
  retry: () => unknown;
}

export type CatalogScopeFailureKind = "token_not_available" | "access_unavailable";

export interface CatalogScopeFailure {
  kind: CatalogScopeFailureKind;
  retry: () => void;
}

function statusOf(error: unknown) {
  return typeof error === "object" && error !== null && "status" in error
    ? (error as { status?: number }).status
    : undefined;
}

function codeOf(error: unknown) {
  if (typeof error !== "object" || error === null || !("body" in error)) return undefined;
  const body = (error as { body?: unknown }).body;
  return typeof body === "object" && body !== null && "code" in body
    ? String((body as { code?: unknown }).code)
    : undefined;
}

export function findCatalogScopeFailure(queries: CatalogScopeQuery[]): CatalogScopeFailure | undefined {
  const failedQueries = queries.filter(({ error }) =>
    codeOf(error) === "token_not_available"
    || codeOf(error) === "catalog_access_unavailable"
    || statusOf(error) === 503,
  );
  if (failedQueries.length === 0) return undefined;

  return {
    kind: failedQueries.some(({ error }) => codeOf(error) === "token_not_available")
      ? "token_not_available"
      : "access_unavailable",
    retry: () => { for (const query of failedQueries) void query.retry(); },
  };
}
