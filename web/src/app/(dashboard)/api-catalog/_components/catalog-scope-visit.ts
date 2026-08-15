export interface CatalogScopeVisit {
  tokenID: number;
  epoch: number;
}

export function transitionCatalogScopeVisit(
  current: CatalogScopeVisit,
  nextTokenID: number,
) {
  if (current.tokenID === nextTokenID) {
    return { visit: current, changed: false };
  }
  return {
    visit: { tokenID: nextTokenID, epoch: current.epoch + 1 },
    changed: true,
  };
}

export function catalogScopeVisitIdentity(scopeKey: string, visit: CatalogScopeVisit) {
  return `${scopeKey}:${visit.epoch}`;
}
