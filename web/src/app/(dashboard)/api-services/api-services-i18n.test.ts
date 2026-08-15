import { createTranslator } from "next-intl";
import { describe, expect, it } from "vitest";

import en from "@/i18n/en.json";
import zh from "@/i18n/zh.json";

function lookup(messages: unknown, path: string) {
  return path.split(".").reduce<unknown>((value, key) => {
    if (typeof value !== "object" || value === null || !(key in value)) return undefined;
    return (value as Record<string, unknown>)[key];
  }, messages);
}

const routeTableKeys = [
  "requestURL", "protocolsAndStatus", "routeSearch", "refreshingRoutes",
  "targetLoadFailed", "copyClientRequest", "routeActions", "createRoute",
  "routeOutsideCurrentPage", "routeOutsideCurrentPageDescription", "clearRouteFilters",
  "routesLabel", "routesLoadFailed", "routesLoadFailedDescription",
] as const;

describe("apiServices runtime messages", () => {
  it.each([["en", en], ["zh", zh]] as const)("resolves route table messages for %s", (locale, messages) => {
    const errors: unknown[] = [];
    const t = createTranslator({ locale, messages, namespace: "apiServices", onError: (error) => errors.push(error) });
    for (const key of routeTableKeys) {
      const value = lookup(messages, `apiServices.${key}`);
      expect(value, `${locale}.apiServices.${key}`).toEqual(expect.any(String));
      expect((value as string).trim(), `${locale}.apiServices.${key} empty`).not.toBe("");
      expect(t(key), `${locale}.${key} unresolved`).not.toBe(key);
    }
    expect(t("expandRoute", { route: "forecast" })).toBeTruthy();
    expect(t("collapseRoute", { route: "forecast" })).toBeTruthy();
    expect(t("endpointCount", { count: 2 })).toBeTruthy();
    expect(errors, `${locale} missing Route table messages`).toEqual([]);
  });

  it.each([["en", en], ["zh", zh]] as const)("resolves every expanded Route workspace message for %s", (locale, messages) => {
    const errors: unknown[] = [];
    const t = createTranslator({ locale, messages, namespace: "apiServices", onError: (error) => errors.push(error) });
    const plainKeys = [
      "targetLoadFailedDescription",
      "routeTargetMissing",
      "routeTargetMissingDescription",
      "addEndpoint",
      "noEndpoints",
      "noEndpointsDescription",
      "endpointsLoadFailed",
      "endpointsLoadFailedDescription",
      "targetReturns503",
      "targetReturns503Description",
      "configuredEndpointPreviewFailed",
      "configuredEndpointPreviewFailedDescription",
      "configuredEndpointURLUnavailable",
      "targetLoading",
      "endpointsLoading",
      "configuredEndpointURLLoading",
    ] as const;
    for (const key of plainKeys) {
      const value = t(key);
      expect(value, `${locale}.${key}`).toBeTruthy();
      expect(value, `${locale}.${key} unresolved`).not.toBe(key);
    }
    expect(t("endpointCount", { count: 2 })).toBeTruthy();
    expect(t("copyEndpointURL", { name: "Primary Endpoint" })).toContain("Primary Endpoint");
    expect(errors, `${locale} missing expanded Route workspace messages`).toEqual([]);
  });

  it.each([["en", en], ["zh", zh]] as const)("formats overview labels and counts for %s", (locale, messages) => {
    const namespace = messages.apiServices as Record<string, string>;
    for (const key of ["routesLabel", "refreshingServices"]) {
      expect(namespace[key], `${locale}.${key}`).toBeTypeOf("string");
    }
    const t = createTranslator({ locale, messages, namespace: "apiServices" });
    expect(t("routesLabel")).toBeTruthy();
    expect(t("routeCount", { count: 0 })).toBeTruthy();
    expect(t("upstreamCount", { count: 0 })).toBeTruthy();
  });

  it.each([["en", en], ["zh", zh]] as const)("formats every route invocation message for %s", (locale, messages) => {
    const errors: unknown[] = [];
    const t = createTranslator({ locale, messages, namespace: "apiServices", onError: (error) => errors.push(error) });
    const plainKeys = [
      "targets", "apiAccess", "apiLogs", "baseUrlLoading", "copyBaseUrl",
      "forwardSubpathEnabled", "forwardSubpathDisabled",
      "targetLoadFailed", "endpointUnavailable503", "endpointUnavailableDescription",
      "viewServiceDetails", "copyCommand", "copyCommandWithToken",
      "copyCommandOptions", "chooseInvocationToken", "chooseInvocationTokenDescription",
      "copyTemplateCommand", "copyCurlCommand", "copyWebsocatCommand",
      "copyCurlTemplateCommand", "copyWebsocatTemplateCommand", "copyAndRememberToken", "openAPIAccess",
      "changeInvocationToken", "templateCommandCopied", "copyCommandFailed",
      "routingChainTitle", "routingChainDescription", "clientNode", "clientRequest",
      "gatewayForwarding", "copyClientRequest", "copyGatewayForwarding", "endpointSummary",
      "noEndpointCandidates",
      "routeNode", "pathNode", "targetNode", "endpointNode", "exampleMethodUnset",
      "routingPreviewLoading", "routingPreviewFailed", "routingPreviewFailedDescription",
      "retryPreview", "routingPreviewEmpty", "routingPreviewEmptyDescription",
      "routingPreviewStaticOnlyDisabled", "routingPreviewUnknownDiagnostic",
      "invocationTokenChecking", "invocationTokenValidationFailed",
      "invocationTokenNoLongerAllowed",
      "headers", "requestHeadersJSONInvalid", "requestDetails", "previewInvocation", "collapseInvocationPreview",
    ] as const;
    for (const key of plainKeys) expect(t(key), `${locale}.${key}`).toBeTruthy();
    expect(t("enabledEndpointCount", { count: 1 })).toBeTruthy();
    expect(t("staticCandidateCount", { count: 2 })).toBeTruthy();
    expect(t("commandCopiedWithToken", { name: "Production Token" })).toBeTruthy();
    expect(t("copyEndpointURL", { name: "primary" })).toBeTruthy();
    expect(errors, `${locale} missing runtime messages`).toEqual([]);
  });

  it.each([["en", en], ["zh", zh]] as const)("resolves the complete control-plane experience messages for %s", (locale, messages) => {
    const errors: unknown[] = [];
    const t = createTranslator({ locale, messages, namespace: "apiServices", onError: (error) => errors.push(error) });
    const plainKeys = [
      "target", "targets", "endpoints", "createTarget", "editTarget", "deleteTarget",
      "createEndpoint", "copyEndpoint", "backendLocked", "backendImmutable",
      "routingPreview", "routingPreviewDiagnostics", "copyCommand", "copyTemplateCommand",
      "invocationTokenNoLongerAllowed", "sharedTargetImpactDescription", "backendNameConflict",
      "noAvailableUpstream", "confirmDisableEndpointTitle", "confirmDisableEndpoint",
      "confirmDeleteLastEndpointTitle", "confirmDeleteEndpoint", "headerOverride",
      "invalidHeaderOverride", "targetsLoadFailed", "targetNotFound",
      "configureRequestExample", "requestExampleDescription", "exampleMethod", "exampleSubpath",
      "exampleQuery", "exampleBody", "clearRequestExample", "invalidExampleQuery",
      "targetServiceMismatch", "targetRoutesLoading", "targetRoutesLoadFailed", "targetRoutesLoadFailedDescription",
      "endpointPreviewRequired", "endpointPreviewInvalid", "endpointPreviewPreparing", "endpointPreviewLoading", "endpointPreviewFailed", "endpointPreviewFailedDescription",
      "retryEndpointPreview", "endpointPreviewEmpty", "endpointPreviewEmptyDescription",
    ] as const;
    for (const key of plainKeys) {
      const value = t(key);
      expect(value, `${locale}.${key}`).toBeTruthy();
      expect(value, `${locale}.${key} unresolved`).not.toBe(key);
    }
    expect(t("routeCount", { count: 2 })).toBeTruthy();
    expect(t("targetCount", { count: 2 })).toBeTruthy();
    expect(t("backendInUse", { count: 2 })).toBeTruthy();
    expect(t("saveTargetImpact", { count: 2 })).toBeTruthy();
    expect(t("lastEndpointDisableDescription", { count: 2 })).toBeTruthy();
    expect(t("lastEndpointDeleteDescription", { count: 2 })).toBeTruthy();
    expect(t("bodySize", { bytes: 1024 })).toBeTruthy();
    expect(t("weightPriority", { weight: 1, priority: 0 })).toBeTruthy();
    expect(t("enabledEndpointCount", { count: 1 }), `${locale}.enabledEndpointCount`).toContain("1");
    expect(errors, `${locale} missing runtime messages`).toEqual([]);
  });

  it.each([["en", en], ["zh", zh]] as const)("states the actual 64-character URL-safe Route path limit for %s", (locale, messages) => {
    const errors: unknown[] = [];
    const t = createTranslator({ locale, messages, namespace: "apiServices", onError: (error) => errors.push(error) });
    expect(t("invalidRouteSlug")).toContain("64");
    expect(t("invalidRouteSlug").toLowerCase()).toContain("url");
    expect(t("pathDescription")).toContain("64");
    expect(t("pathDescription").toLowerCase()).toContain("url");
    for (const key of ["invalidTargetName", "endpointNameRequired", "endpointUrlRequired", "credentialRequired", "invalidProxyUrl", "invalidHeaderOverride"] as const) expect(t(key)).toBeTruthy();
    expect(errors, `${locale} missing Route editor messages`).toEqual([]);
  });

  it.each([["en", en], ["zh", zh]] as const)("resolves channel stage status messages for %s", (locale, messages) => {
    const errors: unknown[] = [];
    const t = createTranslator({ locale, messages, namespace: "channels", onError: (error) => errors.push(error) });
    expect(t("stageConfigured")).toBeTruthy();
    expect(t("stageNotConfigured")).toBeTruthy();
    expect(errors, `${locale} missing runtime messages`).toEqual([]);
  });

  it("keeps the Route table workspace key set non-empty and exactly aligned", () => {
    const requiredKeys = [
      "routesLabel", "routeActions", "targetActions", "endpointActions",
      "copyBaseUrl", "copyEndpointConfig", "copyEndpointURL",
      "pathMapping", "publicRequestResult", "upstreamRequestResults", "requestPolicy",
      "serviceBaseUrlResult", "serviceBaseUrlDescription", "targetCurrentImpact", "targetImpactSummary",
      "targetNoRoutes", "targetRoutesLoading", "targetRoutesLoadFailed", "targetRoutesLoadFailedDescription",
      "endpointRouteResults", "endpointRouteResultsEmpty", "endpointPreviewRequired", "endpointPreviewInvalid",
      "endpointPreviewPreparing", "endpointPreviewLoading", "endpointPreviewFailed", "endpointPreviewFailedDescription",
      "retryEndpointPreview", "endpointPreviewEmpty", "endpointPreviewEmptyDescription",
    ] as const;

    for (const [locale, messages] of [["en", en], ["zh", zh]] as const) {
      for (const key of requiredKeys) {
        const value = lookup(messages, `apiServices.${key}`);
        expect(value, `${locale}.apiServices.${key}`).toEqual(expect.any(String));
        expect((value as string).trim(), `${locale}.apiServices.${key} empty`).not.toBe("");
      }
    }

    expect(new Set(Object.keys(zh.apiServices))).toEqual(new Set(Object.keys(en.apiServices)));
  });

  const retiredRouteMapAndSheetKeys = [
      "routeMap",
      "viewInvocation",
      "routePreviewSummary",
      "forwardToTarget",
      "routeMapEmptyTitle",
      "routeMapEmptyDescription",
      "routeMapIncomplete",
      "routeMapIncompleteDescription",
      "copyEndpointAddress",
      "invocationPreviewTitle",
      "invocationPreviewDescription",
      "catalogInvocationPreviewDescription",
      "routeSummary",
      "targetSummary",
      "internalTargetHidden",
      "staticPreviewDetails",
      "staticPreview",
      "staticCandidateDescription",
      "noRetryOrFallback",
      "noRetryOrFallbackDescription",
      "adjustRequest",
      "invocationCommand",
      "targetUnused",
      "targetHasNoEndpoint503",
      "targetHasNoEndpointDescription",
  ] as const;

  it("does not retain messages exclusively owned by the retired Hover, Route Map, and Sheet surfaces", () => {

    for (const [locale, messages] of [["en", en], ["zh", zh]] as const) {
      for (const key of retiredRouteMapAndSheetKeys) {
        expect(lookup(messages, `apiServices.${key}`), `${locale}.apiServices.${key} must not preserve a dead UI contract`).toBeUndefined();
      }
    }
  });
});
