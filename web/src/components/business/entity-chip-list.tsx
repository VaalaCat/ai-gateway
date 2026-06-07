"use client";

import { Badge } from "@/components/ui/badge";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { EntityLabel } from "@/components/business/entity-label";
import { cn } from "@/lib/utils";
import type { EntityName } from "@/components/business/entity-picker/registry";

interface EntityChipListProps {
  entity: EntityName;
  ids: Array<number | string>;
  /** 行内最多直接展示几个，超出折进 +N Popover。默认 2。 */
  max?: number;
  /** ids 为空时展示的文案（如"所有组"）。 */
  emptyLabel?: string;
  className?: string;
}

function chip(entity: EntityName, id: number | string) {
  return (
    <Badge key={String(id)} variant="secondary" className="font-normal">
      <EntityLabel entity={entity} id={String(id)} showId={false} hover={false} className="truncate" />
    </Badge>
  );
}

export function EntityChipList({ entity, ids, max = 2, emptyLabel, className }: EntityChipListProps) {
  if (!ids || ids.length === 0) {
    return (
      <Badge variant="outline" className="font-normal text-muted-foreground">
        {emptyLabel ?? "-"}
      </Badge>
    );
  }

  const shown = ids.slice(0, max);
  const rest = ids.slice(max);

  return (
    <div className={cn("flex flex-wrap items-center gap-1", className)}>
      {shown.map((id) => chip(entity, id))}
      {rest.length > 0 && (
        <Popover>
          <PopoverTrigger asChild>
            <Badge variant="outline" className="cursor-pointer font-normal">
              +{rest.length}
            </Badge>
          </PopoverTrigger>
          <PopoverContent align="start" className="w-56 p-2">
            <div className="flex flex-wrap gap-1">{ids.map((id) => chip(entity, id))}</div>
          </PopoverContent>
        </Popover>
      )}
    </div>
  );
}
