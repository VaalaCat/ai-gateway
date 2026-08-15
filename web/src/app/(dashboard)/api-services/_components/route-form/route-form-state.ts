import type { APIProtocol, APIRequestExample, APIRoute, APIRouteFields, APIRouteTargetCommand } from "@/lib/api/api-services";
import { isRouteSlug } from "../route-slug";
import { isStandardRouteHTTPMethod, STANDARD_ROUTE_HTTP_METHODS } from "../route-editor/standard-http-methods";
import { credentialComplete } from "../upstream-credential";

export interface RouteFormValues {
  path: string;
  protocols: APIProtocol[];
  methodMode: "all" | "selected";
  allowedMethods: string[];
  websocketSubprotocols: string[];
  target: APIRouteTargetCommand;
  forwardSubpath: boolean;
  exampleEnabled: boolean;
  exampleRequest: APIRequestExample;
  exampleHeaderRows: ExampleHeaderRow[];
  enabled: boolean;
}

export interface HeaderOverrideRow { id: string; name: string; value: string }
export type ExampleHeaderRow = HeaderOverrideRow;

const emptyExample: APIRequestExample = { method: "", subpath: "", query: "", headers: {}, body: "" };
const headerNamePattern = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/;

export function headerRowsObject(rows: HeaderOverrideRow[]) {
  return Object.fromEntries(rows.map((row) => [row.name.trim(), row.value]));
}

export const exampleHeadersObject = headerRowsObject;

export function requestExampleByteLength(example: APIRequestExample) {
  const encoded = JSON.stringify(example).replaceAll("<", "\\u003c").replaceAll(">", "\\u003e").replaceAll("&", "\\u0026").replaceAll("\u2028", "\\u2028").replaceAll("\u2029", "\\u2029");
  return new TextEncoder().encode(encoded).byteLength;
}

const blockedExampleHeaders = new Set(["authorization", "proxy-authorization", "host", "connection", "keep-alive", "proxy-authenticate", "proxy-connection", "te", "trailer", "transfer-encoding", "upgrade", "forwarded", "cookie", "set-cookie", "x-api-key", "x-auth-token", "x-access-token", "x-amz-security-token", "x-goog-api-key", "x-azure-api-key", "api-key", "authentication", "x-authentication", "x-authorization", "x-proxy-authorization"]);

export function unsafeExampleHeader(name: string) {
  const lower = name.toLowerCase();
  if (lower === "x-tokenize" || lower === "x-token-bucket") return false;
  return blockedExampleHeaders.has(lower)
    || lower.startsWith("x-vaala-")
    || lower.startsWith("x-forwarded-")
    || /(^|-)(auth|authorization|credential|secret|password|passwd|cookie|signature|token)(-|$)/.test(lower)
    || /(apikey|accesskey|accesstoken|refreshtoken|idtoken)/.test(lower.replaceAll("-", ""));
}

export function invalidExampleHeader(row: ExampleHeaderRow, rows: ExampleHeaderRow[]) {
  const name = row.name.trim();
  return !name
    || !headerNamePattern.test(name)
    || !validHeaderValue(row.value)
    || unsafeExampleHeader(name)
    || rows.some((candidate) => candidate.id !== row.id && candidate.name.trim().toLowerCase() === name.toLowerCase());
}

