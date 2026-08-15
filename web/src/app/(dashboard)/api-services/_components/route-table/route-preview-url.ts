import type { APIBackend, APIRoute, APIRoutePreview, APIRequestExample } from "@/lib/api/api-services";

import { buildInvocationCommand } from "../invocation-command";
import { segmentEndpointRouteURL } from "./configured-route-url";
import type { SegmentedURLValue } from "../segmented-url";

export interface RoutePreviewViewModel {
  methods: string[] | "all";
  publicURL: SegmentedURLValue;
  targetName: string;
  endpoints: Array<{ id: number; name: string; status: number; finalURL: SegmentedURLValue }>;
  diagnostics: string[];
}

export interface RoutePreviewViewModelInput {
  serviceSlug: string;
  origin: string;
  route: APIRoute;
  backend: APIBackend;
  preview: APIRoutePreview;
  invocationProtocol?: "http" | "websocket";
}

function endpointFinalURLForProtocol(finalURL: string, invocationProtocol: "http" | "websocket" | undefined) {
  if (invocationProtocol !== "websocket") return finalURL;
  try {
    const parsed = new URL(finalURL);
    const websocketProtocol = parsed.protocol === "http:" ? "ws:" : parsed.protocol === "https:" ? "wss:" : undefined;
    return websocketProtocol ? `${websocketProtocol}${finalURL.slice(parsed.protocol.length)}` : finalURL;
  } catch {
    return finalURL;
  }
}

export function buildPublicSegmentedURL(
  origin: string,
  serviceSlug: string,
  route: Pick<APIRoute, "slug" | "protocols" | "example_request">,
  example: APIRequestExample = route.example_request,
): SegmentedURLValue {
  const text = buildInvocationCommand({ origin, serviceSlug, routeSlug: route.slug, protocols: route.protocols, example, token: "${API_TOKEN}" }).publicUrl;
  const pathname = new URL(text).pathname;
  const marker = "/v1/api/";
  const pathStart = text.indexOf(pathname);
  const serviceStart = pathStart + pathname.indexOf(marker) + marker.length;
  const serviceEnd = text.indexOf("/", serviceStart);
  const routeStart = serviceEnd + 1;
  const nextPath = text.indexOf("/", routeStart);
  const query = text.indexOf("?", routeStart);
  const routeEnd = nextPath < 0 ? (query < 0 ? text.length : query) : nextPath;
  return { text, segments: [
    { start: serviceStart, end: serviceEnd, kind: "service", label: serviceSlug },
    { start: routeStart, end: routeEnd, kind: "route", label: route.slug },
  ] };
}

export function buildEndpointSegmentedURL(text: string, endpointName: string, routePath: string, subpath = ""): SegmentedURLValue {
  return segmentEndpointRouteURL({ text, endpointName, routePath, subpath });
}

export function buildRoutePreviewViewModel(input: RoutePreviewViewModelInput): RoutePreviewViewModel {
  return {
    methods: input.route.allowed_methods.length === 0 ? "all" : [...input.route.allowed_methods],
    publicURL: buildPublicSegmentedURL(input.origin, input.serviceSlug, input.route),
    targetName: input.backend.name,
    endpoints: input.preview.endpoints.map((endpoint) => ({
      id: endpoint.upstream_id,
      name: endpoint.upstream_name,
      status: endpoint.status,
      finalURL: buildEndpointSegmentedURL(endpointFinalURLForProtocol(endpoint.final_url, input.invocationProtocol), endpoint.upstream_name, input.route.upstream_path, input.route.forward_subpath ? input.route.example_request.subpath : ""),
    })),
    diagnostics: [...input.preview.diagnostics],
  };
}
