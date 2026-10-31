import { describe, expect, it } from "vitest";
import { isSafeSiteIconURL } from "./site-branding";

describe("site icon URLs", () => {
  it.each(["", "/brand/icon.svg", "/favicon.png?v=2", "/brand%20icon.svg?version=2#icon", "https://cdn.example.com/icon.png", "http://localhost:18141/icon.svg"])("accepts %j", (value) => {
    expect(isSafeSiteIconURL(value)).toBe(true);
  });

  it.each([
    "//evil.example/icon.svg", "javascript:alert(1)", "data:image/svg+xml,<svg/>",
    "icon.png", "https://user:pass@example.com/icon.png", "https://user@example.com/icon.png",
    "/\\evil.example/icon.svg", "https:\\evil.example/icon.svg", "https:///", " https://example.com/icon.png",
    "https://@example.com/icon.png", "https:///example.com/icon.png", "https:////example.com/icon.png",
    "/icon\n.png", "/icon\r.png", "/icon\t.png", "/icon\u0000.png", "/icon\u007f.png",
    "/icon\u0085.png", "https://example.com/icon\u009f.png",
    "/%2fevil.example/icon.svg", "/%5cicon.png", "/icon%0a.png", "/icon%7f.png", "/icon%C2%85.png",
    "/brand icon.svg", "https://example.com/my icon.png", "/icon\u00a0.png", "/icon%.png",
  ])("rejects %j", (value) => {
    expect(isSafeSiteIconURL(value)).toBe(false);
  });
});