export function safeExampleSubpath(raw: string) {
  if (!raw) return true;
  if (/[\\\x00\r\n?#]/.test(raw) || raw.startsWith("//") || /^[A-Za-z][A-Za-z\d+.-]*:/.test(raw)) return false;
  return raw.split("/").every((part) => {
    let segment = part;
    for (let layer = 0; layer < 4; layer += 1) {
      if (segment === "." || segment === ".." || /[/\\\x00\r\n?#]/.test(segment) || /%(2f|5c|00)/i.test(segment)) return false;
      try {
        const decoded = decodeURIComponent(segment);
        if (decoded === segment) return true;
        segment = decoded;
      } catch {
        return false;
      }
    }
    return false;
  });
}

export function safeExampleQuery(raw: string) {
  if (/[\x00\r\n#]/.test(raw)) return false;
  for (let index = 0; index < raw.length; index += 1) {
    if (raw[index] !== "%") continue;
    if (!/^[\da-f]{2}$/i.test(raw.slice(index + 1, index + 3))) return false;
    index += 2;
  }
  return true;
}

function validHeaderValue(value: string) {
  return !/[\x00-\x08\x0a-\x1f\x7f]/.test(value);
}

function unsafeOverrideHeader(name: string) {
  const lower = name.toLowerCase();
  return ["connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "proxy-connection", "te", "trailer", "transfer-encoding", "upgrade", "forwarded"].includes(lower)
    || lower.startsWith("x-vaala-")
    || lower.startsWith("x-forwarded-");
}

function validHostHeader(value: string) {
  return /^[0-9A-Za-z!$%&'()*+,\-.:;=\[\]_~]*$/.test(value);
}

function invalidURL(raw: string) {
  try {
    const url = new URL(raw);
    return !/^https?:$/.test(url.protocol) || !url.host || Boolean(url.username || url.password || url.hash);
  } catch {
    return true;
  }
}

export function invalidOverrideHeader(row: HeaderOverrideRow, rows: HeaderOverrideRow[]) {
  const name = row.name.trim();
  return !name
    || !headerNamePattern.test(name)
    || !validHeaderValue(row.value)
    || unsafeOverrideHeader(name)
    || (name.toLowerCase() === "host" && !validHostHeader(row.value))
    || rows.some((candidate) => candidate.id !== row.id && candidate.name.trim().toLowerCase() === name.toLowerCase());
}

export function routeTargetForRequest(values: RouteFormValues, targetHeaderRows: HeaderOverrideRow[] = []): APIRouteTargetCommand {
  if (values.target.mode === "existing") return values.target;
  return {
    ...values.target,
    first_upstream: {
      ...values.target.first_upstream,
      header_override: headerRowsObject(targetHeaderRows),
    },
  };
}

export function emptyRouteFormValues(): RouteFormValues {
  return {
    path: "",
    protocols: ["http"],
    methodMode: "all",
    allowedMethods: [],
    websocketSubprotocols: [],
    target: { mode: "existing", backend_id: 0 },
    forwardSubpath: false,
    exampleEnabled: false,
    exampleRequest: emptyExample,
    exampleHeaderRows: [],
    enabled: true,
  };
}

export type RouteFormAction =
  | { type: "methods"; mode: RouteFormValues["methodMode"]; methods: string[] }
  | { type: "target"; target: APIRouteTargetCommand }
  | { type: "patch"; patch: Partial<RouteFormValues> };

export function routeFormReducer(values: RouteFormValues, action: RouteFormAction): RouteFormValues {
  if (action.type === "methods") {
    return {
      ...values,
      methodMode: action.mode,
      allowedMethods: action.mode === "all" ? [] : [...new Set(action.methods)],
    };
  }
  if (action.type === "target") {
    return {
      ...values,
      target: action.target.mode === "existing" ? { mode: "existing", backend_id: action.target.backend_id } : action.target,
    };
  }
  return { ...values, ...action.patch };
}

export function normalizeRoutePath(raw: string) {
  return raw.trim().replace(/^\/+/, "");
}

export function validateRouteFormValues(values: RouteFormValues, targetHeaderRows: HeaderOverrideRow[] = []): string[] {
  const errors: string[] = [];
  const path = normalizeRoutePath(values.path);
  if (!isRouteSlug(path)) errors.push("invalidRouteSlug");
  if (values.protocols.length === 0) errors.push("protocolRequired");
  if (values.methodMode === "selected" && values.allowedMethods.length === 0) errors.push("allowedMethodsRequired");
  if (values.target.mode === "existing" && values.target.backend_id < 1) errors.push("backendRequired");
  if (values.target.mode === "create" && (!values.target.backend.name.trim() || values.target.backend.name.trim().length > 64)) errors.push("invalidTargetName");
  if (values.target.mode === "create" && !values.target.first_upstream.name.trim()) errors.push("endpointNameRequired");
  if (values.target.mode === "create" && !values.target.first_upstream.base_url.trim()) errors.push("endpointUrlRequired");
  if (values.target.mode === "create" && values.target.first_upstream.base_url && invalidURL(values.target.first_upstream.base_url)) errors.push("invalidEndpointUrl");
  if (values.target.mode === "create" && values.target.first_upstream.proxy_url && invalidURL(values.target.first_upstream.proxy_url)) errors.push("invalidProxyUrl");
  if (values.target.mode === "create" && !credentialComplete(values.target.first_upstream.auth_type, values.target.first_upstream.credential)) errors.push("credentialRequired");
  if (values.target.mode === "create" && targetHeaderRows.some((row) => invalidOverrideHeader(row, targetHeaderRows))) errors.push("invalidHeaderOverride");
  if (values.exampleEnabled) {
    const example = { ...values.exampleRequest, headers: exampleHeadersObject(values.exampleHeaderRows) };
    const allowedMethods: readonly string[] = values.allowedMethods.length ? values.allowedMethods : STANDARD_ROUTE_HTTP_METHODS;
    if (!isStandardRouteHTTPMethod(example.method) || !allowedMethods.includes(example.method)) errors.push("invalidExampleMethod");
    if (!safeExampleSubpath(example.subpath)) errors.push("invalidExamplePath");
    if (!safeExampleQuery(example.query)) errors.push("invalidExampleQuery");
    const headerRows = values.exampleHeaderRows.length
      ? values.exampleHeaderRows
      : Object.entries(values.exampleRequest.headers).map(([name, value], index) => ({ id: `example-header-${index}`, name, value }));
    if (headerRows.some((row) => invalidExampleHeader(row, headerRows))) errors.push("invalidExampleHeader");
    if (requestExampleByteLength(example) > 64 * 1024) errors.push("bodyTooLarge");
  }
  return errors;
}

export function routeFieldsForSubmit(values: RouteFormValues): APIRouteFields {
  const path = normalizeRoutePath(values.path);
  return {
    slug: path,
    protocols: values.protocols,
    allowed_methods: values.methodMode === "all" ? [] : values.allowedMethods,
    upstream_path: path,
    forward_subpath: values.forwardSubpath,
    example_request: values.exampleEnabled ? { ...values.exampleRequest, headers: exampleHeadersObject(values.exampleHeaderRows) } : emptyExample,
    websocket_subprotocols: values.protocols.includes("websocket") ? values.websocketSubprotocols : [],
    status: values.enabled ? 1 : 0,
  };
}

export function hydrateRouteFormValues(route: APIRoute): RouteFormValues {
  return {
    path: route.slug,
    protocols: route.protocols,
    methodMode: route.allowed_methods.length ? "selected" : "all",
    allowedMethods: route.allowed_methods,
    websocketSubprotocols: route.websocket_subprotocols ?? [],
    target: { mode: "existing", backend_id: route.backend_id },
    forwardSubpath: route.forward_subpath,
    exampleEnabled: Object.values(route.example_request).some((value) => typeof value === "object" ? Object.keys(value).length > 0 : value !== ""),
    exampleRequest: { ...route.example_request, headers: { ...route.example_request.headers } },
    exampleHeaderRows: Object.entries(route.example_request.headers).map(([name, value], index) => ({ id: `example-header-${index}`, name, value })),
    enabled: route.status === 1,
  };
}
