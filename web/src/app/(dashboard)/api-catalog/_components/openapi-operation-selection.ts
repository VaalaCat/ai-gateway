import type { APICatalogRoute } from "@/lib/api/api-access";
import { encodeInvocationPathSegment } from "../../api-services/_components/invocation-command";
import { resolveOpenAPIObject } from "./openapi-operation-contract";

const HTTP_METHODS = new Set([
  "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE",
]);

export interface OpenAPIInvocationValues {
  [name: string]: string | number | boolean | null | undefined | string[] | Record<string, string | string[]>;
  query?: Record<string, string | string[]>;
  headers?: Record<string, string>;
}

export interface OpenAPIPathItem {
  parameters?: unknown[];
  [key: string]: unknown;
}

export interface OpenAPIOperation {
  routeID: number;
  routeSlug: string;
  path: string;
  method: string;
  operation: Record<string, unknown>;
  pathItem: OpenAPIPathItem;
}

export interface OpenAPIOperationIdentity {
  routeID?: number;
  path?: string;
  method?: string;
}

function record(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? value as Record<string, unknown>
    : undefined;
}

function routeSlugForPath(item: Record<string, unknown>, routes: APICatalogRoute[]) {
  const slug = item["x-ai-gateway-route-slug"];
  return typeof slug === "string" && routes.some((route) => route.slug === slug) ? slug : undefined;
}

function hasReferenceSiblings(item: Record<string, unknown>, allowRouteSlug: boolean) {
  return Object.keys(item).some((key) => (
    key !== "$ref" && (!allowRouteSlug || key !== "x-ai-gateway-route-slug")
  ));
}

function resolveOpenAPIPathItem(
  item: Record<string, unknown>,
  components: Record<string, unknown>,
) {
  let current = item;
  let root = true;
  const visited = new Set<string>();
  while (Object.hasOwn(current, "$ref")) {
    const reference = current.$ref;
    if (typeof reference !== "string" || hasReferenceSiblings(current, root) || visited.has(reference)) {
      return undefined;
    }
    visited.add(reference);
    const resolved = resolveOpenAPIObject(current, components);
    if (!resolved.resolved || resolved.recursive) return undefined;
    current = resolved.value;
    root = false;
  }
  return current;
}

export function listOpenAPIOperations(document: unknown, routes: APICatalogRoute[]): OpenAPIOperation[] {
  const documentObject = record(document);
  const paths = record(documentObject?.paths);
  if (!paths) return [];

  const components = record(documentObject?.components) ?? {};

  const routeBySlug = new Map(routes.map((route) => [route.slug, route]));
  return Object.entries(paths)
    .flatMap(([path, rawItem]) => {
      const sourceItem = record(rawItem);
      if (!sourceItem) return [];
      const routeSlug = routeSlugForPath(sourceItem, routes);
      const route = routeSlug === undefined ? undefined : routeBySlug.get(routeSlug);
      if (routeSlug === undefined || !route) return [];
      const item = resolveOpenAPIPathItem(sourceItem, components);
      if (!item) return [];
      return Object.entries(item)
        .flatMap(([rawMethod, rawOperation]) => {
          const method = rawMethod.toUpperCase();
          const operation = record(rawOperation);
          if (!HTTP_METHODS.has(method) || !operation) return [];
          return [{ routeID: route.id, routeSlug, path, method, operation, pathItem: item }];
        })
        .sort((left, right) => left.method < right.method ? -1 : left.method > right.method ? 1 : 0);
    })
    .sort((left, right) => (
      left.path < right.path ? -1 : left.path > right.path ? 1
        : left.method < right.method ? -1 : left.method > right.method ? 1 : 0
    ));
}

export function findOpenAPIOperation(
  operations: OpenAPIOperation[],
  requested: OpenAPIOperationIdentity,
  options: { identitiesComplete?: boolean } = {},
): OpenAPIOperation | undefined {
  const method = requested.method?.toUpperCase();
  const exact = operations.find((operation) => (
    operation.routeID === requested.routeID
    && operation.path === requested.path
    && operation.method === method
  ));
  return exact ?? (options.identitiesComplete === false ? undefined : operations[0]);
}

function normalizedOrigin(raw: string) {
  const parsed = new URL(raw);
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") throw new Error("origin must use http or https");
  if (parsed.username || parsed.password || parsed.search || parsed.hash || parsed.pathname !== "/") {
    throw new Error("origin must not contain credentials, query, fragment, or a path");
  }
  return parsed.origin;
}

function pathValue(values: OpenAPIInvocationValues, key: string) {
  const value = values[key];
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean" ? String(value) : "";
}

function encodeOpenAPIPathSegment(segment: string, values: OpenAPIInvocationValues) {
  let encoded = "";
  let lastIndex = 0;
  for (const match of segment.matchAll(/\{([^}]+)\}/g)) {
    encoded += encodeInvocationPathSegment(segment.slice(lastIndex, match.index));
    const name = match[1]!;
    encoded += Object.hasOwn(values, name) ? encodeInvocationPathSegment(pathValue(values, name)) : `{${name}}`;
    lastIndex = match.index! + match[0].length;
  }
  return `${encoded}${encodeInvocationPathSegment(segment.slice(lastIndex))}`;
}

function routeRelativePath(routeSlug: string, path: string) {
  const normalized = path.startsWith("/") ? path : `/${path}`;
  if (!routeSlug) return normalized;
  const routePrefix = `/${routeSlug}`;
  return normalized === routePrefix ? "" : normalized.startsWith(`${routePrefix}/`) ? normalized.slice(routePrefix.length) : normalized;
}

function encodeQuery(query: OpenAPIInvocationValues["query"]) {
  if (!query || typeof query !== "object" || Array.isArray(query)) return "";
  const parameters = new URLSearchParams();
  for (const [name, rawValue] of Object.entries(query)) {
    for (const value of Array.isArray(rawValue) ? rawValue : [rawValue]) parameters.append(name, value);
  }
  const encoded = parameters.toString();
  return encoded ? `?${encoded}` : "";
}

export function buildOpenAPIInvocationURL(
  origin: string,
  serviceSlug: string,
  routeSlug: string,
  path: string,
  values: OpenAPIInvocationValues,
) {
  if (!origin) return "";
  const base = ["v1", "api", encodeInvocationPathSegment(serviceSlug), ...(routeSlug ? [encodeInvocationPathSegment(routeSlug)] : [])].join("/");
  const relative = routeRelativePath(routeSlug, path)
    .split("/")
    .map((segment) => encodeOpenAPIPathSegment(segment, values))
    .join("/");
  const separator = relative && !relative.startsWith("/") ? "/" : "";
  return `${normalizedOrigin(origin)}/${base}${separator}${relative}${encodeQuery(values.query)}`;
}
