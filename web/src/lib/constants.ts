export const STATUS = {
  ENABLED: 1,
  DISABLED: 2,
} as const;

export const ROLE = {
  USER: 1,
  ADMIN: 2,
} as const;

// Channel provider types (match backend database values)
export const CHANNEL_TYPES = {
  OPENAI: 1,
  AZURE: 3,
  ANTHROPIC: 14,
  AWS: 33,
  VERTEX_AI: 41,
} as const;

// API type identifiers (match backend consts)
export const API_TYPES = {
  CHAT_COMPLETION: "chat-completion",
  RESPONSES: "responses",
  CLAUDE: "claude",
} as const;

// LocalStorage / Cookie key names
export const STORAGE_KEYS = {
  TOKEN: "token",
  LOCALE: "locale",
} as const;

// HTTP header names
export const HTTP_HEADERS = {
  CONTENT_TYPE: "Content-Type",
  AUTHORIZATION: "Authorization",
} as const;

// Chat message roles
export const CHAT_ROLES = {
  USER: "user",
  ASSISTANT: "assistant",
  SYSTEM: "system",
} as const;

// Default page sizes
export const PAGE_SIZES = {
  DEFAULT: 20,
  LOGS: 10,
} as const;

// Model name prefix → @lobehub/icons provider key
export const MODEL_PROVIDER_PREFIXES: [string, string][] = [
  // OpenAI
  ["gpt", "openai"],
  ["o1", "openai"],
  ["o3", "openai"],
  ["o4", "openai"],
  ["chatgpt", "openai"],
  ["dall-e", "openai"],
  ["codex", "openai"],
  ["sora", "openai"],
  ["whisper", "openai"],
  // Anthropic
  ["claude", "anthropic"],
  // Google
  ["gemini", "google"],
  ["gemma", "google"],
  ["imagen", "google"],
  ["veo", "google"],
  // Meta
  ["llama", "meta"],
  // DeepSeek
  ["deepseek", "deepseek"],
  // Qwen / Alibaba
  ["qwen", "qwen"],
  ["qwq", "qwen"],
  ["qvq", "qwen"],
  // Mistral
  ["mistral", "mistral"],
  ["codestral", "mistral"],
  ["pixtral", "mistral"],
  ["devstral", "mistral"],
  ["magistral", "mistral"],
  ["voxtral", "mistral"],
  // Cohere
  ["command", "cohere"],
  // xAI
  ["grok", "xai"],
  // Perplexity
  ["sonar", "perplexity"],
  // Nvidia
  ["nemotron", "nvidia"],
  // Microsoft
  ["phi-", "microsoft"],
  // Amazon
  ["nova", "amazon"],
  ["titan", "amazon"],
  // Stability
  ["stable-diffusion", "stability"],
  ["flux", "black-forest-labs"],
  // Chinese providers
  ["hunyuan", "hunyuan"],
  ["glm", "zhipu"],
  ["yi-", "01ai"],
  ["moonshot", "moonshot"],
  ["kimi", "moonshot"],
  ["ernie", "baidu"],
  ["doubao", "doubao"],
  ["step", "stepfun"],
  ["minimax", "minimax"],
  ["baichuan", "baichuan"],
  ["seed", "bytedance"],
  // Other
  ["jamba", "ai21"],
  ["reka", "reka"],
  ["granite", "ibm"],
  ["voyage", "voyage"],
];

