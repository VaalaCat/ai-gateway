import { describe, expect, it } from "vitest";

import {
  buildGenericAPISettingUpdates,
  currentGenericAPISettingValues,
  genericAPISettingDefinitions,
  type GenericAPISettingDraft,
} from "./generic-api-settings";

describe("currentGenericAPISettingValues", () => {
  it("loads persisted strings and fills every missing default", () => {
    const values = currentGenericAPISettingValues({
      api_usage_queue_capacity: "12345",
      api_websocket_control_write_timeout_ms: "27",
    });

    expect(values).toMatchObject({
      api_usage_queue_capacity: "12345",
      api_usage_worker_concurrency: "2",
      api_upstream_dial_timeout_ms: "30000",
      api_websocket_control_write_timeout_ms: "27",
    });
  });
});

describe("buildGenericAPISettingUpdates", () => {
  it("accepts queue maxima and timeout zero/max boundaries", () => {
    const current = currentGenericAPISettingValues({});
    const draft: GenericAPISettingDraft = {
      api_usage_queue_capacity: "1000000",
      api_usage_worker_concurrency: "32",
      api_upload_idle_timeout_ms: "0",
      api_upstream_dial_timeout_ms: "3600000",
      api_upstream_tls_handshake_timeout_ms: "0",
      api_upstream_response_header_timeout_ms: "3600000",
      api_websocket_handshake_timeout_ms: "0",
      api_websocket_control_write_timeout_ms: "3600000",
    };

    expect(buildGenericAPISettingUpdates(current, draft)).toEqual({
      ok: true,
      updates: draft,
    });
  });

  const invalidCases = genericAPISettingDefinitions.flatMap((definition) => [
    [definition.key, ""],
    [definition.key, "not-a-number"],
    [definition.key, "1.5"],
    [definition.key, String(definition.min - 1)],
    [definition.key, String(definition.max + 1)],
  ] as const);

  it.each(invalidCases)("rejects invalid integer boundary %s=%j", (key, value) => {
    const current = currentGenericAPISettingValues({});
    expect(buildGenericAPISettingUpdates(current, { [key]: value })).toEqual({
      ok: false,
      invalidKey: key,
    });
  });
});
