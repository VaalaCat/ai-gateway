import type { APICatalogRoute } from "@/lib/api/api-access";

import type { OpenAPIOperation } from "./openapi-operation-selection";

export type OpenAPIObject = Record<string, unknown>;
export type OpenAPIParameterLocation = "path" | "query" | "header";
export type OpenAPIUnsupportedReason = "reference" | "content" | "shape" | "serialization" | "mediaType";

const COMPONENT_KINDS = new Set([
  "parameters",
  "requestBodies",
  "responses",
  "headers",
  "schemas",
  "examples",
  "pathItems",
]);

export interface ResolvedOpenAPIObject {
  value: OpenAPIObject;
  reference?: string;
  resolved: boolean;
  recursive: boolean;
}

export interface NormalizedOpenAPIParameter {
  name: string;
  in: OpenAPIParameterLocation | "unknown";
  required: boolean;
  value: string;
  schema?: unknown;
  style?: string;
  explode?: boolean;
  allowReserved?: boolean;
  content?: unknown;
  reference?: string;
  supported: boolean;
  unsupportedReason?: OpenAPIUnsupportedReason;
}

export interface OpenAPIInvocationDraft {
  path: Record<string, string>;
  query: Record<string, string>;
  headers: Record<string, string>;
  body: string;
  contentType?: string;
}

export interface NormalizedOpenAPIMediaType {
  contentType: string;
  body: string;
  examples: Array<{ name: string; value: unknown }>;
  supported: boolean;
  unsupportedReason?: OpenAPIUnsupportedReason;
}

export interface NormalizedOpenAPIRequestBody {
  required: boolean;
  reference?: string;
  mediaTypes: NormalizedOpenAPIMediaType[];
  supported: boolean;
  unsupportedReason?: OpenAPIUnsupportedReason;
}

export function openAPIObject(value: unknown): OpenAPIObject | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? value as OpenAPIObject
    : undefined;
}

function decodeJSONPointerSegment(segment: string) {
  let decoded = "";
  for (let index = 0; index < segment.length; index++) {
    if (segment[index] !== "~") {
      decoded += segment[index];
      continue;
    }
    const escape = segment[index + 1];
    if (escape !== "0" && escape !== "1") return undefined;
    decoded += escape === "0" ? "~" : "/";
    index++;
  }
  return decoded;
}

function componentReference(reference: string) {
  if (!reference.startsWith("#")) return undefined;
  let fragment: string;
  try {
    fragment = decodeURIComponent(reference.slice(1));
  } catch {
    return undefined;
  }
  if (!fragment.startsWith("/")) return undefined;
  const segments = fragment.slice(1).split("/");
  if (segments.length !== 3) return undefined;
  const [root, kind, name] = segments.map(decodeJSONPointerSegment);
  return root === "components" && kind && name && COMPONENT_KINDS.has(kind)
    ? { kind, name }
    : undefined;
}

export function resolveOpenAPIObject(value: unknown, components: OpenAPIObject): ResolvedOpenAPIObject {
  const original = openAPIObject(value) ?? {};
  const reference = typeof original.$ref === "string" ? original.$ref : undefined;
  if (!reference) return { value: original, resolved: false, recursive: false };

  const target = componentReference(reference);
  const collection = target ? openAPIObject(components[target.kind]) : undefined;
  const resolved = target && collection && Object.hasOwn(collection, target.name)
    ? openAPIObject(collection[target.name])
    : undefined;
  if (!resolved) return { value: original, reference, resolved: false, recursive: false };
  return {
    value: resolved,
    reference,
    resolved: true,
    recursive: resolved.$ref === reference,
  };
}

export function listOpenAPIRouteSlugs(document: unknown) {
  const paths = openAPIObject(openAPIObject(document)?.paths);
  if (!paths) return [];
  const slugs = new Set<string>();
  for (const item of Object.values(paths)) {
    const slug = openAPIObject(item)?.["x-ai-gateway-route-slug"];
    if (typeof slug === "string") slugs.add(slug);
  }
  return [...slugs];
}

