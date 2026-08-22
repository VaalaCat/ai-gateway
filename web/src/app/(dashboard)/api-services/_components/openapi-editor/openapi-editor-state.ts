export type JSONValue = null | boolean | number | string | JSONValue[] | { [key: string]: JSONValue };

export interface OpenAPIPathItem {
  $ref?: string;
  summary?: string;
  description?: string;
  parameters?: Array<Record<string, JSONValue>>;
  operations?: Record<string, Record<string, JSONValue>>;
  [key: string]: JSONValue | undefined;
}

export interface OpenAPIRouteDocument {
  id: number;
  slug: string;
  upstream_path: string;
  updated_at: number;
  paths: Record<string, OpenAPIPathItem>;
}

export interface OpenAPIDocumentSnapshot {
  service: { id: number; slug: string; name: string; description: string; updated_at: number; document: Record<string, JSONValue> };
  routes: OpenAPIRouteDocument[];
}

export interface OpenAPIUpdateRequest {
  service: { id: number; updated_at: number; document: Record<string, JSONValue> };
  routes: Array<{ id: number; updated_at: number; paths: Record<string, OpenAPIPathItem> }>;
}

function clone<T>(value: T): T { return structuredClone(value); }

function requirePath(path: string) {
  const normalized = path.trim();
  if (!normalized.startsWith("/")) throw new Error("OpenAPI path must start with /");
  return normalized;
}

function httpMethod(method: string) {
  const normalized = method.trim().toUpperCase();
  if (!normalized) throw new Error("OpenAPI operation method is required");
  return normalized;
}

type RoutePathFacts = Pick<OpenAPIRouteDocument, "slug" | "upstream_path">;

export function exportedPath(route: RoutePathFacts, storedPath: string) {
  const path = requirePath(storedPath);
  if (route.slug === "") return path;
  const publicPrefix = `/${route.slug}`;
  const upstreamPrefix = route.upstream_path.replace(/\/$/, "");
  if (upstreamPrefix === "") {
    if (path === publicPrefix || path.startsWith(`${publicPrefix}/`)) return path;
    return `${publicPrefix}${path}`;
  }
  if (path === upstreamPrefix) return publicPrefix;
  if (!path.startsWith(`${upstreamPrefix}/`)) throw new Error("openAPIPathOutsideRoute");
  return `${publicPrefix}${path.slice(upstreamPrefix.length)}`;
}

function validateUniqueExportedPaths(snapshot: OpenAPIDocumentSnapshot) {
  const publicPathOwners = new Map<string, string>();
  const publicPathByStoredPath = new Map<string, string>();
  for (const route of snapshot.routes) {
    for (const storedPath of Object.keys(route.paths)) {
      const publicPath = exportedPath(route, storedPath);
      const owner = `${route.id}:${storedPath}`;
      if (publicPathOwners.has(publicPath) && publicPathOwners.get(publicPath) !== owner) throw new Error("openAPIPublicPathDuplicate");
      if (publicPathByStoredPath.has(storedPath) && publicPathByStoredPath.get(storedPath) !== publicPath) throw new Error("openAPIStoredPathDuplicate");
      publicPathOwners.set(publicPath, owner);
      publicPathByStoredPath.set(storedPath, publicPath);
    }
  }
}

function updateRoute(snapshot: OpenAPIDocumentSnapshot, routeID: number, update: (route: OpenAPIRouteDocument) => OpenAPIRouteDocument) {
  let found = false;
  const routes = snapshot.routes.map((route) => {
    if (route.id !== routeID) return route;
    found = true;
    return update(route);
  });
  if (!found) throw new Error(`OpenAPI route ${routeID} does not exist`);
  return { ...snapshot, routes };
}

export function renamePath(snapshot: OpenAPIDocumentSnapshot, routeID: number, oldPath: string, nextPath: string) {
  const source = requirePath(oldPath);
  const target = requirePath(nextPath);
  const renamed = updateRoute(snapshot, routeID, (route) => {
    if (!(source in route.paths)) throw new Error(`OpenAPI path ${source} does not exist`);
    if (target !== source && target in route.paths) throw new Error("openAPIStoredPathDuplicate");
    const paths = { ...route.paths };
    const item = paths[source];
    delete paths[source];
    paths[target] = clone(item);
    return { ...route, paths };
  });
  validateUniqueExportedPaths(renamed);
  return renamed;
}

export function upsertOperation(snapshot: OpenAPIDocumentSnapshot, routeID: number, path: string, method: string, operation: Record<string, JSONValue>) {
  const normalizedPath = requirePath(path);
  const normalizedMethod = httpMethod(method);
  return updateRoute(snapshot, routeID, (route) => {
    const item = route.paths[normalizedPath];
    if (!item) throw new Error(`OpenAPI path ${normalizedPath} does not exist`);
    return {
      ...route,
      paths: { ...route.paths, [normalizedPath]: { ...item, operations: { ...item.operations, [normalizedMethod]: clone(operation) } } },
    };
  });
}

