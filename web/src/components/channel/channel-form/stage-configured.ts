import type { ChannelForm, ChannelOtherSettings } from "./types";
import type { SectionId } from "./section-visibility";
import { parseOtherSettings } from "./utils";

const UPSTREAM_REQUEST_SETTING_KEYS = [
  "claude_beta_query",
  "allow_service_tier",
  "allow_inference_geo",
  "disable_store",
  "allow_safety_identifier",
  "allow_include_obfuscation",
] as const satisfies ReadonlyArray<keyof ChannelOtherSettings>;

export function isStageConfigured(id: SectionId, form: ChannelForm): boolean {
  switch (id) {
    case "meta":
    case "routing":
    case "processing":
      return true;
    case "affinity":
      return form.affinity !== "";
    case "connection": {
      const otherSettings = parseOtherSettings(form.other_settings);
      return !!(
        form.organization ||
        form.api_version ||
        form.proxy_url ||
        form.disable_keepalive ||
        UPSTREAM_REQUEST_SETTING_KEYS.some((key) => !!otherSettings[key])
      );
    }
    case "resilience":
      return form.resilience !== "";
    case "response":
      return form.status_code_mapping !== "" || form.free || (form.price_ratio !== "" && form.price_ratio !== "1");
  }
}
