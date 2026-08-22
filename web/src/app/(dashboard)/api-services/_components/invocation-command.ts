import type { APIProtocol, APIRequestExample } from "@/lib/api/api-services";

const API_TOKEN_TEMPLATE = "${API_TOKEN}" as const;

interface InvocationInput {
  origin: string;
  serviceSlug: string;
  routeSlug: string;
  protocols: APIProtocol[];
  example: APIRequestExample;
  token: string | typeof API_TOKEN_TEMPLATE;
}

export interface CanonicalCurlInvocationInput {
  url: string;
  method: string;
  headers: Record<string, string>;
  body?: string;
  token: string | typeof API_TOKEN_TEMPLATE;
}

export interface InvocationResult {
  kind: "curl" | "websocat";
  command: string;
  publicUrl: string;
}

export function quotePOSIXShell(value: string): string {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}

function normalizedOrigin(raw: string) {
  const parsed = new URL(raw);
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw new Error("origin must use http or https");
  }
  if (parsed.username || parsed.password || parsed.search || parsed.hash) {
    throw new Error("origin must not contain credentials, query, or fragment");
  }
  if (parsed.pathname !== "/") throw new Error("origin must not contain a path");
  return parsed.origin;
}

export function encodeInvocationPathSegment(raw: string) {
  let decoded = raw;
  try {
    decoded = decodeURIComponent(raw);
  } catch {
    // Preserve malformed percent input as literal data instead of widening the path.
  }
  if (decoded === "." || decoded === "..") return decoded.replaceAll(".", "%2E");
  return encodeURIComponent(decoded).replace(/[!'()*]/g, (value) =>
    `%${value.charCodeAt(0).toString(16).toUpperCase()}`,
  );
}

function isGatewayAuthorizationHeader(name: string): boolean {
  return name.trim().toLowerCase() === "authorization";
}

function publicURL(input: InvocationInput, websocket: boolean) {
  const origin = normalizedOrigin(input.origin);
  const base = websocket ? origin.replace(/^http/, "ws") : origin;
  const subpath = input.example.subpath.replace(/^\/+/, "");
  const path = [
    "v1",
    "api",
    encodeInvocationPathSegment(input.serviceSlug),
    encodeInvocationPathSegment(input.routeSlug),
    ...(subpath ? subpath.split("/").map(encodeInvocationPathSegment) : []),
  ].join("/");
  return `${base}/${path}${input.example.query ? `?${input.example.query}` : ""}`;
}

function authorizationArgument(token: InvocationInput["token"]) {
  if (token === API_TOKEN_TEMPLATE) {
    return `${quotePOSIXShell("Authorization: Bearer ")}"${API_TOKEN_TEMPLATE}"`;
  }
  return quotePOSIXShell(`Authorization: Bearer ${token}`);
}

function curlCommand(input: InvocationInput, url: string) {
  return buildCanonicalCurlInvocationCommand({
    url,
    method: input.example.method,
    headers: input.example.headers,
    body: input.example.body,
    token: input.token,
  });
}

export function buildCanonicalCurlInvocationCommand(input: CanonicalCurlInvocationInput) {
  const parts = [
    "curl",
    "--request",
    quotePOSIXShell(input.method || "GET"),
    "--url",
    quotePOSIXShell(input.url),
    "--header",
    authorizationArgument(input.token),
  ];
  for (const [name, value] of Object.entries(input.headers)) {
    if (isGatewayAuthorizationHeader(name)) continue;
    parts.push("--header", quotePOSIXShell(`${name}: ${value}`));
  }
  if (input.body) parts.push("--data-raw", quotePOSIXShell(input.body));
  return parts.join(" ");
}

function websocatCommand(input: InvocationInput, url: string) {
  const parts = ["websocat", "--header", authorizationArgument(input.token)];
  for (const [name, value] of Object.entries(input.example.headers)) {
    if (isGatewayAuthorizationHeader(name)) {
      continue;
    } else if (name.toLowerCase() === "sec-websocket-protocol") {
      parts.push("--protocol", quotePOSIXShell(value));
    } else {
      parts.push("--header", quotePOSIXShell(`${name}: ${value}`));
    }
  }
  parts.push(quotePOSIXShell(url));
  return parts.join(" ");
}

const commandBuilders = {
  curl: curlCommand,
  websocat: websocatCommand,
} satisfies Record<InvocationResult["kind"], (input: InvocationInput, url: string) => string>;

export function buildInvocationCommands(input: InvocationInput): InvocationResult[] {
	const kinds: InvocationResult["kind"][] = [];
	if (input.protocols.includes("http")) kinds.push("curl");
	if (input.protocols.includes("websocket")) kinds.push("websocat");
	return kinds.map((kind) => {
		const url = publicURL(input, kind === "websocat");
		return { kind, publicUrl: url, command: commandBuilders[kind](input, url) };
	});
}

export function buildInvocationCommand(input: InvocationInput): InvocationResult {
	return buildInvocationCommands(input)[0] ?? (() => {
		const url = publicURL(input, false);
		return { kind: "curl" as const, publicUrl: url, command: commandBuilders.curl(input, url) };
	})();
}
