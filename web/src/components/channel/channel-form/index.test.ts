import { expect, it } from "vitest";

import { isStageConfigured } from "./stage-configured";
import { emptyForm } from "./types";

it("marks upstream request stage configured for request field policies", () => {
  expect(isStageConfigured("connection", {
    ...emptyForm,
    other_settings: JSON.stringify({ allow_service_tier: true }),
  })).toBe(true);
});

it("does not count processing-only other settings as upstream request config", () => {
  expect(isStageConfigured("connection", {
    ...emptyForm,
    other_settings: JSON.stringify({ inline_image_url: true, builtin_tool_fallback: "function" }),
  })).toBe(false);
});

it("keeps an empty upstream request stage unconfigured", () => {
  expect(isStageConfigured("connection", emptyForm)).toBe(false);
});
