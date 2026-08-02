export function replaceCurrentPageSearch(targetSearch: string) {
  const target = new URL(window.location.href);
  target.search = targetSearch;
  window.history.replaceState(
    null,
    "",
    `${target.pathname}${target.search}${target.hash}`,
  );
}
