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
    ["service_slug_conflict", "openAPIServiceSlugConflict"],
    ["server_selection_required", "openAPIServerSelectionRequired"],
    ["selected_server_not_found", "openAPISelectedServerInvalid"],
    ["invalid_selected_server", "openAPISelectedServerInvalid"],
    ["invalid_openapi", "openAPIInvalidDocument"],
    ["request_too_large", "openAPIRequestTooLarge"],
  ])("translates the stable %s API error code without exposing its server message", (code, key) => {
    const error = new ApiError(400, "server wording must not reach the operator", { code });

    expect(apiServiceErrorMessage(t, error)).toBe(`translated:${key}`);
  });

  it("uses a local fallback for an unknown server code and preserves client-side errors", () => {
    expect(apiServiceErrorMessage(t, new ApiError(400, "untrusted server wording", { code: "future_code" }))).toBe("translated:mutationFailed");
    expect(apiServiceErrorMessage(t, new Error("network unavailable"))).toBe("network unavailable");
  });

  it("uses the caller fallback for unknown API errors and localizes a generic forbidden response", () => {
    expect(apiServiceErrorMessage(t, new ApiError(500, "untrusted server wording", { code: "future_code" }), "openAPIImportFailed")).toBe("translated:openAPIImportFailed");
    expect(apiServiceErrorMessage(t, new ApiError(403, "untrusted forbidden wording"), "openAPIPreviewFailed")).toBe("translated:permissionDenied");
    expect(apiServiceErrorMessage(t, new Error("network unavailable"), "openAPIImportFailed")).toBe("network unavailable");
  });

  it("localizes sync publication failure only when it identifies the committed service", () => {
    expect(apiServiceErrorMessage(t, new ApiError(500, "untrusted sync wording", {
      code: "sync_publish_failed",
      details: { service_id: 42 },
    }), "openAPIImportFailed")).toBe("translated:openAPISyncPublishFailed");
    expect(apiServiceErrorMessage(t, new ApiError(500, "untrusted sync wording", {
      code: "sync_publish_failed",
      details: { service_id: 0 },
    }), "openAPIImportFailed")).toBe("translated:openAPIImportFailed");
  });
});
