import type { APIBackend, APIRoute, APIUpstream } from "./api/api-services";

function opaqueFingerprint(value: unknown) {
  const input = JSON.stringify(value);
  let first = 0x811c9dc5;
  let second = 0x9e3779b9;
  for (let index = 0; index < input.length; index += 1) {
    const code = input.charCodeAt(index);
    first = Math.imul(first ^ code, 0x01000193);
    second = Math.imul(second ^ code, 0x85ebca6b);
  }
  return `${(first >>> 0).toString(16).padStart(8, "0")}${(second >>> 0).toString(16).padStart(8, "0")}`;
}

export function routePreviewDependencyRevision(route: APIRoute, target: APIBackend, endpoints: APIUpstream[]) {
  return opaqueFingerprint({
    route: {
      id: route.id,
      api_service_id: route.api_service_id,
      backend_id: route.backend_id,
      slug: route.slug,
      protocols: route.protocols,
      allowed_methods: route.allowed_methods,
      upstream_path: route.upstream_path,
      forward_subpath: route.forward_subpath,
      example_request: route.example_request,
      websocket_subprotocols: route.websocket_subprotocols,
      status: route.status,
      updated_at: route.updated_at,
    },
    target: {
      id: target.id,
      api_service_id: target.api_service_id,
      name: target.name,
      route_count: target.route_count,
      upstream_count: target.upstream_count,
      enabled_upstream_count: target.enabled_upstream_count,
      endpoint_hosts: target.endpoint_hosts,
      updated_at: target.updated_at,
    },
    endpoints: endpoints.map((endpoint) => ({
      id: endpoint.id,
      backend_id: endpoint.backend_id,
      name: endpoint.name,
      base_url: endpoint.base_url,
      weight: endpoint.weight,
      priority: endpoint.priority,
      auth_type: endpoint.auth_type,
      status: endpoint.status,
      credential_configured: endpoint.credential_configured,
      proxy_url_configured: endpoint.proxy_url_configured,
    })).sort((left, right) => left.id - right.id),
  });
}
