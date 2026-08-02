"use client";

import { ProviderAvatar } from "@/components/business/provider-avatar";
import { getModelProvider, getProviderIconKey } from "@/lib/constants";

interface ModelProviderLogoProps {
  modelName: string;
  size?: number;
  className?: string;
}

export function ModelProviderLogo({
  modelName,
  size = 14,
  className,
}: ModelProviderLogoProps) {
  const provider = getModelProvider(modelName);
  const iconKey = provider ? getProviderIconKey(provider) : null;

  return iconKey ? (
    <span className={className} aria-hidden="true">
      <ProviderAvatar provider={iconKey} size={size} />
    </span>
  ) : null;
}
