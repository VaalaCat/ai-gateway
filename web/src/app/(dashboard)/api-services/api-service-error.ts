import { ApiError } from "@/lib/api/client";

type Translate = (key: string, values?: Record<string, number | string>) => string;

const messageKeyByCode: Record<string, string> = {
  backend_not_found: "targetNotFound",
  backend_service_mismatch: "targetServiceMismatch",
  preview_invalid_target: "routingPreviewValidationFailed",
  no_available_upstream: "noAvailableUpstream",
  backend_id_immutable: "backendImmutable",
  backend_name_conflict: "backendNameConflict",
  invalid_example_subpath: "routingPreviewValidationFailed",
  invalid_example_request: "routingPreviewValidationFailed",
  service_slug_conflict: "openAPIServiceSlugConflict",
  server_selection_required: "openAPIServerSelectionRequired",
  selected_server_not_found: "openAPISelectedServerInvalid",
  invalid_selected_server: "openAPISelectedServerInvalid",
  invalid_openapi: "openAPIInvalidDocument",
  request_too_large: "openAPIRequestTooLarge",
  sync_publish_failed: "openAPISyncPublishFailed",
  upstream_url_required: "openAPIUpstreamURLRequired",
};

export function apiServiceErrorMessage(t: Translate, reason: unknown, fallbackKey = "mutationFailed") {
  if (reason instanceof ApiError) {
    const code = typeof reason.body?.code === "string" ? reason.body.code : "";
    if (code === "sync_publish_failed") {
      const details = reason.body?.details;
      const serviceID = typeof details === "object" && details !== null && "service_id" in details
        && typeof details.service_id === "number"
        ? details.service_id
        : 0;
      if (!Number.isSafeInteger(serviceID) || serviceID <= 0) return t(fallbackKey);
    }
    if (messageKeyByCode[code]) return t(messageKeyByCode[code]);
    return t(reason.status === 403 ? "permissionDenied" : fallbackKey);
  }
  return reason instanceof Error ? reason.message : t(fallbackKey);
}

export function apiBackendDeleteErrorMessage(t: Translate, reason: unknown) {
  if (reason instanceof ApiError && reason.body?.code === "backend_in_use") {
    const details = reason.body.details as { route_count?: number } | undefined;
    return t("backendInUse", { count: Number(details?.route_count ?? 0) });
  }
  if (reason instanceof ApiError && reason.body?.code === "backend_name_conflict") return t("backendNameConflict");
  return reason instanceof Error ? reason.message : t("mutationFailed");
}

export function apiServiceDiagnosticMessage(t: Translate, code: string) {
  return t(messageKeyByCode[code] ?? "routingPreviewUnknownDiagnostic");
}
