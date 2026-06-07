"use client";

import { Copy } from "lucide-react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";
import { ENTITY_ADAPTERS, type EntityName } from "@/components/business/entity-picker/registry";
import type { EntityAdapter } from "@/components/business/entity-picker/types";
import { ENTITY_HOVER_BODIES } from "./bodies";

/** 该实体是否有富 hover body（EntityLabel 据此决定是否包 HoverCard）。 */
export function hasEntityHoverBody(entity: EntityName): boolean {
  return entity in ENTITY_HOVER_BODIES;
}

/** 富实体 hover 卡片：头部 名字 #id [复制] + 按实体 body。 */
export function EntityHoverCard({
  entity,
  item,
  id,
}: {
  entity: EntityName;
  item: unknown;
  id: string;
}) {
  const t = useTranslations("entityHover");
  const tc = useTranslations("common");
  const adapter = ENTITY_ADAPTERS[entity] as unknown as EntityAdapter<unknown>;
  const name = adapter.getLabel(item);
  const body = ENTITY_HOVER_BODIES[entity];

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <span className="font-medium truncate">{name}</span>
        <span className="flex items-center gap-1 shrink-0 text-muted-foreground">
          #{id}
          <Button
            variant="ghost"
            size="icon"
            className="size-5"
            aria-label={t("copyId")}
            onClick={() =>
              copyTextWithFeedback(id, { success: tc("copied"), error: tc("copyFailed") })
            }
          >
            <Copy className="size-3" />
          </Button>
        </span>
      </div>
      {body?.(item)}
    </div>
  );
}