export function addOperation(snapshot: OpenAPIDocumentSnapshot, routeID: number, path: string, method: string) {
  const normalizedMethod = httpMethod(method);
  const route = snapshot.routes.find((candidate) => candidate.id === routeID);
  const item = route?.paths[requirePath(path)];
  if (!item) throw new Error("openAPIPathMissing");
  if (item.operations?.[normalizedMethod]) throw new Error("openAPIMethodDuplicate");
  return upsertOperation(snapshot, routeID, path, normalizedMethod, { responses: { 200: { description: "OK" } } });
}

export function renameOperation(snapshot: OpenAPIDocumentSnapshot, routeID: number, path: string, oldMethod: string, nextMethod: string) {
  const source = httpMethod(oldMethod);
  const target = httpMethod(nextMethod);
  return updateRoute(snapshot, routeID, (route) => {
    const normalizedPath = requirePath(path);
    const item = route.paths[normalizedPath];
    const operation = item?.operations?.[source];
    if (!item || !operation) throw new Error("openAPIOperationMissing");
    if (target !== source && item.operations?.[target]) throw new Error("openAPIMethodDuplicate");
    const operations = { ...item.operations };
    delete operations[source];
    operations[target] = clone(operation);
    return { ...route, paths: { ...route.paths, [normalizedPath]: { ...item, operations } } };
  });
}

export function removeOperation(snapshot: OpenAPIDocumentSnapshot, routeID: number, path: string, method: string) {
  const normalizedPath = requirePath(path);
  const normalizedMethod = httpMethod(method);
  return updateRoute(snapshot, routeID, (route) => {
    const item = route.paths[normalizedPath];
    if (!item) throw new Error(`OpenAPI path ${normalizedPath} does not exist`);
    const operations = { ...item.operations };
    delete operations[normalizedMethod];
    return { ...route, paths: { ...route.paths, [normalizedPath]: { ...item, operations } } };
  });
}

export function parseJSONField(raw: string): { ok: true; value: JSONValue } | { ok: false; error: string } {
  try { return { ok: true, value: JSON.parse(raw) as JSONValue }; }
  catch { return { ok: false, error: "openAPIJSONInvalid" }; }
}

export function parseJSONObjectField(raw: string): { ok: true; value: Record<string, JSONValue> } | { ok: false; error: string } {
  const parsed = parseJSONField(raw);
  if (!parsed.ok) return parsed;
  if (typeof parsed.value !== "object" || parsed.value === null || Array.isArray(parsed.value)) return { ok: false, error: "openAPIJSONObjectRequired" };
  return { ok: true, value: parsed.value };
}

function updateParameters(snapshot: OpenAPIDocumentSnapshot, routeID: number, path: string, method: string | undefined, update: (parameters: Array<Record<string, JSONValue>>) => Array<Record<string, JSONValue>>) {
  const normalizedPath = requirePath(path);
  return updateRoute(snapshot, routeID, (route) => {
    const item = route.paths[normalizedPath];
    if (!item) throw new Error("openAPIPathMissing");
    if (method === undefined) return { ...route, paths: { ...route.paths, [normalizedPath]: { ...item, parameters: update(item.parameters ?? []) } } };
    const normalizedMethod = httpMethod(method);
    const operation = item.operations?.[normalizedMethod];
    if (!operation) throw new Error("openAPIOperationMissing");
    return { ...route, paths: { ...route.paths, [normalizedPath]: { ...item, operations: { ...item.operations, [normalizedMethod]: { ...operation, parameters: update(Array.isArray(operation.parameters) ? operation.parameters as Array<Record<string, JSONValue>> : []) } } } } };
  });
}

export function addParameter(snapshot: OpenAPIDocumentSnapshot, routeID: number, path: string, method?: string) {
  return updateParameters(snapshot, routeID, path, method, (parameters) => [...parameters, { name: "", in: "query", required: false, schema: {} }]);
}

export function updateParameter(snapshot: OpenAPIDocumentSnapshot, routeID: number, path: string, method: string | undefined, index: number, parameter: Record<string, JSONValue>) {
  return updateParameters(snapshot, routeID, path, method, (parameters) => {
    if (index < 0 || index >= parameters.length) throw new Error("openAPIParameterMissing");
    return parameters.map((current, currentIndex) => currentIndex === index ? clone(parameter) : current);
  });
}

export function removeParameter(snapshot: OpenAPIDocumentSnapshot, routeID: number, path: string, method: string | undefined, index: number) {
  return updateParameters(snapshot, routeID, path, method, (parameters) => {
    if (index < 0 || index >= parameters.length) throw new Error("openAPIParameterMissing");
    return parameters.filter((_, currentIndex) => currentIndex !== index);
  });
}

export function groupPathsByRoute(snapshot: OpenAPIDocumentSnapshot) {
  return snapshot.routes.map((route) => ({ routeID: route.id, slug: route.slug, upstreamPath: route.upstream_path, paths: Object.keys(route.paths).sort() }));
}

export function buildOpenAPIUpdate(snapshot: OpenAPIDocumentSnapshot): OpenAPIUpdateRequest {
  return {
    service: { id: snapshot.service.id, updated_at: snapshot.service.updated_at, document: snapshot.service.document },
    routes: snapshot.routes.map((route) => ({ id: route.id, updated_at: route.updated_at, paths: route.paths })),
  };
}