// Internal provider key → { display name, lobehub icon key }
// Only providers with lobehub icons are listed (enterprise-grade icon support)
export const PROVIDER_INFO: Record<string, { display: string; icon: string }> = {
  openai: { display: "OpenAI", icon: "OpenAI" },
  anthropic: { display: "Anthropic", icon: "Anthropic" },
  google: { display: "Google", icon: "Google" },
  deepseek: { display: "DeepSeek", icon: "DeepSeek" },
  qwen: { display: "Qwen", icon: "Qwen" },
  meta: { display: "Meta", icon: "Meta" },
  mistral: { display: "Mistral", icon: "Mistral" },
  cohere: { display: "Cohere", icon: "Cohere" },
  xai: { display: "xAI", icon: "XAI" },
  perplexity: { display: "Perplexity", icon: "Perplexity" },
  nvidia: { display: "Nvidia", icon: "Nvidia" },
  microsoft: { display: "Microsoft", icon: "Microsoft" },
  amazon: { display: "Amazon", icon: "aws" },
  stability: { display: "Stability AI", icon: "Stability" },
  "black-forest-labs": { display: "Black Forest Labs", icon: "Bfl" },
  hunyuan: { display: "Hunyuan", icon: "Hunyuan" },
  zhipu: { display: "Zhipu", icon: "Zhipu" },
  "01ai": { display: "01.AI", icon: "zeroone" },
  moonshot: { display: "Moonshot", icon: "Moonshot" },
  baidu: { display: "Baidu", icon: "Baidu" },
  doubao: { display: "Doubao", icon: "Doubao" },
  stepfun: { display: "StepFun", icon: "Stepfun" },
  minimax: { display: "MiniMax", icon: "Minimax" },
  baichuan: { display: "Baichuan", icon: "Baichuan" },
  bytedance: { display: "ByteDance", icon: "ByteDance" },
  ai21: { display: "AI21", icon: "Ai21" },
  ibm: { display: "IBM", icon: "IBM" },
  nousresearch: { display: "NousResearch", icon: "nousresearch" },
  baai: { display: "BAAI", icon: "baai" },
  inflection: { display: "Inflection", icon: "inflection" },
  aion: { display: "Aion Labs", icon: "aionlabs" },
  voyage: { display: "Voyage", icon: "voyage" },
};

// Backward compat
export const PROVIDER_DISPLAY_NAMES: Record<string, string> = Object.fromEntries(
  Object.entries(PROVIDER_INFO).map(([k, v]) => [k, v.display])
);

// Get lobehub icon key for a provider
export function getProviderIconKey(provider: string): string | null {
  return PROVIDER_INFO[provider]?.icon ?? null;
}

// Path prefix → provider (for "nvidia/xxx", "meta-llama/xxx" style names)
const PATH_PROVIDER_MAP: Record<string, string> = {
  "openai": "openai",
  "anthropic": "anthropic",
  "google": "google",
  "meta-llama": "meta",
  "meta": "meta",
  "nvidia": "nvidia",
  "microsoft": "microsoft",
  "mistralai": "mistral",
  "deepseek-ai": "deepseek",
  "deepseek": "deepseek",
  "qwen": "qwen",
  "cohere": "cohere",
  "perplexity": "perplexity",
  "nousresearch": "nousresearch",
  "bytedance-seed": "bytedance",
  "bytedance": "bytedance",
  "baai": "baai",
  "black-forest-labs": "black-forest-labs",
  "stabilityai": "stability",
  "01-ai": "01ai",
  "zhipuai": "zhipu",
  "minimaxi": "minimax",
  "minimaxai": "minimax",
  "paddlepaddle": "baidu",
  "aion-labs": "aion",
  "inflection": "inflection",
  "ai21labs": "ai21",
  "amazon": "amazon",
  "ibm": "ibm",
  "moonshotai": "moonshot",
  "zai-org": "zhipu",
  "zai": "zhipu",
};

export function getModelProvider(modelName: string): string | null {
  const lower = modelName.toLowerCase();
  const segments = lower.split("/");

  // 1. Check each path segment against PATH_PROVIDER_MAP
  // e.g. "Pro/zai-org/GLM-4.7" → check "pro", "zai-org", "glm-4.7"
  for (const seg of segments) {
    if (PATH_PROVIDER_MAP[seg]) return PATH_PROVIDER_MAP[seg];
  }

  // 2. Check last segment (base model name) against prefix list
  const baseName = segments[segments.length - 1];
  for (const [prefix, provider] of MODEL_PROVIDER_PREFIXES) {
    if (baseName.startsWith(prefix)) return provider;
  }

  return null;
}

export function groupModelsByProvider(models: string[]): { provider: string | null; displayName: string; models: string[] }[] {
  const groups = new Map<string, string[]>();

  for (const model of models) {
    const provider = getModelProvider(model) ?? "_other";
    if (!groups.has(provider)) groups.set(provider, []);
    groups.get(provider)!.push(model);
  }

  const known: { provider: string | null; displayName: string; models: string[] }[] = [];
  let other: { provider: string | null; displayName: string; models: string[] } | null = null;

  for (const [key, groupModels] of groups) {
    if (key === "_other") {
      other = { provider: null, displayName: "Other", models: groupModels.sort() };
    } else {
      known.push({ provider: key, displayName: PROVIDER_DISPLAY_NAMES[key] ?? key, models: groupModels.sort() });
    }
  }

  known.sort((a, b) => b.models.length - a.models.length);
  return other ? [...known, other] : known;
}
