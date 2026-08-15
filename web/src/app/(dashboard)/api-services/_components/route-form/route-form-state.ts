import type { APIProtocol, APIRequestExample, APIRoute, APIRouteFields, APIRouteTargetCommand } from "@/lib/api/api-services";
import { isRouteSlug } from "../route-slug";
import { credentialComplete } from "../upstream-credential";

export interface RouteFormValues {
  path: string;
  protocols: APIProtocol[];
  methodMode: "all" | "selected";
  allowedMethods: string[];
  websocketSubprotocols: string[];
  target: APIRouteTargetCommand;
  forwardSubpath: boolean;
  enabled: boolean;
}

export interface HeaderOverrideRow { id: string; name: string; value: string }
export type ExampleHeaderRow = HeaderOverrideRow;

const emptyExample: APIRequestExample = { method: "", subpath: "", query: "", headers: {}, body: "" };
const headerNamePattern = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/;

export function headerRowsObject(rows: HeaderOverrideRow[]) {
  return Object.fromEntries(rows.map((row) => [row.name.trim(), row.value]));
}

// Kept only until the superseded staged editor is removed in Task 8.
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
  return errors;
}

export function routeFieldsForSubmit(values: RouteFormValues, existingExample = emptyExample): APIRouteFields {
  const path = normalizeRoutePath(values.path);
  return {
    slug: path,
    protocols: values.protocols,
    allowed_methods: values.methodMode === "all" ? [] : values.allowedMethods,
    upstream_path: path,
    forward_subpath: values.forwardSubpath,
    example_request: existingExample,
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
    enabled: route.status === 1,
  };
}
