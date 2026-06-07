"use client";

import { useMemo } from "react";
import { useTranslations } from "next-intl";
import { Badge } from "@/components/ui/badge";
import { useChannels } from "@/lib/api/channels";
import type { AdminScript } from "@/lib/types";

// ScopeBadge 紧凑展示脚本作用域：渠道名 chip + 模型名；全空则显示"全局"。
// 渠道名通过一次性的 useChannels 列表解析（react-query 按 key 去重，多行共享一次请求）；
// 未命中/加载中回退 #id。
// page_size 取 500：渠道数预期 < 500，一次拉全量建 id→name 映射。
export function ScopeBadge({ scope }: { scope: AdminScript["scope"] }) {
  const t = useTranslations("scripts");
  const ch = scope?.channel_ids ?? [];
  const md = scope?.model_names ?? [];
  const { data } = useChannels({ page_size: 500 });
  const nameById = useMemo(
    () => new Map((data?.data ?? []).map((c) => [c.id, c.name])),
    [data],
  );
  if (ch.length === 0 && md.length === 0) {
    return <Badge variant="outline" className="text-xs">{t("scopeAll")}</Badge>;
  }
  return (
    <div className="flex flex-wrap items-center gap-1">
      {ch.map((c) => (
        <Badge key={`c${c}`} variant="secondary" className="text-xs">
          {nameById.get(c) ?? `#${c}`}
        </Badge>
      ))}
      {md.map((m) => (
        <Badge key={`m${m}`} variant="outline" className="text-xs">{m}</Badge>
      ))}
    </div>
  );
}
