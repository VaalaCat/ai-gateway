"use client";

import { useTranslations } from "next-intl";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Field, FieldDescription, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { AuthType, ChannelAuthProtocol } from "@/lib/types";
import { parseChannelAuth, stringifyChannelAuth } from "./utils";

const AUTH_PROTOCOLS = [
  { key: "chat_completions", label: "Chat Completions" },
  { key: "responses", label: "Responses" },
  { key: "messages", label: "Messages" },
  { key: "generate_content", label: "Gemini" },
] as const;

// UpstreamAuthEditor 编辑整体认证和按出站协议的覆盖。
export function UpstreamAuthEditor({ value, onChange }: {
  value: string;
  onChange: (value: string) => void;
}) {
  // 1. 读取表单配置，按整组更新或删除协议覆盖。
  const t = useTranslations("channels");
  const auth = parseChannelAuth(value);
  const updateEntry = (protocol: ChannelAuthProtocol | "default", entry?: AuthType) => {
    if (protocol === "default") {
      onChange(stringifyChannelAuth({ ...auth, default: entry }));
      return;
    }
    const protocols = { ...auth.protocols };
    if (entry) protocols[protocol] = entry;
    else delete protocols[protocol];
    onChange(stringifyChannelAuth({ ...auth, protocols }));
  };

  // 2. 展示默认认证、协议覆盖和密钥引用提示。
  return (
    <FieldSet>
      <FieldLegend>{t("upstreamAuth")}</FieldLegend>
      <FieldDescription>{t("upstreamAuthDesc", { channelKey: "${CHANNEL_KEY}" })}</FieldDescription>
      <FieldGroup className="gap-4">
        <AuthEntry id="upstream-auth-default" label={t("upstreamAuthDefault")} inheritedLabel={t("upstreamAuthUseDefault")} entry={auth.default ?? undefined} onChange={(entry) => updateEntry("default", entry)} />
        <FieldSet>
          <FieldLegend variant="label">{t("upstreamAuthProtocols")}</FieldLegend>
          <FieldDescription>{t("upstreamAuthProtocolsDesc")}</FieldDescription>
          <FieldDescription>{t("geminiAuthHint")}</FieldDescription>
          <FieldGroup className="gap-4">
            {AUTH_PROTOCOLS.map(({ key, label }) => (
              <AuthEntry key={key} id={`upstream-auth-${key}`} label={label} inheritedLabel={t("upstreamAuthInherit")} entry={auth.protocols?.[key]} onChange={(entry) => updateEntry(key, entry)} />
            ))}
          </FieldGroup>
        </FieldSet>
        <Alert role="note">
          <AlertDescription>{t("upstreamAuthStorageWarning")}</AlertDescription>
        </Alert>
      </FieldGroup>
    </FieldSet>
  );
}

// AuthEntry 编辑一组 Header 和 Value，切回继承时删除整组。
function AuthEntry({ id, label, inheritedLabel, entry, onChange }: {
  id: string;
  label: string;
  inheritedLabel: string;
  entry?: AuthType;
  onChange: (entry?: AuthType) => void;
}) {
  // 1. 检查当前自定义输入是否完整。
  const t = useTranslations("channels");
  const incomplete = !!entry && (!entry.header.trim() || !entry.value.trim());

  // 2. 选择继承或自定义，并编辑认证原文。
  return (
    <FieldSet>
      <Field>
        <FieldLabel htmlFor={id}>{label}</FieldLabel>
        <Select value={entry ? "custom" : "inherit"} onValueChange={(mode) => onChange(mode === "custom" ? { header: "", value: "" } : undefined)}>
          <SelectTrigger id={id}><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectGroup>
              <SelectItem value="inherit">{inheritedLabel}</SelectItem>
              <SelectItem value="custom">{t("upstreamAuthCustom")}</SelectItem>
            </SelectGroup>
          </SelectContent>
        </Select>
      </Field>
      {entry && (
        <FieldGroup className="gap-4">
          <Field data-invalid={!entry.header.trim() || undefined}>
            <FieldLabel htmlFor={`${id}-header`}>Header · {label}</FieldLabel>
            <Input id={`${id}-header`} value={entry.header} aria-invalid={!entry.header.trim()} autoComplete="off" placeholder="Authorization" onChange={(event) => onChange({ ...entry, header: event.target.value })} />
          </Field>
          <Field data-invalid={!entry.value.trim() || undefined}>
            <FieldLabel htmlFor={`${id}-value`}>Value · {label}</FieldLabel>
            <Input id={`${id}-value`} value={entry.value} aria-invalid={!entry.value.trim()} autoComplete="off" spellCheck={false} placeholder="Bearer ${CHANNEL_KEY}" onChange={(event) => onChange({ ...entry, value: event.target.value })} />
          </Field>
          {incomplete && <FieldDescription role="alert">{t("upstreamAuthIncomplete")}</FieldDescription>}
        </FieldGroup>
      )}
    </FieldSet>
  );
}
