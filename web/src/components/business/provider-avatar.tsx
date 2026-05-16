"use client";

import { ProviderIcon } from "@lobehub/icons";
import { ComponentType, lazy, Suspense } from "react";

// Providers supported by ProviderIcon (via providerEnum/providerConfig)
const PROVIDER_ICON_SUPPORTED = new Set([
  "openai", "anthropic", "google", "deepseek", "qwen", "meta", "mistral",
  "cohere", "xai", "perplexity", "nvidia", "microsoft", "stability", "bfl",
  "hunyuan", "zhipu", "zeroone", "moonshot", "baidu", "doubao", "stepfun",
  "minimax", "baichuan", "bytedance", "ai21", "ibm", "nousresearch",
]);

// Providers NOT in ProviderIcon but have Avatar components in @lobehub/icons
// These use lazy-loaded Avatar imports as fallback
const AVATAR_FALLBACKS: Record<string, () => Promise<{ default: ComponentType<{ size?: number }> }>> = {
  aws: () => import("@lobehub/icons").then(m => ({ default: (m as Record<string, any>).Aws?.Avatar })),
  baai: () => import("@lobehub/icons").then(m => ({ default: (m as Record<string, any>).BAAI?.Avatar })),
  aionlabs: () => import("@lobehub/icons").then(m => ({ default: (m as Record<string, any>).AionLabs?.Avatar })),
  inflection: () => import("@lobehub/icons").then(m => ({ default: (m as Record<string, any>).Inflection?.Avatar })),
  voyage: () => import("@lobehub/icons").then(m => ({ default: (m as Record<string, any>).Voyage?.Avatar })),
};

// Cache lazy components
const lazyCache = new Map<string, ComponentType<{ size?: number }>>();
function getLazyAvatar(key: string) {
  if (!lazyCache.has(key) && AVATAR_FALLBACKS[key]) {
    lazyCache.set(key, lazy(AVATAR_FALLBACKS[key]));
  }
  return lazyCache.get(key);
}

interface ProviderAvatarProps {
  provider: string; // lobehub icon key (lowercase)
  size?: number;
}

export function ProviderAvatar({ provider, size = 14 }: ProviderAvatarProps) {
  const key = provider.toLowerCase();

  // Try ProviderIcon first (fast path for most providers)
  if (PROVIDER_ICON_SUPPORTED.has(key)) {
    return <ProviderIcon provider={key} size={size} />;
  }

  // Fallback to lazy Avatar for unsupported providers
  const LazyAvatar = getLazyAvatar(key);
  if (LazyAvatar) {
    return (
      <Suspense fallback={<span style={{ width: size, height: size }} />}>
        <LazyAvatar size={size} />
      </Suspense>
    );
  }

  return null;
}
