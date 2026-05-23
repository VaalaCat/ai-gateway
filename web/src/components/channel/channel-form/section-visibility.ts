// web/src/components/channel/channel-form/section-visibility.ts
import type { ChannelForm } from "./types";

export type SectionId =
  | "basic"
  | "endpoints-protocol"
  | "models"
  | "protocol-behavior"
  | "request-rewrite"
  | "route-roles";

/**
 * Fields each section owns. When every field of a section is in
 * adapter.hiddenFields, the section's Tab is suppressed entirely.
 */
export const SECTION_FIELDS: Record<SectionId, ReadonlyArray<keyof ChannelForm>> = {
  basic: [
    "name",
    "type",
    "key",
    "base_url",
    "weight",
    "priority",
    "status",
    "tag",
    "remark",
    "use_legacy_adaptor",
  ],
  "endpoints-protocol": [
    "endpoints",
    "supported_api_types",
    "passthrough_enabled",
    "api_version",
    "organization",
  ],
  models: ["models", "model_mapping", "test_model"],
  "protocol-behavior": [
    "system_prompt",
    "system_prompt_in_input",
    "setting",
    "other_settings",
    "auto_ban",
    "status_code_mapping",
  ],
  "request-rewrite": ["param_override", "header_override", "proxy_url"],
  "route-roles": ["role_mapping"],
};

export function isSectionAllHidden(
  id: SectionId,
  hidden?: ReadonlySet<keyof ChannelForm>,
): boolean {
  if (!hidden || hidden.size === 0) return false;
  return SECTION_FIELDS[id].every((f) => hidden.has(f));
}
