import type { APICatalogRoute, APICatalogService } from "@/lib/api/api-access";

export type CatalogProtocol = "http" | "websocket";

export interface CatalogSelection {
  serviceID?: number;
  routeID?: number;
  protocol?: CatalogProtocol;
  path?: string;
  method?: string;
}

export interface CatalogIDPick {
  id?: number;
  pending: boolean;
}

function parseID(value: string | null) {
  if (!value || !/^[1-9]\d*$/.test(value)) return undefined;
  const id = Number(value);
  return Number.isSafeInteger(id) ? id : undefined;
}

export function parseCatalogSelection(params: URLSearchParams): CatalogSelection {
  const serviceID = parseID(params.get("service_id"));
  const routeID = parseID(params.get("route_id"));
  const requestedProtocol = params.get("protocol");
  const protocol = requestedProtocol === "http" || requestedProtocol === "websocket"
    ? requestedProtocol
    : undefined;
  const requestedPath = params.get("path");
  const path = requestedPath && requestedPath.startsWith("/") ? requestedPath : undefined;
  const requestedMethod = params.get("method")?.toLowerCase();
  const method = requestedMethod && ["get", "post", "put", "patch", "delete", "head", "options", "trace"].includes(requestedMethod)
    ? requestedMethod
    : undefined;

  return {
    ...(serviceID === undefined ? {} : { serviceID }),
    ...(routeID === undefined ? {} : { routeID }),
    ...(protocol === undefined ? {} : { protocol }),
    ...(path === undefined ? {} : { path }),
    ...(method === undefined ? {} : { method }),
  };
}

export function pickCatalogID<T extends { id: number }>(
  requestedID: number | undefined,
  items: T[],
  exhausted: boolean,
): CatalogIDPick {
  if (requestedID === undefined) return { id: items[0]?.id, pending: false };
  const requested = items.find((item) => item.id === requestedID);
  if (requested) return { id: requested.id, pending: false };
  return exhausted ? { id: items[0]?.id, pending: false } : { pending: true };
}

export function normalizeServiceID(requestedID: number | undefined, services: APICatalogService[]) {
  return services.find((service) => service.id === requestedID)?.id ?? services[0]?.id;
}

export function normalizeRouteID(requestedID: number | undefined, routes: APICatalogRoute[]) {
  return routes.find((route) => route.id === requestedID)?.id ?? routes[0]?.id;
}

export function normalizeProtocol(requested: CatalogProtocol | undefined, route: APICatalogRoute | undefined) {
  if (!route) return undefined;
  return requested && route.protocols.includes(requested) ? requested : route.protocols[0];
}
