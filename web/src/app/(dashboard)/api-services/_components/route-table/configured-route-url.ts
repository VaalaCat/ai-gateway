import type { SegmentedURLValue } from "../segmented-url";

export interface ConfiguredPublicRouteURLInput {
  origin: string;
  serviceSlug: string;
  routeSlug: string;
  forwardSubpath: boolean;
}

export interface ConfiguredEndpointURLInput {
  finalURL: string;
  endpointName: string;
  routePath: string;
  forwardSubpath: boolean;
}

interface EndpointRouteSegmentationInput {
  text: string;
  endpointName: string;
  routePath: string;
  subpath?: string;
  trailingText?: string;
}

function encodeSegment(raw: string) {
  let decoded = raw;
  try {
    decoded = decodeURIComponent(raw);
  } catch {
    // Preserve malformed percent input as literal data instead of widening the path.
  }
  return encodeURIComponent(decoded).replace(/[!'()*]/g, (value) => `%${value.charCodeAt(0).toString(16).toUpperCase()}`);
}

function encodedPath(raw: string) {
  try {
    const encoded = raw.split("/").map(encodeSegment).join("/");
    return `/${encoded.replace(/^\/+/, "")}`;
  } catch {
    return undefined;
  }
}

function joinOrigin(origin: string, path: string) {
  try {
    const parsed = new URL(origin);
    return parsed.origin === "null" ? path : `${parsed.origin}${path}`;
  } catch {
    return path;
  }
}

function pathEnd(text: string) {
  const queryStart = text.indexOf("?");
  const hashStart = text.indexOf("#");
  if (queryStart < 0) return hashStart < 0 ? text.length : hashStart;
  if (hashStart < 0) return queryStart;
  return Math.min(queryStart, hashStart);
}

function routeEndpoint(pathname: string, routePath: string) {
  const route = encodedPath(routePath)?.replace(/\/+$/, "");
  if (!route || route === "/") return pathname;
  let routeStart = pathname.lastIndexOf(route);
  while (routeStart >= 0) {
    const routeEnd = routeStart + route.length;
    if (routeEnd === pathname.length || pathname[routeEnd] === "/") {
      return pathname.slice(0, routeEnd);
    }
    routeStart = pathname.lastIndexOf(route, routeStart - 1);
  }
  return pathname;
}

function configuredEndpointText(finalURL: string, routePath: string) {
  try {
    const parsed = new URL(finalURL);
    return `${parsed.origin}${routeEndpoint(parsed.pathname, routePath)}`.replace(/\/+$/, "");
  } catch {
    const withoutSearchOrHash = finalURL.slice(0, pathEnd(finalURL));
    return routeEndpoint(withoutSearchOrHash, routePath).replace(/\/+$/, "");
  }
}

function segmentedPublicURL(text: string, serviceSlug: string, routeSlug: string): SegmentedURLValue {
  const marker = "/v1/api/";
  const pathStart = text.indexOf(marker);
  const serviceStart = pathStart + marker.length;
  const serviceEnd = text.indexOf("/", serviceStart);
  const routeStart = serviceEnd + 1;
  const routeEnd = text.indexOf("/", routeStart);
  return {
    text,
    segments: [
      { start: serviceStart, end: serviceEnd, kind: "service", label: serviceSlug },
      { start: routeStart, end: routeEnd < 0 ? text.length : routeEnd, kind: "route", label: routeSlug },
    ],
  };
}

export function segmentEndpointRouteURL({ text, endpointName, routePath, subpath = "", trailingText = "" }: EndpointRouteSegmentationInput): SegmentedURLValue {
  if (!routePath.replaceAll("/", "")) return { text, segments: [{ start: 0, end: text.length, kind: "endpoint", label: endpointName }] };
  const route = encodedPath(routePath);
  const child = subpath ? encodedPath(subpath) : undefined;
  if (!route || (subpath && !child)) return { text, segments: [{ start: 0, end: text.length, kind: "endpoint", label: endpointName }] };
  const routeSuffix = child ? route.replace(/\/+$/, "") : route;
  const completeSuffix = `${child ? `${routeSuffix}/${child.replace(/^\/+/, "")}` : routeSuffix}${trailingText}`;
  const textPathEnd = pathEnd(text);
  const routeStart = textPathEnd - completeSuffix.length;
  if (routeStart < 0 || text.slice(routeStart, textPathEnd) !== completeSuffix) {
    return { text, segments: [{ start: 0, end: text.length, kind: "endpoint", label: endpointName }] };
  }
  return {
    text,
    segments: [
      { start: 0, end: routeStart, kind: "endpoint", label: endpointName },
      { start: routeStart, end: routeStart + routeSuffix.length, kind: "route", label: routePath },
    ],
  };
}

export function buildConfiguredPublicRouteURL(input: ConfiguredPublicRouteURLInput): SegmentedURLValue {
  const path = `/v1/api/${encodeSegment(input.serviceSlug)}/${encodeSegment(input.routeSlug)}`;
  const text = joinOrigin(input.origin, path) + (input.forwardSubpath ? "/…" : "");
  return segmentedPublicURL(text, input.serviceSlug, input.routeSlug);
}

export function buildConfiguredEndpointURL(input: ConfiguredEndpointURLInput): SegmentedURLValue {
  const text = configuredEndpointText(input.finalURL, input.routePath) + (input.forwardSubpath ? "/…" : "");
  return segmentEndpointRouteURL({
    text,
    endpointName: input.endpointName,
    routePath: input.routePath,
    trailingText: input.forwardSubpath ? "/…" : "",
  });
}
