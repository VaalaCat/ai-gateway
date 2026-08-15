export interface CatalogTokenInvalidationGuard {
  scopeIdentity: string;
  invalidated: boolean;
}

export const initialCatalogTokenInvalidationGuard: CatalogTokenInvalidationGuard = {
  scopeIdentity: "",
  invalidated: false,
};

export function transitionCatalogTokenInvalidationGuard(
  previous: CatalogTokenInvalidationGuard,
  scopeIdentity: string,
  hasTokenUnavailableFailure: boolean,
) {
  const guard = previous.scopeIdentity === scopeIdentity
    ? previous
    : { scopeIdentity, invalidated: false };
  if (!hasTokenUnavailableFailure || guard.invalidated) {
    return { guard, shouldInvalidate: false };
  }
  return {
    guard: { ...guard, invalidated: true },
    shouldInvalidate: true,
  };
}
