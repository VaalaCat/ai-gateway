"use client";

import { ModelProviderLogo } from "@/components/business/model-provider-logo";
import { cn } from "@/lib/utils";

interface ModelNameProps {
  name: string;
  size?: number;
  className?: string;
}

export function ModelName({ name, size = 14, className }: ModelNameProps) {
  return (
    <span className={cn("inline-flex items-center gap-1", className)}>
      <ModelProviderLogo modelName={name} size={size} />
      <span className="font-mono text-xs">{name}</span>
    </span>
  );
}
