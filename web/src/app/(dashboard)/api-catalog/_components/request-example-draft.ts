import type { APICatalogRoute } from "@/lib/api/api-access";
import type { APIRequestExample } from "@/lib/api/api-services";

import type { CatalogProtocol } from "./catalog-selection";

export interface RequestExampleDraft {
  method: string;
  subpath: string;
  query: string;
  headers: Record<string, string>;
  body: string;
}

function isHeader(name: string, expected: string) {
  return name.trim().toLowerCase() === expected;
}

export function createRequestExampleDraft(route: APICatalogRoute): RequestExampleDraft {
  const example = route.example_request;
  const headers = { ...(example?.headers ?? {}) };
  const hasWebSocketProtocol = Object.keys(headers).some((name) =>
    isHeader(name, "sec-websocket-protocol"),
  );
  const websocketProtocol = route.websocket_subprotocols?.[0];

  if (route.protocols.includes("websocket") && websocketProtocol && !hasWebSocketProtocol) {
    headers["Sec-WebSocket-Protocol"] = websocketProtocol;
  }

  return {
    method: example?.method || route.allowed_methods[0] || "GET",
    subpath: example?.subpath ?? "",
    query: example?.query ?? "",
    headers,
    body: example?.body ?? "",
  };
}

export function requestExampleFromDraft(
  draft: RequestExampleDraft,
  protocol: CatalogProtocol,
): APIRequestExample {
  const headers = Object.fromEntries(
    Object.entries(draft.headers).filter(([name]) => (
      !isHeader(name, "authorization")
      && (protocol !== "http" || !isHeader(name, "sec-websocket-protocol"))
    )),
  );

  return {
    method: draft.method,
    subpath: draft.subpath,
    query: draft.query,
    headers,
    body: protocol === "websocket" ? "" : draft.body,
  };
}
