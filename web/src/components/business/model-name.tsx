"use client";

import { getModelProvider, getProviderIconKey } from "@/lib/constants";
import { ProviderAvatar } from "@/components/business/provider-avatar";
import { cn } from "@/lib/utils";

interface ModelNameProps {
  name: string;
  size?: number;
  className?: string;
}

export function ModelName({ name, size = 14, className }: ModelNameProps) {
  const provider = getModelProvider(name);
  const iconKey = provider ? getProviderIconKey(provider) : null;
  return (
    <span className={cn("inline-flex items-center gap-1", className)}>
      {iconKey && <ProviderAvatar provider={iconKey} size={size} />}
      <span className="font-mono text-xs">{name}</span>
    </span>
  );
}
