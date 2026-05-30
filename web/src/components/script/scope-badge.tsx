"use client";

import { useTranslations } from "next-intl";
import { Badge } from "@/components/ui/badge";
import type { AdminScript } from "@/lib/types";

// ScopeBadge 紧凑展示脚本作用域：频道 #id chip + 模型名；全空则显示"全局"。
export function ScopeBadge({ scope }: { scope: AdminScript["scope"] }) {
  const t = useTranslations("scripts");
  const ch = scope?.channel_ids ?? [];
  const md = scope?.model_names ?? [];
  if (ch.length === 0 && md.length === 0) {
    return <Badge variant="outline" className="text-xs">{t("scopeAll")}</Badge>;
  }
  return (
    <div className="flex flex-wrap items-center gap-1">
      {ch.map((c) => (
        <Badge key={`c${c}`} variant="secondary" className="text-xs">#{c}</Badge>
      ))}
      {md.map((m) => (
        <Badge key={`m${m}`} variant="outline" className="text-xs">{m}</Badge>
      ))}
    </div>
  );
}