export function unresolvedOpenAPIRouteSlugs(document: unknown, routes: APICatalogRoute[]) {
  const loaded = new Set(routes.map((route) => route.slug));
  return listOpenAPIRouteSlugs(document).filter((slug) => !loaded.has(slug));
}

function scalarString(value: unknown) {
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean"
    ? String(value)
    : "";
}

function namedExampleValue(examples: unknown, components: OpenAPIObject) {
  const collection = openAPIObject(examples);
  if (!collection) return undefined;
  const firstName = Object.keys(collection).sort()[0];
  if (!firstName) return undefined;
  const resolved = resolveOpenAPIObject(collection[firstName], components).value;
  return Object.hasOwn(resolved, "value") ? resolved.value : resolved;
}

function parameterExample(parameter: OpenAPIObject, schema: OpenAPIObject | undefined, components: OpenAPIObject) {
  if (Object.hasOwn(parameter, "example")) return scalarString(parameter.example);
  const named = namedExampleValue(parameter.examples, components);
  if (named !== undefined) return scalarString(named);
  if (schema && Object.hasOwn(schema, "example")) return scalarString(schema.example);
  if (schema && Object.hasOwn(schema, "default")) return scalarString(schema.default);
  return "";
}

function parameterLocation(value: unknown): OpenAPIParameterLocation | undefined {
  return value === "path" || value === "query" || value === "header" ? value : undefined;
}

function defaultSerialization(location: OpenAPIParameterLocation) {
  return location === "query"
    ? { style: "form", explode: true }
    : { style: "simple", explode: false };
}

function normalizeParameter(raw: unknown, components: OpenAPIObject): NormalizedOpenAPIParameter | undefined {
  const resolved = resolveOpenAPIObject(raw, components);
  const parameter = resolved.value;
  const location = parameterLocation(parameter.in);
  const name = typeof parameter.name === "string" ? parameter.name : resolved.reference;
  if (!name) return undefined;
  if (location === "header" && name.trim().toLowerCase() === "authorization") return undefined;
  if (!location) {
    return {
      name,
      in: "unknown",
      required: false,
      value: "",
      reference: resolved.reference,
      supported: false,
      unsupportedReason: "reference",
    };
  }

  const schemaView = resolveOpenAPIObject(parameter.schema, components);
  const schema = parameter.schema === undefined ? undefined : schemaView.value;
  const schemaObject = openAPIObject(schema);
  const schemaIsReference = typeof openAPIObject(parameter.schema)?.$ref === "string";
  const scalarSchemaTypes = new Set(["string", "number", "integer", "boolean"]);
  const defaults = defaultSerialization(location);
  const style = typeof parameter.style === "string" ? parameter.style : defaults.style;
  const explode = typeof parameter.explode === "boolean" ? parameter.explode : defaults.explode;
  let unsupportedReason: OpenAPIUnsupportedReason | undefined;
  if (parameter.content !== undefined) unsupportedReason = "content";
  else if (schemaIsReference && (
    !schemaView.resolved
    || schemaView.recursive
    || !scalarSchemaTypes.has(String(schemaObject?.type ?? ""))
  )) unsupportedReason = "reference";
  else if (schemaObject?.type === "array" || schemaObject?.type === "object") unsupportedReason = "shape";
  else if (
    style !== defaults.style
    || explode !== defaults.explode
    || parameter.allowReserved === true
  ) unsupportedReason = "serialization";

  return {
    name,
    in: location,
    required: location === "path" || parameter.required === true,
    value: parameterExample(parameter, schemaObject, components),
    schema,
    style,
    explode,
    allowReserved: parameter.allowReserved === true,
    content: parameter.content,
    reference: resolved.reference,
    supported: unsupportedReason === undefined,
    unsupportedReason,
  };
}

