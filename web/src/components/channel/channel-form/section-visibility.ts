// web/src/components/channel/channel-form/section-visibility.ts
import type { ChannelForm } from "./types";

export type SectionId =
  | "meta"
  | "routing"
  | "affinity"
  | "processing"
  | "connection"
  | "resilience"
  | "response";

export const SECTION_FIELDS: Record<SectionId, ReadonlyArray<keyof ChannelForm>> = {
  meta: ["name", "key", "base_url", "tag", "remark", "use_legacy_adaptor"],
  routing: [
    "status", "weight", "priority", "models", "test_model",
    "supported_api_types", "limit", "auto_ban",
  ],
  affinity: ["affinity"],
  processing: [
    "model_mapping", "system_prompt", "role_mapping", "param_override",
    "header_override", "endpoints", "passthrough_enabled",
    "system_prompt_in_input", "other_settings",
  ],
  connection: ["organization", "api_version", "proxy_url", "disable_keepalive", "other_settings"],
  resilience: ["resilience"],
  response: ["status_code_mapping", "free", "price_ratio"],
};

export function isSectionAllHidden(
  id: SectionId,
  hidden?: ReadonlySet<keyof ChannelForm>,
): boolean {
  if (!hidden || hidden.size === 0) return false;
  return SECTION_FIELDS[id].every((f) => hidden.has(f));
}
