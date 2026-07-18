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
type AvatarSizeProps = { size: number };

const AVATAR_FALLBACKS: Record<string, () => Promise<{ default: ComponentType<AvatarSizeProps> }>> = {
  aws: async () => {
    const Avatar = (await import("@lobehub/icons")).Aws.Avatar;
    return { default: function AwsAvatar(props: AvatarSizeProps) { return <Avatar {...props} />; } };
  },
  baai: async () => {
    const Avatar = (await import("@lobehub/icons")).BAAI.Avatar;
    return { default: function BAAIAvatar(props: AvatarSizeProps) { return <Avatar {...props} />; } };
  },
  aionlabs: async () => {
    const Avatar = (await import("@lobehub/icons")).AionLabs.Avatar;
    return { default: function AionLabsAvatar(props: AvatarSizeProps) { return <Avatar {...props} />; } };
  },
  inflection: async () => {
    const Avatar = (await import("@lobehub/icons")).Inflection.Avatar;
    return { default: function InflectionAvatar(props: AvatarSizeProps) { return <Avatar {...props} />; } };
  },
  voyage: async () => {
    const Avatar = (await import("@lobehub/icons")).Voyage.Avatar;
    return { default: function VoyageAvatar(props: AvatarSizeProps) { return <Avatar {...props} />; } };
  },
};

const LAZY_AVATARS = Object.fromEntries(
  Object.entries(AVATAR_FALLBACKS).map(([key, load]) => [key, lazy(load)]),
) as Record<string, ComponentType<AvatarSizeProps>>;

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
  const LazyAvatar = LAZY_AVATARS[key];
  if (LazyAvatar) {
    return (
      <Suspense fallback={<span style={{ width: size, height: size }} />}>
        <LazyAvatar size={size} />
      </Suspense>
    );
  }

  return null;
}
