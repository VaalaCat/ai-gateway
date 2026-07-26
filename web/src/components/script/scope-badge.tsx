"use client";

import { useTranslations } from "next-intl";
import { Badge } from "@/components/ui/badge";
import { EntityChipList } from "@/components/business/entity-chip-list";
import type { AdminScript } from "@/lib/types";

// 紧凑展示非空作用域；实体未加载或已删除时由 EntityLabel 回退为 #ID。
export function ScopeBadge({ scope }: { scope: AdminScript["scope"] }) {
  const t = useTranslations("scripts");
  const channelIds = scope?.channel_ids ?? [];
  const privateChannelIds = scope?.private_channel_ids ?? [];
  const modelNames = scope?.model_names ?? [];
  const groupIds = scope?.group_ids ?? [];
  const userIds = scope?.user_ids ?? [];
  const isGlobal = [channelIds, privateChannelIds, modelNames, groupIds, userIds]
    .every((values) => values.length === 0);

  if (isGlobal) {
    return <Badge variant="outline" className="text-xs">{t("scopeAll")}</Badge>;
  }

  return (
    <div className="flex flex-wrap items-center gap-1">
      {channelIds.length > 0 && <EntityChipList entity="channel" ids={channelIds} />}
      {privateChannelIds.length > 0 && (
        <EntityChipList entity="byok-channel" ids={privateChannelIds} scope="all" />
      )}
      {groupIds.length > 0 && <EntityChipList entity="user-group" ids={groupIds} />}
      {userIds.length > 0 && <EntityChipList entity="user" ids={userIds} />}
      {modelNames.map((m) => (
        <Badge key={`m${m}`} variant="outline" className="text-xs">{m}</Badge>
      ))}
    </div>
  );
}
