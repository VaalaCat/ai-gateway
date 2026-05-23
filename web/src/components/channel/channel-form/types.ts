import type {
  ChannelOtherSettings,
  ChannelSettings,
  BuiltinToolFallbackPolicy,
} from "@/lib/types";

export interface ChannelForm {
  name: string;
  type: string;
  key: string;
  base_url: string;
  models: string;
  model_mapping: string;
  weight: string;
  priority: string;
  status: string;
  setting: string;
  organization: string;
  api_version: string;
  tag: string;
  remark: string;
  test_model: string;
  auto_ban: string;
  status_code_mapping: string;
  param_override: string;
  header_override: string;
  other_settings: string;
  supported_api_types: string;
  endpoints: string;
  passthrough_enabled: boolean;
  use_legacy_adaptor: boolean;
  system_prompt: string;
  proxy_url: string;
  role_mapping: string;
  system_prompt_in_input: boolean;
}

export const emptyForm: ChannelForm = {
  name: "",
  type: "1",
  key: "",
  base_url: "",
  models: "",
  model_mapping: "",
  weight: "1",
  priority: "0",
  status: "1",
  setting: "",
  organization: "",
  api_version: "",
  tag: "",
  remark: "",
  test_model: "",
  auto_ban: "0",
  status_code_mapping: "",
  param_override: "",
  header_override: "",
  other_settings: "",
  supported_api_types: "",
  endpoints: "",
  passthrough_enabled: false,
  use_legacy_adaptor: false,
  system_prompt: "",
  proxy_url: "",
  role_mapping: "",
  system_prompt_in_input: false,
};

export interface EndpointConfig {
  chat_completions?: string;
  responses?: string;
  messages?: string;
  models?: string;
}

export const ENDPOINT_DEFAULTS: Record<string, string> = {
  chat_completions: "/v1/chat/completions",
  responses: "/v1/responses",
  messages: "/v1/messages",
  models: "/v1/models",
};

export const ENDPOINT_OPTIONS = [
  { key: "chat_completions" as const, labelKey: "apiTypeChatCompletion" as const, default: "/v1/chat/completions" },
  { key: "responses" as const, labelKey: "apiTypeResponses" as const, default: "/v1/responses" },
  { key: "messages" as const, labelKey: "apiTypeClaude" as const, default: "/v1/messages" },
  { key: "models" as const, labelKey: "apiTypeModels" as const, default: "/v1/models" },
];

export type ChannelProtocols = {
  claude: boolean;
  openaiChat: boolean;
  openaiResponses: boolean;
};

// Re-export from lib/types so consumers don't import from two places.
export type { ChannelOtherSettings, ChannelSettings, BuiltinToolFallbackPolicy };
