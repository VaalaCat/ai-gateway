export const MAX_CATALOG_PAGE = 1_000;

export function catalogPageExhausted({
  page,
  pageItemCount,
  previousLoadedCount,
  loadedCount,
  total,
}: {
  page: number;
  pageItemCount: number;
  previousLoadedCount: number;
  loadedCount: number;
  total: number;
}) {
  return page >= MAX_CATALOG_PAGE
    || pageItemCount === 0
    || loadedCount >= total
    || (page > 1 && loadedCount <= previousLoadedCount);
}
