"use client";

import type { ReactNode } from "react";
import { useTranslations } from "next-intl";
import { User as UserIcon, Server, KeyRound, Cpu, type LucideIcon } from "lucide-react";

import { StatusBadge, RoleBadge } from "@/components/business/status-badge";
import { AgentConnectionStatus } from "@/components/business/agent-connection-status";
import { ChannelBillingBadge, billingBadge } from "@/components/business/channel-billing-badge";
import { CopyableText } from "@/components/business/copyable-text";
import { EntityLabel } from "@/components/business/entity-label";
import type { EntityName } from "@/components/business/entity-picker/registry";
import type { User, Channel, Token, Agent } from "@/lib/types";
import { formatMoneyCompact } from "@/lib/utils/format";

/** hover body 内一行：左标签右值。 */
function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="text-muted-foreground shrink-0">{label}</span>
      <span className="text-right break-all">{children}</span>
    </div>
  );
}

function UserBody({ item }: { item: User }) {
  const t = useTranslations("entityHover");
  return (
    <div className="flex flex-col gap-1">
      {item.email && <Field label={t("email")}>{item.email}</Field>}
      <Field label={t("role")}><RoleBadge role={item.role} /></Field>
      <Field label={t("balance")}>{formatMoneyCompact(item.quota)}</Field>
      {item.group_name && <Field label={t("group")}>{item.group_name}</Field>}
    </div>
  );
}

function ChannelBody({ item }: { item: Channel }) {
  const t = useTranslations("entityHover");
  return (
    <div className="flex flex-col gap-1">
      {billingBadge(item).kind !== "none" && (
        <Field label={t("billing")}><ChannelBillingBadge channel={item} /></Field>
      )}
      {item.tag && <Field label={t("tags")}>{item.tag}</Field>}
    </div>
  );
}

function TokenBody({ item }: { item: Token }) {
  const t = useTranslations("entityHover");
  const modelCount = item.models
    ? item.models.split(",").map((s) => s.trim()).filter(Boolean).length
    : 0;
  return (
    <div className="flex flex-col gap-1">
      <Field label={t("owner")}>
        <EntityLabel entity="user" id={item.user_id} hover={false} />
      </Field>
      <Field label={t("expiresAt")}>
        {item.expired_at > 0
          ? new Date(item.expired_at * 1000).toLocaleDateString()
          : t("expiresNever")}
      </Field>
      <Field label={t("models")}>{modelCount > 0 ? modelCount : t("modelsAll")}</Field>
    </div>
  );
}

function AgentBody({ item }: { item: Agent }) {
  const t = useTranslations("entityHover");
  const tags = item.tags ? item.tags.split(",").map((s) => s.trim()).filter(Boolean) : [];
  return (
    <div className="flex flex-col gap-1">
      <Field label={t("nodeId")}><CopyableText text={item.agent_id} /></Field>
      {tags.length > 0 && <Field label={t("tags")}>{tags.join(", ")}</Field>}
    </div>
  );
}

/** 按实体渲染 hover body。只注册 user/channel/token/agent；其余实体不弹富 hover。 */
export const ENTITY_HOVER_BODIES: Partial<Record<EntityName, (item: unknown) => ReactNode>> = {
  user: (item) => <UserBody item={item as User} />,
  channel: (item) => <ChannelBody item={item as Channel} />,
  token: (item) => <TokenBody item={item as Token} />,
  agent: (item) => <AgentBody item={item as Agent} />,
};

/** 各实体的类型图标(hover 卡头部)。 */
export const ENTITY_TYPE_ICONS: Partial<Record<EntityName, LucideIcon>> = {
  user: UserIcon,
  channel: Server,
  token: KeyRound,
  agent: Cpu,
};

/** 各实体的状态元素(提到 hover 卡头部做 pill)。访问器只返回元素,不直接调 hook。 */
export const ENTITY_HOVER_STATUS: Partial<Record<EntityName, (item: unknown) => ReactNode>> = {
  user: (i) => <StatusBadge status={(i as User).status} />,
  channel: (i) => <StatusBadge status={(i as Channel).status} />,
  token: (i) => <StatusBadge status={(i as Token).status} />,
  agent: (i) => <AgentConnectionStatus kind="control" value={(i as Agent).connection.control} />,
};
