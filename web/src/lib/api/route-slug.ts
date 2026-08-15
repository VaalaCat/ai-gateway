const routeSlugPattern = /^[a-z0-9][a-z0-9._~-]{0,63}$/;

export function isRouteSlug(slug: string) {
  return routeSlugPattern.test(slug);
}
