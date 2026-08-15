import { describe, expect, it } from "vitest";

import { ApiError } from "@/lib/api/client";

import { apiServiceErrorMessage } from "./api-service-error";

const t = (key: string) => `translated:${key}`;

describe("apiServiceErrorMessage", () => {
  it.each([
    ["backend_not_found", "targetNotFound"],
    ["backend_service_mismatch", "targetServiceMismatch"],
    ["preview_invalid_target", "routingPreviewValidationFailed"],
    ["no_available_upstream", "noAvailableUpstream"],
  ])("translates the stable %s API error code without exposing its server message", (code, key) => {
    const error = new ApiError(400, "server wording must not reach the operator", { code });

    expect(apiServiceErrorMessage(t, error)).toBe(`translated:${key}`);
  });

  it("uses a local fallback for an unknown server code and preserves client-side errors", () => {
    expect(apiServiceErrorMessage(t, new ApiError(400, "untrusted server wording", { code: "future_code" }))).toBe("translated:mutationFailed");
    expect(apiServiceErrorMessage(t, new Error("network unavailable"))).toBe("network unavailable");
  });
});
