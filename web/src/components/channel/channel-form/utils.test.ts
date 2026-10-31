import { expect, it } from "vitest";

import { parseEndpoints, sanitizeOtherSettingsForSubmit } from "./utils";

it("returns an empty object for an empty string", () => {
  expect(parseEndpoints("")).toEqual({});
});

it("returns an empty object when the stored value is the JSON literal null", () => {
  // Some channel rows have `endpoints` stored as the 4-char string "null" rather than
  // an empty string. JSON.parse("null") succeeds and returns null, so parseEndpoints
  // must not trust the parsed result blindly, or callers indexing into it (e.g.
  // channels/page.tsx) crash with "Cannot read properties of null".
  expect(parseEndpoints("null")).toEqual({});
});

it("returns an empty object for a JSON array", () => {
  expect(parseEndpoints("[]")).toEqual({});
});

it("returns the parsed object for a valid endpoint config", () => {
  expect(parseEndpoints('{"chat_completions":"/v1/chat/completions"}')).toEqual({
    chat_completions: "/v1/chat/completions",
  });
});

it("keeps ordered compatibility rules including explicit exclusions when saving", () => {
  const rules = [
    { model_pattern: "^gemini-exception$", profile: "none" },
    { model_pattern: "^gemini-.*$", profile: "gemini_compat" },
  ];
  const raw = JSON.stringify({ model_tool_schema_compatibility: rules, inline_image_url: true });
  expect(JSON.parse(sanitizeOtherSettingsForSubmit(raw, ""))).toEqual({
    model_tool_schema_compatibility: rules,
    inline_image_url: true,
  });
});

it.each([
  { model_pattern: "", profile: "gemini_compat" },
  { model_pattern: "gemini", profile: "unknown" },
])("does not save an unfinished compatibility rule: %j", (rule) => {
  const raw = JSON.stringify({ model_tool_schema_compatibility: [rule], inline_image_url: true });
  expect(JSON.parse(sanitizeOtherSettingsForSubmit(raw, ""))).toEqual({ inline_image_url: true });
});

it.each(["(?i)^gemini-", "(?=gemini)", "["])("sends the original pattern %s for Go regex validation", (modelPattern) => {
  const rules = [{ model_pattern: modelPattern, profile: "gemini_compat" }];
  expect(JSON.parse(sanitizeOtherSettingsForSubmit(JSON.stringify({
    model_tool_schema_compatibility: rules,
  }), ""))).toEqual({ model_tool_schema_compatibility: rules });
});
