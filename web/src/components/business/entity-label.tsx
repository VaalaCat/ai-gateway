"use client";

import { cn } from "@/lib/utils";
import { ENTITY_ADAPTERS, type EntityName } from "@/components/business/entity-picker/registry";
import type { AdminScope, EntityAdapter } from "@/components/business/entity-picker/types";

interface EntityLabelProps {
  entity: EntityName;
  id: string | number | undefined | null;
  scope?: AdminScope;
  className?: string;
  /** 名字后是否附灰色 #id(默认 true)。value-即-label 的实体(如 model)自动不显示。 */
  showId?: boolean;
}

export function EntityLabel({
  entity,
  id,
  scope = "self",
  className,
  showId = true,
}: EntityLabelProps) {
  const adapter = ENTITY_ADAPTERS[entity] as unknown as EntityAdapter<unknown>;
  const idStr = id == null || id === "" ? "" : String(id);
  const one = adapter.useOne(idStr, { scope });

  if (!idStr) {
    return <span className={cn("text-muted-foreground", className)}>-</span>;
  }

  const resolved = one.data ? adapter.getLabel(one.data) : undefined;
  const label = resolved ?? adapter.labelForValue?.(idStr);
  // label 来自 labelForValue 说明 value 本身即名字(如 model_name),不再追加 #id
  const isValueLabel = !resolved && label != null;

  if (!label) {
    return <span className={cn("text-muted-foreground", className)}>#{idStr}</span>;
  }

  return (
    <span className={className}>
      {label}
      {showId && !isValueLabel && (
        <span className="text-muted-foreground ml-1">#{idStr}</span>
      )}
    </span>
  );
}
