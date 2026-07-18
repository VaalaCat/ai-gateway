import { describe, expect, it } from "vitest";

import { parseOptionalRelayURI } from "./relay-uri";

describe("parseOptionalRelayURI", () => {
  it("accepts an empty global default without adding required semantics", () => {
    expect(parseOptionalRelayURI(" \t\r\n")).toEqual({ normalized: "" });
  });

  it("trims the exact ASCII whitespace set including vertical tab", () => {
    expect(parseOptionalRelayURI("\t\n\v\f\r ws://relay.example.com/tunnel \t\n\v\f\r")).toEqual({
      normalized: "ws://relay.example.com/tunnel",
    });
  });

  it("does not trim Unicode whitespace around a URI", () => {
    expect(parseOptionalRelayURI("\u00a0wss://relay.example.com/tunnel\u00a0")).toEqual({ error: "invalid" });
  });

  it.each([
    ["leading NUL", "\u0000wss://relay.example.com/tunnel"],
    ["trailing NUL", "wss://relay.example.com/tunnel\u0000"],
    ["internal tab", "wss://relay.example.com/tunnel\tsegment"],
    ["internal LF", "wss://relay.example.com/tunnel\nsegment"],
    ["internal VT", "wss://relay.example.com/tunnel\vsegment"],
    ["internal FF", "wss://relay.example.com/tunnel\fsegment"],
    ["internal CR", "wss://relay.example.com/tunnel\rsegment"],
    ["internal DEL", "wss://relay.example.com/tunnel\u007fsegment"],
  ])("rejects raw ASCII control characters: %s", (_name, raw) => {
    expect(parseOptionalRelayURI(raw)).toEqual({ error: "invalid" });
  });

  it("accepts a percent-encoded NUL because it is not a raw control character", () => {
    expect(parseOptionalRelayURI("wss://relay.example.com/tunnel%00segment")).toEqual({
      normalized: "wss://relay.example.com/tunnel%00segment",
    });
  });

  it.each([
    ["zero", "wss://relay.example.com:0/tunnel", { error: "invalid" }],
    ["maximum", "wss://relay.example.com:65535/tunnel", { normalized: "wss://relay.example.com:65535/tunnel" }],
    ["above maximum", "wss://relay.example.com:65536/tunnel", { error: "invalid" }],
    ["large", "wss://relay.example.com:99999/tunnel", { error: "invalid" }],
    ["non-numeric", "wss://relay.example.com:relay/tunnel", { error: "invalid" }],
    ["IPv6 zero", "wss://[2001:db8::1]:0/tunnel", { error: "invalid" }],
    ["IPv6 maximum", "wss://[2001:db8::1]:65535/tunnel", { normalized: "wss://[2001:db8::1]:65535/tunnel" }],
    ["IPv6 above maximum", "wss://[2001:db8::1]:65536/tunnel", { error: "invalid" }],
  ])("matches the server port contract for %s", (_name, raw, expected) => {
    expect(parseOptionalRelayURI(raw)).toEqual(expected);
  });

  it.each([
    ["valid ws", " ws://relay.example.com/tunnel ", { normalized: "ws://relay.example.com/tunnel" }],
    ["valid wss", "wss://relay.example.com/tunnel?region=jp", { normalized: "wss://relay.example.com/tunnel?region=jp" }],
    ["relative", "/tunnel", { error: "invalid" }],
    ["HTTP", "https://relay.example.com/tunnel", { error: "invalid" }],
    ["userinfo", "wss://user:pass@relay.example.com/tunnel", { error: "invalid" }],
    ["empty userinfo", "wss://@relay.example.com/tunnel", { error: "invalid" }],
    ["fragment", "wss://relay.example.com/tunnel#", { error: "invalid" }],
    ["invalid escape", "wss://relay.example.com/tunnel?bad=%zz", { error: "invalid" }],
  ])("matches the server contract for %s", (_name, raw, expected) => {
    expect(parseOptionalRelayURI(raw)).toEqual(expected);
  });

  it("enforces both raw and canonical 2048-byte storage boundaries", () => {
    const prefix = "wss://relay.example/";
    const boundary = `${prefix}${"a".repeat(2048 - prefix.length - "%E7%95%8C".length)}界`;
    const overflow = `${prefix}${"a".repeat(2048 - prefix.length - new TextEncoder().encode("界").length)}界`;

    expect(new TextEncoder().encode(new URL(boundary).toString())).toHaveLength(2048);
    expect(parseOptionalRelayURI(boundary)).toEqual({ normalized: boundary });
    expect(new TextEncoder().encode(overflow)).toHaveLength(2048);
    expect(new TextEncoder().encode(new URL(overflow).toString()).length).toBeGreaterThan(2048);
    expect(parseOptionalRelayURI(overflow)).toEqual({ error: "too_long" });
  });
});