function parameterItems(value: unknown) {
  return Array.isArray(value) ? value : [];
}

export function normalizeOpenAPIParameters(operation: OpenAPIOperation, components: OpenAPIObject) {
  const merged = new Map<string, NormalizedOpenAPIParameter>();
  const sources = [
    ...parameterItems(operation.pathItem.parameters),
    ...parameterItems(operation.operation.parameters),
  ];
  for (const item of sources) {
    const parameter = normalizeParameter(item, components);
    if (!parameter) continue;
    const key = `${parameter.in}\u0000${parameter.name}`;
    merged.set(key, parameter);
  }
  return [...merged.values()];
}

function bodyString(value: unknown) {
  if (value === undefined) return "";
  return typeof value === "string" ? value : JSON.stringify(value, null, 2) ?? "";
}

function normalizedNamedExamples(value: unknown, components: OpenAPIObject) {
  const examples = openAPIObject(value);
  if (!examples) return [];
  return Object.keys(examples).sort().map((name) => {
    const example = resolveOpenAPIObject(examples[name], components).value;
    return { name, value: Object.hasOwn(example, "value") ? example.value : example };
  });
}

function openAPIMediaTypeSupported(contentType: string) {
  const essence = contentType.split(";", 1)[0]!.trim().toLowerCase();
  return essence === "application/json"
    || essence.endsWith("+json")
    || essence.startsWith("text/");
}

export function normalizeOpenAPIRequestBody(
  operation: OpenAPIOperation,
  components: OpenAPIObject,
): NormalizedOpenAPIRequestBody {
  const resolved = resolveOpenAPIObject(operation.operation.requestBody, components);
  const requestBody = resolved.value;
  const requestBodyReferenceUnsupported = resolved.reference !== undefined && (
    !resolved.resolved
    || resolved.recursive
    || typeof requestBody.$ref === "string"
  );
  const content = openAPIObject(requestBody.content) ?? {};
  const mediaTypes = Object.keys(content).sort().map((contentType) => {
    const media = openAPIObject(content[contentType]) ?? {};
    const examples = normalizedNamedExamples(media.examples, components);
    const schema = resolveOpenAPIObject(media.schema, components).value;
    const value = Object.hasOwn(media, "example") ? media.example
      : examples.length ? examples[0]!.value
        : Object.hasOwn(schema, "example") ? schema.example
          : Object.hasOwn(schema, "default") ? schema.default
            : undefined;
    const supported = openAPIMediaTypeSupported(contentType);
    return {
      contentType,
      body: bodyString(value),
      examples,
      supported,
      ...(supported ? {} : { unsupportedReason: "mediaType" as const }),
    };
  });
  return {
    required: requestBody.required === true,
    reference: resolved.reference,
    mediaTypes,
    supported: !requestBodyReferenceUnsupported,
    ...(requestBodyReferenceUnsupported ? { unsupportedReason: "reference" as const } : {}),
  };
}

export function createOpenAPIInvocationDraft(
  operation: OpenAPIOperation,
  components: OpenAPIObject,
): OpenAPIInvocationDraft {
  const draft: OpenAPIInvocationDraft = { path: {}, query: {}, headers: {}, body: "" };
  for (const parameter of normalizeOpenAPIParameters(operation, components)) {
    if (parameter.in === "unknown") continue;
    const values = parameter.in === "header" ? draft.headers : draft[parameter.in];
    values[parameter.name] = parameter.value;
  }
  const firstMediaType = normalizeOpenAPIRequestBody(operation, components).mediaTypes[0];
  if (firstMediaType) {
    draft.body = firstMediaType.body;
    draft.contentType = firstMediaType.contentType;
  }
  return draft;
}

export function isOpenAPIMethodAllowed(allowedMethods: string[], method: string) {
  if (allowedMethods.length === 0) return true;
  const normalized = method.toUpperCase();
  return allowedMethods.some((allowed) => allowed.toUpperCase() === normalized);
}
