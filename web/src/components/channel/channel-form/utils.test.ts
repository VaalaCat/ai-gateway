import { expect, it } from "vitest";

import { channelProtocols, parseEndpoints, sanitizeOtherSettingsForSubmit, stringifyEndpoints } from "./utils";

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

it("preserves Gemini endpoint templates and arbitrary query parameters", () => {
  const endpoints = { generate_content: "/proxy/v1beta/models/{model}:{action}?custom=value" };
  expect(parseEndpoints(stringifyEndpoints(endpoints))).toEqual(endpoints);
  expect(channelProtocols(stringifyEndpoints(endpoints))).toEqual({
    claude: false, openaiChat: false, openaiResponses: false, gemini: true,
  });
});

it("keeps Chat and Claude per-model conversions to a Gemini-only upstream", () => {
  const settings = {
    protocol_override: { openai_chat: "gemini", claude: "gemini" },
    model_protocol_override: [
      { model: "custom-.*", overrides: { openai_chat: "gemini" } },
      { model: "other-.*", overrides: { claude: "gemini" } },
    ],
    unknown_setting: { anything: true },
  };
  expect(JSON.parse(sanitizeOtherSettingsForSubmit(JSON.stringify(settings), JSON.stringify({
    generate_content: "/v1beta/models/{model}:{action}",
  })))).toEqual(settings);
});

it("keeps ordered compatibility rules including explicit exclusions when saving", () => {
  const rules = [
    { model_pattern: "^gemini-exception$", profile: "none" },
    { model_pattern: "^gemini-.*$", profile: "gemini_compat" },
    { model_pattern: "^unsigned-", profile: "none", signature_fallback: "gemini" },
    { model_pattern: "^compat-", profile: "gemini_compat", signature_fallback: "gemini" },
    { model_pattern: "^plain-", profile: "none", signature_fallback: "none" },
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
  { model_pattern: "gemini", profile: "none", signature_fallback: "unknown" },
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
