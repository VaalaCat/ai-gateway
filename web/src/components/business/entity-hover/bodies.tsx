"use client";

import type { ReactNode } from "react";
import { useTranslations } from "next-intl";

import { StatusBadge, RoleBadge, OnlineBadge } from "@/components/business/status-badge";
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
    <div className="space-y-1">
      {item.email && <Field label={t("email")}>{item.email}</Field>}
      <Field label={t("role")}><RoleBadge role={item.role} /></Field>
      <Field label={t("status")}><StatusBadge status={item.status} /></Field>
      <Field label={t("balance")}>{formatMoneyCompact(item.quota - item.used_quota)}</Field>
      {item.group_name && <Field label={t("group")}>{item.group_name}</Field>}
    </div>
  );
}

function ChannelBody({ item }: { item: Channel }) {
  const t = useTranslations("entityHover");
  return (
    <div className="space-y-1">
      <Field label={t("status")}><StatusBadge status={item.status} /></Field>
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
    <div className="space-y-1">
      <Field label={t("owner")}>
        <EntityLabel entity="user" id={item.user_id} hover={false} />
      </Field>
      <Field label={t("status")}><StatusBadge status={item.status} /></Field>
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
    <div className="space-y-1">
      <Field label={t("nodeId")}><CopyableText text={item.agent_id} /></Field>
      <Field label={t("online")}><OnlineBadge lastSeen={item.last_seen} /></Field>
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
