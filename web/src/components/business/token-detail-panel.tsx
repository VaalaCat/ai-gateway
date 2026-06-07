"use client";

import { useTranslations } from "next-intl";
import { Badge } from "@/components/ui/badge";
import { CopyableText } from "@/components/business/copyable-text";
import { DateCell } from "@/components/business/date-cell";
import { EntityLabel } from "@/components/business/entity-label";
import { TokenAvailableModels } from "@/components/business/token-available-models";
import { useAuth } from "@/lib/auth";
import { cn } from "@/lib/utils";
import { parseModels } from "@/lib/parse-models";
import type { Token } from "@/lib/types";

interface TokenDetailPanelProps {
  token: Token;
}

export function TokenDetailPanel({ token }: TokenDetailPanelProps) {
  const t = useTranslations("tokenDetail");
  const tCommon = useTranslations("common");
  const { isAdmin } = useAuth();

  const filterRules = parseModels(token.models ?? "");
  const channelIds = token.allowed_channel_ids ?? [];

  return (
    <div className="grid gap-6 py-2 md:grid-cols-2">
      <section className="space-y-2">
        <h3 className="text-sm font-medium">{t("tokenInfo")}</h3>
        <dl className="space-y-1.5 text-sm">
          <Field label={t("fieldName")}>
            <span className="font-mono break-all">{token.name}</span>
          </Field>
          <Field label={t("fieldKey")}>
            <span onClick={(e) => e.stopPropagation()} className="inline-block">
              <CopyableText
                text={token.key}
                display={token.key.slice(0, 12) + "..."}
                revealable
              />
            </span>
          </Field>
          {isAdmin && (
            <Field label={t("fieldOwner")}>
              <EntityLabel entity="user" id={token.user_id} />
            </Field>
          )}
          <Field label={t("fieldStatus")}>
            <span className={cn("inline-flex items-center gap-1", token.status === 1 ? "text-green-600" : "text-destructive")}>
              <span className="inline-block size-1.5 rounded-full bg-current" />
              {token.status === 1 ? tCommon("enabled") : tCommon("disabled")}
            </span>
          </Field>
          <Field label={t("fieldExpires")}>
            {token.expired_at
              ? <DateCell timestamp={token.expired_at} />
              : <span className="text-muted-foreground">{t("fieldNeverExpires")}</span>}
          </Field>
          <Field label={t("fieldTemplate")}>
            {token.template_id
              ? <span className="font-mono">#{token.template_id}</span>
              : <span className="text-muted-foreground">-</span>}
          </Field>
          {token.trace_enabled && (
            <Field label={t("fieldTrace")}>
              <Badge variant="secondary">{tCommon("enabled")}</Badge>
            </Field>
          )}
          {channelIds.length > 0 && (
            <Field label={t("fieldChannels")}>
              <span className="flex flex-wrap gap-x-2 gap-y-1">
                {channelIds.map((id) => (
                  <EntityLabel key={id} entity="channel" id={id} className="font-mono text-sm" />
                ))}
              </span>
            </Field>
          )}
          <Field label={t("fieldFilterRule")}>
            {filterRules.length === 0
              ? <span className="text-muted-foreground italic">{t("fieldFilterRuleNone")}</span>
              : <span className="font-mono break-all">{filterRules.join(", ")}</span>}
          </Field>
          <Field label={t("fieldCreated")}>
            <DateCell timestamp={token.created_at} />
          </Field>
          <Field label={t("fieldUpdated")}>
            <DateCell timestamp={token.updated_at} />
          </Field>
        </dl>
      </section>
      <TokenAvailableModels tokenKey={token.key} />
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline gap-3">
      <dt className="w-24 shrink-0 text-xs text-muted-foreground">{label}</dt>
      <dd className="flex-1 min-w-0">{children}</dd>
    </div>
  );
}
