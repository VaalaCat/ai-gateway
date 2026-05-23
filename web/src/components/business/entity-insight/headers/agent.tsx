"use client";

import { useTranslations } from "next-intl";

import { Card, CardContent } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import type { EntityMeta } from "@/lib/api/insights";

export function AgentHeader({ meta }: { meta: EntityMeta }) {
  const t = useTranslations("insights.header");
  return (
    <Card>
      <CardContent className="flex flex-wrap items-center gap-3 p-4">
        <span
          className={`size-2 rounded-full ${meta.online ? "bg-emerald-500" : "bg-muted"}`}
        />
        <span className="truncate font-medium">{meta.name || meta.id}</span>
        {meta.last_seen ? (
          <span className="text-xs text-muted-foreground">
            {t("lastSeen")} {new Date(meta.last_seen * 1000).toLocaleString()}
          </span>
        ) : null}
        {meta.region ? (
          <>
            <Separator orientation="vertical" className="mx-2 h-5" />
            <span className="text-xs text-muted-foreground">{meta.region}</span>
          </>
        ) : null}
        {meta.version ? (
          <span className="text-xs text-muted-foreground">v{meta.version}</span>
        ) : null}
      </CardContent>
    </Card>
  );
}
