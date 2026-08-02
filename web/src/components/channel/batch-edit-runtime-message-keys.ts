export const batchFieldGroupMessageKeys = [
  "batchEditGroupBasic",
  "batchEditGroupRouting",
  "batchEditGroupProcessing",
  "batchEditGroupConnection",
] as const;

export const batchFieldMessageKeys = [
  "publicDisplayName", "type", "apiKey", "baseUrl", "tag", "remark", "batchEditFieldStatus", "models",
  "weight", "priority", "testModel", "autoBan", "usageLimit", "batchEditFieldAffinity", "modelMapping",
  "systemPrompt", "roleMapping", "paramOverride", "headerOverride", "supportedApiTypes", "sectionEndpoints",
  "passthroughEnabled", "fieldSystemPromptInInput", "useLegacyAdaptor", "setting", "otherSettings", "proxy",
  "organization", "apiVersion", "disableKeepalive", "statusCodeMapping", "resilienceOverride", "priceRatio", "free",
] as const;

export const channelBatchRuntimeMessageKeys = [
  ...batchFieldMessageKeys,
  ...batchFieldGroupMessageKeys,
  "selectedChannels",
  "batchEdit",
  "clearSelection",
  "enableChannel",
  "disableChannel",
  "channelStatusUpdating",
  "publicDisplayNamePreview",
  "publicDisplayNameAutoPreview",
  "batchEditTitle",
  "batchEditDescription",
  "batchEditApply",
  "batchEditApplying",
  "batchEditFieldHint",
  "noBatchFieldSelected",
  "batchEditUpdateField",
  "batchEditSuccess",
  "batchEditFailed",
  "batchEditSelectAtLeastOne",
  "batchEditInvalidId",
  "batchEditMaxIds",
  "publicDisplayNameTooLong",
  "publicDisplayNameControlCharacters",
] as const;

export type BatchFieldGroupMessageKey = (typeof batchFieldGroupMessageKeys)[number];
export type BatchFieldMessageKey = (typeof batchFieldMessageKeys)[number];
