import type { Channel } from "@/lib/types";
import {
  ChannelForm,
  ChannelOtherSettings,
  ChannelProtocols,
  ChannelSettings,
  EndpointConfig,
  emptyForm,
} from "./types";

/* ── parseSetting / stringifySetting (existing logic) ─────────────────── */

export function parseSetting(raw: string): ChannelSettings {
  if (!raw) return {};
  try {
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

export function stringifySetting(s: ChannelSettings): string {
  const cleaned = Object.fromEntries(
    Object.entries(s).filter(([, v]) => v !== false && v !== "" && v !== undefined)
  );
  return Object.keys(cleaned).length ? JSON.stringify(cleaned) : "";
}

/* ── parseOtherSettings / stringifyOtherSettings ─────────────────────── */

export function parseOtherSettings(raw: string): ChannelOtherSettings {
  if (!raw) return {};
  try {
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

export function stringifyOtherSettings(s: ChannelOtherSettings): string {
  const cleaned = Object.fromEntries(
    Object.entries(s).filter(
      ([, v]) => v !== false && v !== "" && v !== undefined && v !== null
    )
  );
  return Object.keys(cleaned).length ? JSON.stringify(cleaned) : "";
}

/* ── parseEndpoints / stringifyEndpoints ─────────────────────────────── */

export function parseEndpoints(raw: string): EndpointConfig {
  if (!raw) return {};
  try {
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

export function stringifyEndpoints(ep: EndpointConfig): string {
  const cleaned = Object.fromEntries(
    Object.entries(ep).filter(([, v]) => v !== undefined && v !== "")
  );
  return Object.keys(cleaned).length ? JSON.stringify(cleaned) : "";
}

/* ── channelProtocols ────────────────────────────────────────────────── */

export function channelProtocols(endpoints: string): ChannelProtocols {
  const eps = parseEndpoints(endpoints);
  return {
    claude: !!eps.messages,
    openaiChat: !!eps.chat_completions,
    openaiResponses: !!eps.responses,
  };
}

/* ── sanitizeOtherSettingsForSubmit (move verbatim from channels page) ─ */

/**
 * Drop `protocol_override` entries that are auto / identity / target endpoint
 * no longer enabled. Drop empty `model_protocol_override` rules. Drop empty
 * or invalid-regex `model_thinking_passthrough` rules.
 */
export function sanitizeOtherSettingsForSubmit(
  rawOtherSettings: string,
  endpoints: string
): string {
  if (!rawOtherSettings) return rawOtherSettings;
  let parsed: ChannelOtherSettings;
  try {
    parsed = JSON.parse(rawOtherSettings);
  } catch {
    return rawOtherSettings;
  }
  const sanitized: ChannelOtherSettings = { ...parsed };
  if (sanitized.protocol_override) {
    const protos = channelProtocols(endpoints);
    const cleaned: Record<string, "openai_chat" | "openai_responses" | "claude"> = {};
    for (const [inbound, target] of Object.entries(sanitized.protocol_override)) {
      if (!target || target === "auto" || target === inbound) continue;
      if (target === "openai_chat" && !protos.openaiChat) continue;
      if (target === "openai_responses" && !protos.openaiResponses) continue;
      if (target === "claude" && !protos.claude) continue;
      cleaned[inbound] = target;
    }
    if (Object.keys(cleaned).length === 0) {
      delete sanitized.protocol_override;
    } else {
      sanitized.protocol_override = cleaned as typeof sanitized.protocol_override;
    }
  }
  if (sanitized.model_protocol_override) {
    const protos = channelProtocols(endpoints);
    const enabled = new Set<string>();
    if (protos.openaiChat) enabled.add("openai_chat");
    if (protos.openaiResponses) enabled.add("openai_responses");
    if (protos.claude) enabled.add("claude");

    const cleanedRules = sanitized.model_protocol_override.filter((rule) => {
      if (!rule.model) return false;
      try {
        new RegExp("^" + rule.model + "$");
      } catch {
        return false;
      }

      const validPairs = Object.entries(rule.overrides).filter(([inb, outb]) => {
        if (!outb || outb === "auto") return false;
        if (outb === inb) return false; // identity drop
        if (!enabled.has(outb as string)) return false;
        // inbound: '*' is always valid; specific inbound must be enabled
        return inb === "*" || enabled.has(inb as string);
      });
      return validPairs.length > 0;
    });
    if (cleanedRules.length === 0) {
      delete sanitized.model_protocol_override;
    } else {
      sanitized.model_protocol_override = cleanedRules;
    }
  }
  if (sanitized.model_thinking_passthrough) {
    const cleaned = sanitized.model_thinking_passthrough.filter((rule) => {
      if (!rule.model_pattern) return false;
      try {
        new RegExp(rule.model_pattern); // 后端 Go regexp.MatchString 部分匹配语义，不加 ^$
      } catch {
        return false;
      }
      return true;
    });
    if (cleaned.length === 0) {
      delete sanitized.model_thinking_passthrough;
    } else {
      sanitized.model_thinking_passthrough = cleaned;
    }
  }
  return stringifyOtherSettings(sanitized);
}

/* ── mapChannelToForm (used by edit-mode initial fill) ───────────────── */

export function mapChannelToForm(channel: Channel): ChannelForm {
  return {
    ...emptyForm,
    name: channel.name ?? "",
    type: String(channel.type ?? 1),
    key: channel.key ?? "",
    base_url: channel.base_url ?? "",
    models: channel.models ?? "",
    model_mapping: channel.model_mapping ?? "",
    weight: String(channel.weight ?? 1),
    priority: String(channel.priority ?? 0),
    status: String(channel.status ?? 1),
    setting: channel.setting ?? "",
    organization: channel.organization ?? "",
    api_version: channel.api_version ?? "",
    tag: channel.tag ?? "",
    remark: channel.remark ?? "",
    test_model: channel.test_model ?? "",
    auto_ban: String(channel.auto_ban ?? 0),
    status_code_mapping: channel.status_code_mapping ?? "",
    param_override: channel.param_override ?? "",
    header_override: channel.header_override ?? "",
    other_settings: channel.other_settings ?? "",
    supported_api_types: channel.supported_api_types ?? "",
    endpoints: channel.endpoints ?? "",
    passthrough_enabled: !!channel.passthrough_enabled,
    use_legacy_adaptor: !!channel.use_legacy_adaptor,
    system_prompt: channel.system_prompt ?? "",
    proxy_url: channel.proxy_url ?? "",
    role_mapping: channel.role_mapping ?? "",
    system_prompt_in_input: !!channel.system_prompt_in_input,
  };
}
