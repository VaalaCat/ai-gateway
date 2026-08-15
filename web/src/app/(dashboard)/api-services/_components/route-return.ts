import { isRouteSlug } from "./route-slug";

export interface RouteReturnContext {
  id: number;
  slug: string;
}

function isRouteReturnContext(route: RouteReturnContext | undefined): route is RouteReturnContext {
  return route !== undefined && Number.isSafeInteger(route.id) && route.id > 0 && isRouteSlug(route.slug);
}

export function routeReturnContext(params: Pick<URLSearchParams, "get">): RouteReturnContext | undefined {
  try {
    const route = { id: Number(params.get("route_id")), slug: params.get("route_slug") ?? "" };
    return isRouteReturnContext(route) ? route : undefined;
  } catch {
    return undefined;
  }
}

export function serviceDetailReturnPath(serviceID: number, route?: RouteReturnContext) {
  if (!isRouteReturnContext(route)) return `/api-services/detail?id=${serviceID}`;
  return `/api-services/detail?id=${serviceID}&route_search=${encodeURIComponent(route.slug)}&route=${route.id}`;
}

export function routeReturnQuery(route?: RouteReturnContext) {
  if (!isRouteReturnContext(route)) return "";
  return new URLSearchParams({ route_id: String(route.id), route_slug: route.slug }).toString();
}
