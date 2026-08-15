export const genericAPISettingDefinitions = [
  {
    key: "api_usage_queue_capacity",
    labelKey: "apiUsageQueueCapacity",
    descriptionKey: "apiUsageQueueCapacityDesc",
    defaultValue: "10000",
    min: 100,
    max: 1_000_000,
  },
  {
    key: "api_usage_worker_concurrency",
    labelKey: "apiUsageWorkerConcurrency",
    descriptionKey: "apiUsageWorkerConcurrencyDesc",
    defaultValue: "2",
    min: 1,
    max: 32,
  },
  {
    key: "api_upload_idle_timeout_ms",
    labelKey: "apiUploadIdleTimeoutMs",
    descriptionKey: "apiUploadIdleTimeoutMsDesc",
    defaultValue: "0",
    min: 0,
    max: 3_600_000,
    kind: "milliseconds",
  },
  {
    key: "api_upstream_dial_timeout_ms",
    labelKey: "apiUpstreamDialTimeoutMs",
    descriptionKey: "apiUpstreamDialTimeoutMsDesc",
    defaultValue: "30000",
    min: 0,
    max: 3_600_000,
    kind: "milliseconds",
  },
  {
    key: "api_upstream_tls_handshake_timeout_ms",
    labelKey: "apiUpstreamTLSHandshakeTimeoutMs",
    descriptionKey: "apiUpstreamTLSHandshakeTimeoutMsDesc",
    defaultValue: "10000",
    min: 0,
    max: 3_600_000,
    kind: "milliseconds",
  },
  {
    key: "api_upstream_response_header_timeout_ms",
    labelKey: "apiUpstreamResponseHeaderTimeoutMs",
    descriptionKey: "apiUpstreamResponseHeaderTimeoutMsDesc",
    defaultValue: "0",
    min: 0,
    max: 3_600_000,
    kind: "milliseconds",
  },
  {
    key: "api_websocket_handshake_timeout_ms",
    labelKey: "apiWebSocketHandshakeTimeoutMs",
    descriptionKey: "apiWebSocketHandshakeTimeoutMsDesc",
    defaultValue: "45000",
    min: 0,
    max: 3_600_000,
    kind: "milliseconds",
  },
  {
    key: "api_websocket_control_write_timeout_ms",
    labelKey: "apiWebSocketControlWriteTimeoutMs",
    descriptionKey: "apiWebSocketControlWriteTimeoutMsDesc",
    defaultValue: "5000",
    min: 0,
    max: 3_600_000,
    kind: "milliseconds",
  },
] as const;

export type GenericAPISettingKey =
  (typeof genericAPISettingDefinitions)[number]["key"];

export type GenericAPISettingValues = Record<GenericAPISettingKey, string>;

export type GenericAPISettingDraft = Partial<GenericAPISettingValues>;

export type GenericAPISettingUpdateResult =
  | { ok: true; updates: GenericAPISettingDraft }
  | { ok: false; invalidKey: GenericAPISettingKey };

export function currentGenericAPISettingValues(
  settings: Readonly<Record<string, string>>,
): GenericAPISettingValues {
  return Object.fromEntries(
    genericAPISettingDefinitions.map((definition) => [
      definition.key,
      settings[definition.key] ?? definition.defaultValue,
    ]),
  ) as GenericAPISettingValues;
}

function isIntegerInRange(value: string, min: number, max: number): boolean {
  if (value.trim() === "") return false;

  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed >= min && parsed <= max;
}

export function buildGenericAPISettingUpdates(
  current: GenericAPISettingValues,
  draft: GenericAPISettingDraft,
): GenericAPISettingUpdateResult {
  for (const definition of genericAPISettingDefinitions) {
    const value = draft[definition.key] ?? current[definition.key];
    if (!isIntegerInRange(value, definition.min, definition.max)) {
      return { ok: false, invalidKey: definition.key };
    }
  }

  return { ok: true, updates: draft };
}
