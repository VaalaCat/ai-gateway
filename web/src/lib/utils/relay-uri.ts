const MAX_RELAY_URI_BYTES = 2048;
const ASCII_EDGE_WHITESPACE = /^[\t\n\v\f\r ]+|[\t\n\v\f\r ]+$/g;
const RAW_ASCII_CONTROL = /[\u0000-\u001f\u007f]/;

export type OptionalRelayURIParseResult =
  | { normalized: string }
  | { error: "invalid" | "too_long" };

function hasInvalidQuery(rawQuery: string) {
  if (rawQuery.includes(";")) return true;
  try {
    for (const part of rawQuery.split("&")) {
      const [key, ...rest] = part.split("=");
      decodeURIComponent(key.replaceAll("+", " "));
      decodeURIComponent(rest.join("=").replaceAll("+", " "));
    }
    return false;
  } catch {
    return true;
  }
}

function hasAuthorityUserinfo(uri: string) {
  const schemeEnd = uri.indexOf("://");
  if (schemeEnd < 0) return false;
  const authorityStart = schemeEnd + 3;
  const suffix = uri.slice(authorityStart);
  const boundary = suffix.search(/[/?#]/);
  const authority = boundary < 0 ? suffix : suffix.slice(0, boundary);
  return authority.includes("@");
}

export function parseOptionalRelayURI(raw: string): OptionalRelayURIParseResult {
  const trimmed = raw.replace(ASCII_EDGE_WHITESPACE, "");
  if (!trimmed) return { normalized: "" };
  if (new TextEncoder().encode(trimmed).length > MAX_RELAY_URI_BYTES) {
    return { error: "too_long" };
  }
  if (
    RAW_ASCII_CONTROL.test(trimmed) ||
    trimmed.includes("#") ||
    /%(?![0-9a-fA-F]{2})/.test(trimmed) ||
    hasAuthorityUserinfo(trimmed)
  ) {
    return { error: "invalid" };
  }
  try {
    const parsed = new URL(trimmed);
    if (
      !["ws:", "wss:"].includes(parsed.protocol.toLowerCase()) ||
      !parsed.hostname ||
      (parsed.port !== "" && (Number(parsed.port) < 1 || Number(parsed.port) > 65535)) ||
      parsed.username ||
      parsed.password ||
      parsed.hash ||
      hasInvalidQuery(parsed.search.slice(1))
    ) {
      return { error: "invalid" };
    }
    if (new TextEncoder().encode(parsed.toString()).length > MAX_RELAY_URI_BYTES) {
      return { error: "too_long" };
    }
    return { normalized: trimmed };
  } catch {
    return { error: "invalid" };
  }
}
